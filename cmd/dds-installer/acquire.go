package main

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Acquire/Verify/Stage is deliberately independent of Apply. It never uses
// the live Companion path, DDS_Data, SQLite, registry, autostart or Plugin path.
const (
	companionLatestURL    = "https://api.github.com/repos/Mr-Dexter-Morgan/DDS_Companion/releases/latest"
	companionAssetsPrefix = "https://github.com/Mr-Dexter-Morgan/DDS_Companion/releases/download/"
	maxFullZIP            = int64(600 << 20)
	maxUnpacked           = int64(1500 << 20) // ZIP-bomb safety ceiling, NOT a disk space requirement.
	stageFreeMargin       = uint64(32 << 20)  // filesystem overhead and modest safety headroom
	maxMembers            = 16000
)

var finalVersionRE = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
var hexDigestRE = regexp.MustCompile(`^[a-f0-9]{64}$`)

type releaseAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
	State  string `json:"state"`
}
type latestRelease struct {
	Tag        string         `json:"tag_name"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}
type stageReceipt struct {
	Schema     string `json:"schema"`
	Component  string `json:"component"`
	Version    string `json:"version"`
	Source     string `json:"source"`
	SHA256     string `json:"sha256"`
	ZIPSize    int64  `json:"zip_size_bytes"`
	Files      int    `json:"verified_files"`
	CreatedUTC string `json:"created_utc"`
	// Content digests of four bootstrap files omitted by DDS.install.json.
	// Missing digests in legacy receipts never establish a safe reusable stage.
	BootstrapSHA256 map[string]string `json:"bootstrap_sha256,omitempty"`
	Status          string            `json:"status"`
}
type transferClient struct {
	HTTP             *http.Client
	LatestURL        string
	PluginURL        string
	BDCLIURL         string
	DiscordURL       string
	AllowTestHTTP    bool // Only injectable in the package's local hermetic tests.
	Progress         func(string)
	DownloadProgress func(downloadProgress)
	FreeBytes        func(string) (uint64, error)
	UsedCachedStage  bool // Cache is verified, but latest online version was not checked.
}

func newTransferClient() *transferClient {
	c := &http.Client{Timeout: 4 * time.Minute}
	c.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) > 8 || r.URL.Scheme != "https" || !allowedGithubHost(r.URL.Hostname()) {
			return errors.New("неразрешённое перенаправление загрузки")
		}
		return nil
	}
	return &transferClient{HTTP: c, LatestURL: companionLatestURL}
}
func allowedGithubHost(host string) bool {
	host = strings.ToLower(host)
	return host == "github.com" || host == "api.github.com" || strings.HasSuffix(host, ".githubusercontent.com") || strings.HasSuffix(host, ".githubassets.com")
}
func (c *transferClient) progress(s string) {
	if c.Progress != nil {
		c.Progress(s)
	}
}
func (c *transferClient) checkURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil {
		return e
	}
	if c.AllowTestHTTP {
		if (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() == "127.0.0.1" {
			return nil
		}
	}
	if u.Scheme != "https" || u.User != nil || u.Fragment != "" || !allowedGithubHost(u.Hostname()) {
		return fmt.Errorf("неподдерживаемый адрес загрузки: %s", u.Hostname())
	}
	return nil
}
func (c *transferClient) get(ctx context.Context, raw string, max int64) ([]byte, error) {
	if err := c.checkURL(raw); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "DDS-Installer/"+installerVersion+" (read-only-stager)")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, githubHTTPError(resp)
	}
	if resp.ContentLength > max {
		return nil, errors.New("ответ превышает допустимый размер")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errors.New("ответ превышает допустимый размер")
	}
	return data, nil
}
func (c *transferClient) latest(ctx context.Context) (latestRelease, releaseAsset, releaseAsset, error) {
	var r latestRelease
	data, err := c.get(ctx, c.LatestURL, 2<<20)
	if err != nil {
		return r, releaseAsset{}, releaseAsset{}, err
	}
	if err = json.Unmarshal(data, &r); err != nil {
		return r, releaseAsset{}, releaseAsset{}, err
	}
	ver := strings.TrimPrefix(r.Tag, "v")
	if r.Draft || r.Prerelease || !finalVersionRE.MatchString(ver) {
		return r, releaseAsset{}, releaseAsset{}, errors.New("актуальный релиз не является Stable")
	}
	zipName := fmt.Sprintf("DDS-%s-windows-x64.zip", ver)
	shaName := zipName + ".sha256"
	var full, side releaseAsset
	for _, a := range r.Assets {
		if a.Name == zipName {
			full = a
		}
		if a.Name == shaName {
			side = a
		}
	}
	if full.Name == "" || side.Name == "" || full.State != "uploaded" || side.State != "uploaded" || full.Size <= 0 || full.Size > maxFullZIP || side.Size <= 0 || side.Size > 4096 {
		return r, full, side, errors.New("релиз неполный или файлы некорректного размера")
	}
	expectedPrefix := companionAssetsPrefix + r.Tag + "/"
	if !c.AllowTestHTTP && (!strings.HasPrefix(full.URL, expectedPrefix) || !strings.HasPrefix(side.URL, expectedPrefix)) {
		return r, full, side, errors.New("файлы релиза имеют неожиданный источник")
	}
	if !strings.HasPrefix(full.Digest, "sha256:") || !hexDigestRE.MatchString(strings.TrimPrefix(full.Digest, "sha256:")) {
		return r, full, side, errors.New("GitHub не предоставил проверяемый SHA-256 архива")
	}
	if err := c.checkURL(full.URL); err != nil {
		return r, full, side, err
	}
	if err := c.checkURL(side.URL); err != nil {
		return r, full, side, err
	}
	return r, full, side, nil
}
func parseSHA256Sidecar(b []byte, filename string) (string, error) {
	if len(b) > 4096 {
		return "", errors.New("неверный SHA-файл")
	}
	s := strings.TrimSpace(strings.TrimPrefix(string(b), "\ufeff"))
	lines := strings.Split(s, "\n")
	if len(lines) != 1 {
		return "", errors.New("SHA-файл содержит несколько строк")
	}
	fields := strings.Fields(s)
	if len(fields) != 2 || !hexDigestRE.MatchString(strings.ToLower(fields[0])) || strings.TrimPrefix(fields[1], "*") != filename {
		return "", errors.New("некорректный SHA-256 sidecar")
	}
	return strings.ToLower(fields[0]), nil
}
func (c *transferClient) download(ctx context.Context, asset releaseAsset, dest string) (string, error) {
	if err := c.checkURL(asset.URL); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", asset.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "DDS-Installer/"+installerVersion+" (read-only-stager)")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("загрузка: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxFullZIP {
		return "", errors.New("файл слишком велик")
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	n, copyErr := copyDownload(io.MultiWriter(f, hash), resp.Body, maxFullZIP, asset.Size, c.DownloadProgress)
	closeErr := f.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n != asset.Size || n > maxFullZIP {
		return "", errors.New("размер скачанного файла не совпадает с GitHub Release")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if digest != strings.TrimPrefix(asset.Digest, "sha256:") {
		return "", errors.New("SHA-256 скачанного файла не совпадает с GitHub Release")
	}
	return digest, nil
}
func safeZipMember(name string) (string, error) {
	if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || strings.ContainsAny(name, ":\x00") {
		return "", errors.New("небезопасный путь ZIP")
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned != strings.TrimSuffix(name, "/") {
		return "", errors.New("нормализация пути ZIP запрещена")
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == "" || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, `<>"|?*`) {
			return "", errors.New("неподдерживаемый путь ZIP для Windows")
		}
		u := strings.ToUpper(strings.Split(part, ".")[0])
		switch u {
		case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
			return "", errors.New("зарезервированное имя Windows")
		}
	}
	return cleaned, nil
}

// inspectZIPForStaging computes the real extracted payload size from the ZIP
// central directory before allocating extraction space. The 1.5 GiB limit is
// strictly an anti-ZIP-bomb ceiling, never a mandatory disk reservation.
// Extraction still enforces the declared sizes and validates manifest hashes.
func inspectZIPForStaging(zipPath string) (uint64, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, err
	}
	defer zr.Close()
	if len(zr.File) > maxMembers {
		return 0, errors.New("слишком много файлов")
	}
	seen := make(map[string]bool, len(zr.File))
	var total uint64
	for _, f := range zr.File {
		clean, e := safeZipMember(f.Name)
		if e != nil {
			return 0, fmt.Errorf("ZIP %q: %w", f.Name, e)
		}
		key := strings.ToLower(clean)
		if seen[key] {
			return 0, errors.New("ZIP содержит дублирующиеся пути")
		}
		seen[key] = true
		if f.Mode()&os.ModeSymlink != 0 || (!f.FileInfo().IsDir() && !f.Mode().IsRegular()) {
			return 0, errors.New("ZIP содержит недопустимый тип файла")
		}
		// Reject bad declared sizes and oversized total before extraction.
		if f.UncompressedSize64 > uint64(maxUnpacked) || total > uint64(maxUnpacked)-f.UncompressedSize64 {
			return 0, errors.New("ZIP превышает допустимый размер распаковки")
		}
		total += f.UncompressedSize64
	}
	return total, nil
}

// Filesystem margin is intentionally small and independent of maxUnpacked.
func enoughStageSpace(available, requiredBytes uint64) bool {
	return requiredBytes <= ^uint64(0)-stageFreeMargin && available >= requiredBytes+stageFreeMargin
}

func unpackVerifiedZIP(zipPath, destination, version string) (int, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, err
	}
	defer zr.Close()
	if len(zr.File) > maxMembers {
		return 0, errors.New("слишком много файлов")
	}
	seen := map[string]bool{}
	var size int64
	count := 0
	for _, f := range zr.File {
		clean, e := safeZipMember(f.Name)
		if e != nil {
			return 0, fmt.Errorf("ZIP %q: %w", f.Name, e)
		}
		key := strings.ToLower(clean)
		if seen[key] {
			return 0, errors.New("ZIP содержит дублирующиеся пути")
		}
		seen[key] = true
		if f.Mode()&os.ModeSymlink != 0 || !f.FileInfo().IsDir() && !f.Mode().IsRegular() {
			return 0, errors.New("ZIP содержит недопустимый тип файла")
		}
		if f.UncompressedSize64 > uint64(maxUnpacked) || size > maxUnpacked-int64(f.UncompressedSize64) {
			return 0, errors.New("ZIP превышает допустимый размер распаковки")
		}
		size += int64(f.UncompressedSize64)
		dest := filepath.Join(destination, filepath.FromSlash(clean))
		rel, e := filepath.Rel(destination, dest)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return 0, errors.New("ZIP выходит за границы staging")
		}
		if f.FileInfo().IsDir() {
			if e = os.MkdirAll(dest, 0700); e != nil {
				return 0, e
			}
			continue
		}
		if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
			return 0, e
		}
		src, e := f.Open()
		if e != nil {
			return 0, e
		}
		dst, e := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			src.Close()
			return 0, e
		}
		n, copyErr := io.Copy(dst, io.LimitReader(src, int64(f.UncompressedSize64)+1))
		errClose := dst.Close()
		src.Close()
		if copyErr != nil {
			return 0, copyErr
		}
		if errClose != nil {
			return 0, errClose
		}
		if n != int64(f.UncompressedSize64) {
			return 0, errors.New("неверная распакованная длина")
		}
		count++
	}
	if err := validateCompanionPackage(destination, version); err != nil {
		return 0, err
	}
	return count, nil
}
func validateCompanionPackage(root, version string) error {
	// This is a package preflight: never launch the downloaded executables.
	for _, p := range []string{"DDS.exe", "DDSUpdater.exe", "current.json", path.Join("versions", version, "DDSApp.exe"), path.Join("versions", version, "DDS.install.json")} {
		info, e := os.Stat(filepath.Join(root, filepath.FromSlash(p)))
		if e != nil || info.IsDir() {
			return fmt.Errorf("отсутствует обязательный файл пакета: %s", p)
		}
	}
	pointerBytes, e := os.ReadFile(filepath.Join(root, "current.json"))
	if e != nil {
		return e
	}
	var pointer currentPointer
	if e = json.Unmarshal(pointerBytes, &pointer); e != nil {
		return e
	}
	if pointer.Version != version || pointer.RelativePath != "versions/"+version || pointer.Schema != "dds-version-pointer-v1" {
		return errors.New("неверный указатель версии в пакете")
	}
	manifestBytes, e := os.ReadFile(filepath.Join(root, "versions", version, "DDS.install.json"))
	if e != nil {
		return e
	}
	var man struct {
		Schema  string `json:"schema"`
		Version string `json:"version"`
		Files   []struct {
			Path   string `json:"path"`
			Size   int64  `json:"size_bytes"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	if e = json.Unmarshal(manifestBytes, &man); e != nil {
		return e
	}
	if man.Schema != "dds-install-manifest-v1" || man.Version != version || len(man.Files) == 0 || len(man.Files) > maxMembers {
		return errors.New("неверный внутренний манифест установки")
	}
	seen := map[string]bool{}
	for _, f := range man.Files {
		clean, e := safeZipMember(f.Path)
		if e != nil || clean != f.Path || f.Size < 0 || !hexDigestRE.MatchString(f.SHA256) {
			return errors.New("некорректная запись файлового манифеста")
		}
		key := strings.ToLower(clean)
		if seen[key] {
			return errors.New("дубликат в манифесте")
		}
		seen[key] = true
		actual := filepath.Join(root, "versions", version, filepath.FromSlash(clean))
		st, e := os.Stat(actual)
		if e != nil || st.IsDir() || st.Size() != f.Size {
			return fmt.Errorf("размер файла не совпадает с манифестом: %s", clean)
		}
		stream, e := os.Open(actual)
		if e != nil {
			return e
		}
		h := sha256.New()
		_, e = io.Copy(h, stream)
		stream.Close()
		if e != nil {
			return e
		}
		if hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
			return fmt.Errorf("хэш внутреннего файла неверен: %s", clean)
		}
	}
	if !seen["ddsapp.exe"] {
		return errors.New("манифест не содержит DDSApp.exe")
	}
	// Reject unlisted payload files: the package must be exactly the accepted
	// install manifest, apart from the manifest itself and stable-root bootstrap.
	versionDir := filepath.Join(root, "versions", version)
	walkErr := filepath.WalkDir(versionDir, func(p string, de os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if de.IsDir() {
			return nil
		}
		rel, er := filepath.Rel(versionDir, p)
		if er != nil {
			return er
		}
		rel = filepath.ToSlash(rel)
		if rel == "DDS.install.json" {
			return nil
		}
		if !seen[strings.ToLower(rel)] {
			return fmt.Errorf("пакет содержит файл вне внутреннего манифеста: %s", rel)
		}
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		switch entry.Name() {
		case "DDS.exe", "DDSUpdater.exe", "current.json":
		case "versions":
			versions, er := os.ReadDir(filepath.Join(root, "versions"))
			if er != nil {
				return er
			}
			if len(versions) != 1 || versions[0].Name() != version || !versions[0].IsDir() {
				return errors.New("пакет содержит постороннюю версию")
			}
		default:
			return fmt.Errorf("посторонний файл в корне пакета: %s", entry.Name())
		}
	}
	return nil
}
func writeStageReceipt(dir string, r stageReceipt) error {
	data, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return e
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(dir, "stage_receipt.json"), data, 0600)
}
func (c *transferClient) stageCompanion(ctx context.Context, root string) (stageReceipt, string, error) {
	var rec stageReceipt
	if prev, path, ok := validCompanionCache(root, c.AllowTestHTTP); ok {
		c.UsedCachedStage = true
		c.progress("Проверенный локальный staging Companion использован без GitHub API; актуальность Stable онлайн не проверялась.")
		return prev, path, nil
	}
	c.progress("Запрашиваю метаданные Stable Release Companion...")
	release, full, side, e := c.latest(ctx)
	if e != nil {
		return rec, "", e
	}
	version := strings.TrimPrefix(release.Tag, "v")
	expectedSHA := strings.TrimPrefix(full.Digest, "sha256:")
	base := filepath.Join(root, "companion")
	target := filepath.Join(base, "v"+version)
	existing, e := os.ReadFile(filepath.Join(target, "stage_receipt.json"))
	if e == nil {
		var prev stageReceipt
		if json.Unmarshal(existing, &prev) == nil && prev.Schema == "dds-installer-stage-v1" && prev.SHA256 == expectedSHA && prev.Version == version && prev.Status == "VERIFIED_AND_STAGED" {
			c.progress("Проверяю существующий staging Companion для повторного использования...")
			if validCompanionStageContents(target, prev) == nil {
				c.progress("Проверенный Companion уже подготовлен — повторная загрузка не нужна.")
				return prev, target, nil
			}
		}
	} else if !os.IsNotExist(e) {
		return rec, "", e
	}
	oldExists := false
	if st, statErr := os.Lstat(target); statErr == nil {
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return rec, "", errors.New("небезопасная существующая staging-папка")
		}
		oldExists = true
	} else if !os.IsNotExist(statErr) {
		return rec, "", statErr
	}
	if c.FreeBytes == nil {
		c.FreeBytes = freeDiskSpace
	}
	available, spaceErr := c.FreeBytes(filepath.Dir(root))
	if spaceErr != nil {
		return rec, "", fmt.Errorf("не удалось проверить свободное место: %w", spaceErr)
	}
	// Only the known ZIP size is needed before downloading it. The actual
	// extracted size is inspected from the verified ZIP after download.
	if !enoughStageSpace(available, uint64(full.Size)) {
		return rec, "", fmt.Errorf("недостаточно места для загрузки: требуется около %d МиБ (ZIP + запас)", (uint64(full.Size)+stageFreeMargin+(1<<20)-1)>>20)
	}
	if e = os.MkdirAll(base, 0700); e != nil {
		return rec, "", e
	}
	tmp, e := os.MkdirTemp(base, ".prepare-")
	if e != nil {
		return rec, "", e
	}
	defer os.RemoveAll(tmp)
	c.progress("Получен Stable Release " + release.Tag + "; проверяю контрольные суммы...")
	sideBytes, e := c.get(ctx, side.URL, 4096)
	if e != nil {
		return rec, "", e
	}
	if int64(len(sideBytes)) != side.Size {
		return rec, "", errors.New("размер SHA-256 sidecar не совпадает с GitHub Release")
	}
	if strings.HasPrefix(side.Digest, "sha256:") && h(sideBytes) != strings.TrimPrefix(side.Digest, "sha256:") {
		return rec, "", errors.New("SHA-256 самого sidecar не совпадает с GitHub Release")
	}
	sideSHA, e := parseSHA256Sidecar(sideBytes, full.Name)
	if e != nil {
		return rec, "", e
	}
	if sideSHA != expectedSHA {
		return rec, "", errors.New("SHA-256 sidecar не совпадает с GitHub Release")
	}
	c.progress("Проверка суммы прошла. Скачиваю пакет (без установки)...")
	zipFile := filepath.Join(tmp, full.Name)
	calculated, e := c.download(ctx, full, zipFile)
	if e != nil {
		return rec, "", e
	}
	if calculated != sideSHA {
		return rec, "", errors.New("SHA-256 не совпадает с контрольной суммой")
	}
	c.progress("Архив скачан, SHA-256 совпадает. Проверяю размер распаковки и подготавливаю staging...")
	unpackedBytes, e := inspectZIPForStaging(zipFile)
	if e != nil {
		return rec, "", e
	}
	// FreeBytes is re-queried *after* downloading: the downloaded ZIP already
	// occupies space on the same volume. Do not count it twice.
	available, spaceErr = c.FreeBytes(tmp)
	if spaceErr != nil {
		return rec, "", fmt.Errorf("не удалось проверить место для распаковки: %w", spaceErr)
	}
	if !enoughStageSpace(available, unpackedBytes) {
		return rec, "", fmt.Errorf("недостаточно места для распаковки: требуется около %d МиБ (файлы + запас)", (unpackedBytes+stageFreeMargin+(1<<20)-1)>>20)
	}
	payload := filepath.Join(tmp, "payload")
	if e = os.Mkdir(payload, 0700); e != nil {
		return rec, "", e
	}
	c.progress("Распаковка и проверка  внутреннего манифеста Companion...")
	count, e := unpackVerifiedZIP(zipFile, payload, version)
	if e != nil {
		return rec, "", e
	}
	// Remove only the temporary copy; verified payload+receipt is the stage contract.
	if e = os.Remove(zipFile); e != nil {
		return rec, "", e
	}
	bootstrap, hashErr := collectBootstrapSHA256(payload, version)
	if hashErr != nil {
		return stageReceipt{}, "", hashErr
	}
	rec = stageReceipt{Schema: "dds-installer-stage-v1", Component: "DDS Companion", Version: version, Source: full.URL, SHA256: calculated, ZIPSize: full.Size, Files: count, CreatedUTC: time.Now().UTC().Format(time.RFC3339), BootstrapSHA256: bootstrap, Status: "VERIFIED_AND_STAGED"}
	if e = writeStageReceipt(tmp, rec); e != nil {
		return stageReceipt{}, "", e
	}
	if oldExists {
		// Only this installer-owned staging version may be replaced. The old
		// stage is kept until a complete, independently verified new stage exists.
		backup, backupErr := os.MkdirTemp(base, ".superseded-")
		if backupErr != nil {
			return stageReceipt{}, "", backupErr
		}
		if backupErr = os.Remove(backup); backupErr != nil {
			return stageReceipt{}, "", backupErr
		}
		if backupErr = os.Rename(target, backup); backupErr != nil {
			return stageReceipt{}, "", backupErr
		}
		if e = os.Rename(tmp, target); e != nil {
			rollbackErr := os.Rename(backup, target)
			if rollbackErr != nil {
				return stageReceipt{}, "", fmt.Errorf("staging promotion failed: %v; staging rollback failed: %v", e, rollbackErr)
			}
			return stageReceipt{}, "", e
		}
		_ = os.RemoveAll(backup)
	} else if e = os.Rename(tmp, target); e != nil {
		return stageReceipt{}, "", e
	}
	c.progress("Готово: весь staging проверен. Рабочие файлы и данные не изменены.")
	return rec, target, nil
}

// Kept non-interactive for tests and future installer orchestration.
func performCompanionStage(root string) (stageReceipt, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	client := newTransferClient()
	client.Progress = func(s string) { fmt.Println(s) }
	return client.stageCompanion(ctx, root)
}

// An explicit action is required; merely opening Setup must never fetch or mutate.
func promptForStage(input io.Reader, output io.Writer) bool {
	fmt.Fprintln(output, "\nДоступная безопасная операция:")
	fmt.Fprintln(output, "  1 — Скачать, проверить и подготовить DDS Companion Stable (НЕ устанавливать)")
	fmt.Fprintln(output, "  Enter — Выход без изменений")
	fmt.Fprint(output, "Выбор: ")
	line, _ := bufio.NewReader(input).ReadString('\n')
	return strings.TrimSpace(line) == "1"
}

func h(b []byte) string { digest := sha256.Sum256(b); return hex.EncodeToString(digest[:]) }

type stageOutcome struct {
	Component string
	Version   string
	Result    string
	Location  string
	Reason    string
	SHA256    string
}

// Staging is independent from installation dependency resolution: a missing
// Discord installation does not prevent verifying the Plugin payload. No
// stage may mutate the live component's installation or user archive.
func planStage(inv Inventory, all bool) []string {
	result := []string{}
	for _, x := range []struct {
		key   string
		state State
	}{
		{"discord", inv.Discord.State}, {"betterdiscord", inv.BetterDiscord.State}, {"plugin", inv.Plugin.State}, {"companion", inv.Companion.State},
	} {
		if all || x.state != StateOK {
			result = append(result, x.key)
		}
	}
	return result
}

func runCascade(ctx context.Context, c *transferClient, stageRoot string, desired []string, sign func(string) error) []stageOutcome {
	outcomes := make([]stageOutcome, 0, len(desired))
	for _, key := range desired {
		o := stageOutcome{Component: key, Result: "FAILED"}
		c.UsedCachedStage = false
		fmt.Println("\n→ Этаж:", labelComponent(key), "— Acquire → Verify → Stage")
		c.DownloadProgress = newConsoleDownloadDisplay(os.Stdout).update
		var err error
		switch key {
		case "discord":
			var a preparedExternal
			a, o.Location, err = c.stageDiscord(ctx, stageRoot, sign)
			o.Version = a.Version
			o.SHA256 = a.SHA256
		case "betterdiscord", "plugin":
			var a preparedExternal
			a, o.Location, err = c.stageGitHubExternal(ctx, stageRoot, key)
			o.Version = a.Version
			o.SHA256 = a.SHA256
		case "companion":
			var a stageReceipt
			a, o.Location, err = c.stageCompanion(ctx, stageRoot)
			o.Version = a.Version
			o.SHA256 = a.SHA256
		default:
			err = fmt.Errorf("неизвестный этаж %q", key)
		}
		if err != nil {
			o.Reason = err.Error()
			fmt.Println("НЕ УДАЛОСЬ:", o.Reason)
			fmt.Println("Рабочие компоненты не затронуты; остальные этажи проверяются независимо.")
		} else {
			o.Result = "VERIFIED_AND_STAGED"
			if c.UsedCachedStage {
				o.Result = "VERIFIED_CACHED"
			}
			fmt.Printf("ГОТОВО: %s %s [%s] | SHA-256 %s\nStaging: %s\n", labelComponent(key), o.Version, o.Result, o.SHA256, o.Location)
		}
		outcomes = append(outcomes, o)
	}
	return outcomes
}

func labelComponent(k string) string {
	switch k {
	case "discord":
		return "Discord"
	case "betterdiscord":
		return "BetterDiscord CLI"
	case "plugin":
		return "DDS Plugin"
	case "companion":
		return "DDS Companion"
	}
	return k
}

func printCascadeSummary(outcomes []stageOutcome) {
	fmt.Println("\n", strings.Repeat("=", 72), "\nРЕЗУЛЬТАТ ПОДГОТОВКИ (УСТАНОВКА НЕ ВЫПОЛНЯЛАСЬ):")
	ok := 0
	for _, o := range outcomes {
		if o.Result == "VERIFIED_AND_STAGED" || o.Result == "VERIFIED_CACHED" {
			ok++
			fmt.Printf("  %s: %s %s\n", labelComponent(o.Component), o.Result, o.Version)
		} else {
			fmt.Printf("  %s: FAILED — %s\n", labelComponent(o.Component), o.Reason)
		}
	}
	fmt.Printf("Итого: %d/%d подготовлено. Рабочие файлы и DataRoot не изменены.\n", ok, len(outcomes))
	for _, o := range outcomes {
		if o.Result == "VERIFIED_CACHED" {
			fmt.Println("Примечание: VERIFIED_CACHED означает локальную проверку сохранённого пакета, а НЕ проверку последнего Stable на GitHub.")
			break
		}
	}
}

func selectStage(input io.Reader, out io.Writer) string {
	fmt.Fprintln(out, "\nДоступные операции (УСТАНОВКА ТОЛЬКО С ОТДЕЛЬНЫМ ПОДТВЕРЖДЕНИЕМ):")
	fmt.Fprintln(out, "  1 — ТОЛЬКО скачать и проверить 4 пакета (БЕЗ УСТАНОВКИ)")
	fmt.Fprintln(out, "  2 — Подготовить только отсутствующие/неисправные компоненты по плану")
	fmt.Fprintln(out, "  3 — DDS Plugin: установить проверенный staging (отдельное подтверждение)")
	fmt.Fprintln(out, "  4 — DDS Companion: проверить готовность к установке (ТОЛЬКО ПРОСМОТР)\n  5 — DDS Companion: установить проверенную новую версию (строгое подтверждение)")
	fmt.Fprintln(out, "  6 — DDS Companion: восстановить прерванную установку (строгое подтверждение)")
	fmt.Fprintln(out, "  7 — DDS Companion: первичная установка в новый каталог (строгое подтверждение)")
	fmt.Fprintln(out, "  8 — УСТАНОВИТЬ DDS: автоматически пройти все необходимые этапы")
	fmt.Fprintln(out, "  Enter — Выход без изменений")
	fmt.Fprint(out, "Выбор: ")
	text, _ := sharedBufferedReader(input).ReadString('\n')
	return strings.TrimSpace(text)
}

func runStageMenu(inv Inventory) {
	reader := bufio.NewReader(os.Stdin)
	mode := selectStage(reader, os.Stdout)
	if mode == "8" {
		exe, _ := os.Executable()
		runFirstSetupMenu(inv, reader, os.Stdout, os.Getenv("LOCALAPPDATA"), os.Getenv("APPDATA"), filepath.Dir(exe))
		return
	}
	if mode == "3" {
		runPluginApplyMenu(inv, reader, os.Stdout, filepath.Join(os.TempDir(), "DDS_Installer_Stage"), os.Getenv("APPDATA"))
		return
	}
	if mode == "4" {
		runCompanionPreview(inv, os.Stdout, filepath.Join(os.TempDir(), "DDS_Installer_Stage"))
		return
	}
	if mode == "5" {
		runCompanionApplyMenu(inv, reader, os.Stdout, filepath.Join(os.TempDir(), "DDS_Installer_Stage"), checkNoCompanionProcesses)
		return
	}
	if mode == "6" {
		runCompanionRecoveryMenu(inv, reader, os.Stdout, checkNoCompanionProcesses)
		return
	}
	if mode == "7" {
		runCompanionFreshMenu(inv, reader, os.Stdout, filepath.Join(os.TempDir(), "DDS_Installer_Stage"), os.Getenv("LOCALAPPDATA"), checkNoCompanionProcesses)
		return
	}
	if mode != "1" && mode != "2" {
		return
	}
	requested := planStage(inv, mode == "1")
	if len(requested) == 0 {
		fmt.Println("Все компоненты обнаружены: изменений и загрузок по плану не требуется.")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	c := newTransferClient()
	c.Progress = func(s string) { fmt.Println(s) }
	folder := filepath.Join(os.TempDir(), "DDS_Installer_Stage")
	fmt.Println("Источник: только официальные Stable; временный staging:", folder)
	fmt.Println("ЗАПРЕЩЕНО: запуск установщиков, замена компонентов, запись в DataRoot/SQLite/реестр.")
	outputs := runCascade(ctx, c, folder, requested, verifyDiscordSignature)
	printCascadeSummary(outputs)
}

// Cached stage reuse never establishes what GitHub currently considers the
// latest Stable release. It only validates an existing, previously verified
// artifact in the installer's own temporary stage. No installed files are read
// or modified by this lookup.

func validExternalCache(root, component string, allowTestURLs bool) (preparedExternal, string, bool) {
	empty := preparedExternal{}
	label, prefix, payload := "DDS Plugin", pluginPrefix, "DDS.plugin.js"
	if component == "betterdiscord" {
		label, prefix, payload = "BetterDiscord CLI", bdcliPrefix, "bdcli.exe"
	} else if component != "plugin" {
		return empty, "", false
	}
	parent := filepath.Join(root, component)
	if requireNormalDir(parent) != nil {
		return empty, "", false
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return empty, "", false
	}
	var tags []string
	for _, ent := range entries {
		tag := ent.Name()
		if !ent.IsDir() || !finalVersionRE.MatchString(strings.TrimPrefix(tag, "v")) {
			continue
		}
		tags = append(tags, tag)
	}
	sort.Slice(tags, func(i, j int) bool {
		return compareStableVersions(strings.TrimPrefix(tags[i], "v"), strings.TrimPrefix(tags[j], "v")) > 0
	})
	for _, tag := range tags {
		version := strings.TrimPrefix(tag, "v")
		target := filepath.Join(parent, tag)
		if requireNormalDir(target) != nil || requireNormalFile(filepath.Join(target, "stage_receipt.json")) != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(target, "stage_receipt.json"))
		if err != nil {
			continue
		}
		var r preparedExternal
		if json.Unmarshal(data, &r) != nil || r.Schema != "dds-installer-external-stage-v1" || r.Status != "VERIFIED_AND_STAGED" || r.Component != label || r.Version != version || !hexDigestRE.MatchString(r.SHA256) || !hexDigestRE.MatchString(r.PayloadSHA256) || r.ZIPSize <= 0 || r.ZIPSize > externalMaxSize {
			continue
		}
		packageName := "DDS.plugin.js"
		if component == "betterdiscord" {
			packageName = "bdcli_" + version + "_windows_amd64.zip"
		}
		if !allowTestURLs && r.Source != prefix+tag+"/"+packageName {
			continue
		}
		pkg := filepath.Join(target, "package", packageName)
		extracted := filepath.Join(target, "payload", payload)
		if requireNormalDir(filepath.Join(target, "package")) != nil || requireNormalDir(filepath.Join(target, "payload")) != nil || requireNormalFile(pkg) != nil || requireNormalFile(extracted) != nil {
			continue
		}
		actual, size, err := shaFile(pkg)
		if err != nil || actual != r.SHA256 || size != r.ZIPSize {
			continue
		}
		if _, ok := readExternalReceipt(target, label, version, r.SHA256, packageName); !ok {
			continue
		}
		if component == "plugin" {
			if r.PayloadSHA256 != r.SHA256 {
				continue
			}
			b, er := os.ReadFile(extracted)
			if er != nil {
				continue
			}
			match := pluginHeaderVersionRE.FindSubmatch(b)
			if len(match) != 2 || string(match[1]) != version {
				continue
			}
		} else if verifyWindowsPE(extracted) != nil {
			continue
		}
		return r, target, true
	}
	return empty, "", false
}

func validCompanionCache(root string, allowTestURLs bool) (stageReceipt, string, bool) {
	empty := stageReceipt{}
	parent := filepath.Join(root, "companion")
	if requireNormalDir(parent) != nil {
		return empty, "", false
	}
	dirs, err := os.ReadDir(parent)
	if err != nil {
		return empty, "", false
	}
	var tags []string
	for _, d := range dirs {
		if d.IsDir() && strings.HasPrefix(d.Name(), "v") && finalVersionRE.MatchString(strings.TrimPrefix(d.Name(), "v")) {
			tags = append(tags, d.Name())
		}
	}
	sort.Slice(tags, func(i, j int) bool {
		return compareStableVersions(strings.TrimPrefix(tags[i], "v"), strings.TrimPrefix(tags[j], "v")) > 0
	})
	for _, tag := range tags {
		version := strings.TrimPrefix(tag, "v")
		target := filepath.Join(parent, tag)
		if requireNormalDir(target) != nil || requireNormalFile(filepath.Join(target, "stage_receipt.json")) != nil || requireNormalDir(filepath.Join(target, "payload")) != nil {
			continue
		}
		content, err := os.ReadFile(filepath.Join(target, "stage_receipt.json"))
		if err != nil {
			continue
		}
		var r stageReceipt
		if json.Unmarshal(content, &r) != nil || r.Schema != "dds-installer-stage-v1" || r.Status != "VERIFIED_AND_STAGED" || r.Component != "DDS Companion" || r.Version != version || !hexDigestRE.MatchString(r.SHA256) || r.ZIPSize <= 0 || r.ZIPSize > maxFullZIP || r.Files <= 0 || r.Files > maxMembers {
			continue
		}
		if !allowTestURLs && r.Source != companionAssetsPrefix+tag+"/DDS-"+version+"-windows-x64.zip" {
			continue
		}
		if validCompanionStageContents(target, r) != nil {
			continue
		}
		return r, target, true
	}
	return empty, "", false
}

// Bootstrap files are outside the versioned DDS.install.json manifest. The
// hashes are captured only after authenticating the downloaded Release ZIP.
// A legacy receipt with no hashes is deliberately not a validated cache.
func bootstrapPaths(version string) []string {
	return []string{"DDS.exe", "DDSUpdater.exe", "current.json", filepath.ToSlash(filepath.Join("versions", version, "DDS.install.json"))}
}

func collectBootstrapSHA256(payload, version string) (map[string]string, error) {
	out := make(map[string]string, 4)
	for _, name := range bootstrapPaths(version) {
		p := filepath.Join(payload, filepath.FromSlash(name))
		if err := requireNormalFile(p); err != nil {
			return nil, fmt.Errorf("bootstrap %s: %w", name, err)
		}
		hash, _, err := shaFile(p)
		if err != nil {
			return nil, err
		}
		out[name] = hash
	}
	return out, nil
}

func validCompanionStageContents(target string, r stageReceipt) error {
	if len(r.BootstrapSHA256) != 4 {
		return errors.New("legacy staging не содержит SHA-256 корневых файлов; требуется повторная проверенная подготовка")
	}
	payload := filepath.Join(target, "payload")
	if requireNormalDir(payload) != nil {
		return errors.New("payload отсутствует")
	}
	for _, name := range bootstrapPaths(r.Version) {
		expected := r.BootstrapSHA256[name]
		if !hexDigestRE.MatchString(expected) {
			return fmt.Errorf("нет проверенного SHA-256 для %s", name)
		}
		p := filepath.Join(payload, filepath.FromSlash(name))
		if err := requireNormalFile(p); err != nil {
			return fmt.Errorf("unsafe bootstrap %s: %w", name, err)
		}
		actual, _, err := shaFile(p)
		if err != nil || actual != expected {
			return fmt.Errorf("bootstrap SHA-256 mismatch: %s", name)
		}
	}
	// Internal files are still fully checked against the versioned manifest.
	return validateCompanionPackage(payload, r.Version)
}

// downloadProgress describes ONLY the number of bytes read from the response.
// A known total comes from the verified release manifest or the HTTP response;
// an unknown total (-1) must never be represented as a guessed percentage.
type downloadProgress struct {
	Bytes    int64
	Total    int64
	Finished bool
}

// copyDownload counts successfully written network bytes without changing
// size caps, hash verification, or the existing staged-file transaction.
func copyDownload(dst io.Writer, src io.Reader, maxBytes, total int64, report func(downloadProgress)) (int64, error) {
	writer := &countingDownloadWriter{target: dst, total: total, report: report}
	if report != nil {
		report(downloadProgress{Total: total})
	}
	n, err := io.Copy(writer, io.LimitReader(src, maxBytes+1))
	if report != nil {
		report(downloadProgress{Bytes: n, Total: total, Finished: true})
	}
	return n, err
}

type countingDownloadWriter struct {
	target io.Writer
	report func(downloadProgress)
	total  int64
	n      int64
	last   time.Time
}

func (w *countingDownloadWriter) Write(p []byte) (int, error) {
	n, err := w.target.Write(p)
	w.n += int64(n)
	if w.report != nil && (time.Since(w.last) >= 150*time.Millisecond || w.total > 0 && w.n >= w.total) {
		w.report(downloadProgress{Bytes: w.n, Total: w.total})
		w.last = time.Now()
	}
	return n, err
}

// The display is single-download at a time, but keeps animating if a network
// read stalls. This is an activity signal, NOT a made-up transfer percentage.
type consoleDownloadDisplay struct {
	out        io.Writer
	mu         sync.Mutex
	active     bool
	done       int64
	total      int64
	started    time.Time
	frame      int
	stop       chan struct{}
	stopped    chan struct{}
	lastLength int
}

func newConsoleDownloadDisplay(out io.Writer) *consoleDownloadDisplay {
	return &consoleDownloadDisplay{out: out}
}

func (d *consoleDownloadDisplay) update(p downloadProgress) {
	d.mu.Lock()
	if !d.active {
		d.active = true
		d.started = time.Now()
		d.stop = make(chan struct{})
		d.stopped = make(chan struct{})
		stop, stopped := d.stop, d.stopped
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			defer close(stopped)
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					d.mu.Lock()
					if d.active {
						d.frame++
						d.renderLocked(false)
					}
					d.mu.Unlock()
				}
			}
		}()
	}
	d.done, d.total = p.Bytes, p.Total
	if p.Finished {
		d.renderLocked(true)
		d.active = false
		close(d.stop)
		stopped := d.stopped
		d.mu.Unlock()
		<-stopped
		return
	}
	d.renderLocked(false)
	d.mu.Unlock()
}

func (d *consoleDownloadDisplay) renderLocked(finished bool) {
	line := formatDownloadLine(d.done, d.total, d.frame, time.Since(d.started), finished)
	pad := d.lastLength - len([]rune(line))
	if pad < 0 {
		pad = 0
	}
	fmt.Fprint(d.out, "\r", line, strings.Repeat(" ", pad))
	d.lastLength = len([]rune(line))
	if finished {
		fmt.Fprintln(d.out)
	}
}

func formatDownloadLine(done, total int64, frame int, elapsed time.Duration, finished bool) string {
	const width = 18
	if done < 0 {
		done = 0
	}
	seconds := int(elapsed.Seconds())
	if total > 0 {
		// Cap the visual bar at 100, never alter the reported byte count.
		pct := int(done * 100 / total)
		if pct > 100 {
			pct = 100
		}
		count := pct * width / 100
		return fmt.Sprintf("  [%-18s] %3d%% | %.1f/%.1f МиБ | %d с", strings.Repeat("#", count), pct, float64(done)/(1<<20), float64(total)/(1<<20), seconds)
	}
	if finished {
		return fmt.Sprintf("  [%s] Получено %.1f МиБ | %d с (размер неизвестен)", strings.Repeat("#", width), float64(done)/(1<<20), seconds)
	}
	spin := []byte{'|', '/', '-', '\\'}
	return fmt.Sprintf("  [%c] Получено %.1f МиБ | %d с (размер неизвестен)", spin[frame%len(spin)], float64(done)/(1<<20), seconds)
}

// DDS Installer 0.2.0-r2: independent source acquisition floors. These
// routines never invoke downloaded programs or write into installed locations.
const (
	pluginLatestURL = "https://api.github.com/repos/Mr-Dexter-Morgan/DDS_BD_Plugin/releases/latest"
	pluginPrefix    = "https://github.com/Mr-Dexter-Morgan/DDS_BD_Plugin/releases/download/"
	bdcliLatestURL  = "https://api.github.com/repos/BetterDiscord/cli/releases/latest"
	bdcliPrefix     = "https://github.com/BetterDiscord/cli/releases/download/"
	// This is the x64 Stable URL linked by Discord's official download page.
	discordInstallerURL = "https://discord.com/api/downloads/distributions/app/installers/latest?arch=x64&channel=stable&platform=win"
	externalMaxSize     = int64(250 << 20)
)

var pluginHeaderVersionRE = regexp.MustCompile(`(?m)^\s*\*\s*@version\s+([^\s]+)`)

type preparedExternal struct {
	Schema        string `json:"schema"`
	Component     string `json:"component"`
	Version       string `json:"version"`
	Source        string `json:"source"`
	SHA256        string `json:"sha256"`
	ZIPSize       int64  `json:"zip_size_bytes"`
	PayloadSHA256 string `json:"payload_sha256"`
	CreatedUTC    string `json:"created_utc"`
	Status        string `json:"status"`
}

func checksumEntry(b []byte, filename string, multiple bool) (string, error) {
	if len(b) > 8192 {
		return "", errors.New("слишком большой файл контрольных сумм")
	}
	found := ""
	for _, line := range strings.Split(strings.TrimSpace(strings.TrimPrefix(string(b), "\ufeff")), "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 || !hexDigestRE.MatchString(strings.ToLower(f[0])) {
			return "", errors.New("неверный формат файла контрольных сумм")
		}
		if strings.TrimPrefix(f[1], "*") == filename {
			if found != "" {
				return "", errors.New("дублирующиеся SHA-256")
			}
			found = strings.ToLower(f[0])
		}
		if !multiple && strings.TrimPrefix(f[1], "*") != filename {
			return "", errors.New("неверное имя в SHA-256 sidecar")
		}
	}
	if found == "" {
		return "", errors.New("пакет отсутствует в списке SHA-256")
	}
	return found, nil
}

func shaFile(file string) (string, int64, error) {
	f, e := os.Open(file)
	if e != nil {
		return "", 0, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, e
}

func requireSpace(c *transferClient, at string, n uint64) error {
	free := c.FreeBytes
	if free == nil {
		free = freeDiskSpace
	}
	x, e := free(at)
	if e != nil {
		return fmt.Errorf("проверка свободного места: %w", e)
	}
	if !enoughStageSpace(x, n) {
		return fmt.Errorf("недостаточно места: требуется %d МиБ с запасом", (n+stageFreeMargin+(1<<20)-1)>>20)
	}
	return nil
}

func commitStage(temp, target string) error {
	if st, e := os.Lstat(target); e == nil {
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return errors.New("существующая staging-папка небезопасна")
		}
		parent := filepath.Dir(target)
		old, e := os.MkdirTemp(parent, ".previous-")
		if e != nil {
			return e
		}
		if e = os.Remove(old); e != nil {
			return e
		}
		if e = os.Rename(target, old); e != nil {
			return e
		}
		if e = os.Rename(temp, target); e != nil {
			if rollback := os.Rename(old, target); rollback != nil {
				return fmt.Errorf("замена staging: %v; rollback: %v", e, rollback)
			}
			return e
		}
		_ = os.RemoveAll(old)
		return nil
	} else if !os.IsNotExist(e) {
		return e
	}
	return os.Rename(temp, target)
}

func readExternalReceipt(target, component, version, sha, relativePayload string) (preparedExternal, bool) {
	var r preparedExternal
	b, e := os.ReadFile(filepath.Join(target, "stage_receipt.json"))
	if e != nil {
		return r, false
	}
	if json.Unmarshal(b, &r) != nil || r.Schema != "dds-installer-external-stage-v1" || r.Status != "VERIFIED_AND_STAGED" || r.Component != component || r.Version != version || r.SHA256 != sha {
		return r, false
	}
	actual, _, e := shaFile(filepath.Join(target, "package", filepath.Base(relativePayload)))
	if e != nil || actual != sha {
		return r, false
	}
	if r.PayloadSHA256 != "" {
		payloadName := "DDS.plugin.js"
		if component == "BetterDiscord CLI" {
			payloadName = "bdcli.exe"
		}
		payloadPath := filepath.Join(target, "payload", payloadName)
		got, _, er := shaFile(payloadPath)
		if er != nil || got != r.PayloadSHA256 {
			return r, false
		}
	}
	if component == "DDS Plugin" && r.PayloadSHA256 != sha {
		return r, false
	}
	if component == "BetterDiscord CLI" {
		archive := filepath.Join(target, "package", filepath.Base(relativePayload))
		archiveSHA, err := inspectBinariesInZIP(archive)
		if err != nil || archiveSHA != r.PayloadSHA256 {
			return r, false
		}
	}
	return r, true
}

// Validate the reused CLI payload against the original GitHub-hashed archive;
// a locally altered receipt must never suffice to approve changed executable bytes.
func inspectBinariesInZIP(file string) (string, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	if len(zr.File) > 100 {
		return "", errors.New("слишком много файлов CLI")
	}
	seen := map[string]bool{}
	found := false
	sum := ""
	var total uint64
	for _, f := range zr.File {
		name, e := safeZipMember(f.Name)
		if e != nil {
			return "", e
		}
		key := strings.ToLower(name)
		if seen[key] {
			return "", errors.New("дубликат файла CLI")
		}
		seen[key] = true
		if f.Mode()&os.ModeSymlink != 0 || (!f.FileInfo().IsDir() && !f.Mode().IsRegular()) {
			return "", errors.New("опасный тип ZIP")
		}
		if f.UncompressedSize64 > uint64(50<<20) || total > uint64(50<<20)-f.UncompressedSize64 {
			return "", errors.New("слишком большой CLI ZIP")
		}
		total += f.UncompressedSize64
		if !strings.EqualFold(filepath.Base(name), "bdcli.exe") {
			continue
		}
		if found || f.FileInfo().IsDir() {
			return "", errors.New("неоднозначный bdcli.exe")
		}
		found = true
		rc, e := f.Open()
		if e != nil {
			return "", e
		}
		hasher := sha256.New()
		n, e := io.Copy(hasher, io.LimitReader(rc, int64(f.UncompressedSize64)+1))
		rc.Close()
		if e != nil || n != int64(f.UncompressedSize64) {
			return "", errors.New("повреждённый bdcli.exe")
		}
		sum = hex.EncodeToString(hasher.Sum(nil))
	}
	if !found {
		return "", errors.New("в CLI ZIP отсутствует bdcli.exe")
	}
	return sum, nil
}

func verifyWindowsPE(filename string) error {
	f, e := os.Open(filename)
	if e != nil {
		return e
	}
	defer f.Close()
	b := make([]byte, 2)
	if _, e = io.ReadFull(f, b); e != nil {
		return e
	}
	if string(b) != "MZ" {
		return errors.New("скачанный CLI не является Windows EXE")
	}
	return nil
}

func extractBDCLI(zipPath, out string) (string, error) {
	zr, e := zip.OpenReader(zipPath)
	if e != nil {
		return "", e
	}
	defer zr.Close()
	if len(zr.File) > 100 {
		return "", errors.New("подозрительный архив CLI: много файлов")
	}
	seen := map[string]bool{}
	var total uint64
	var execItem *zip.File
	for _, z := range zr.File {
		n, er := safeZipMember(z.Name)
		if er != nil {
			return "", er
		}
		key := strings.ToLower(n)
		if seen[key] {
			return "", errors.New("дублирующийся путь ZIP")
		}
		seen[key] = true
		if z.Mode()&os.ModeSymlink != 0 || (!z.FileInfo().IsDir() && !z.Mode().IsRegular()) {
			return "", errors.New("запрещённый тип ZIP")
		}
		if total > uint64(50<<20)-z.UncompressedSize64 || z.UncompressedSize64 > uint64(50<<20) {
			return "", errors.New("CLI ZIP превышает лимит распаковки")
		}
		total += z.UncompressedSize64
		if strings.EqualFold(filepath.Base(n), "bdcli.exe") {
			if execItem != nil {
				return "", errors.New("несколько bdcli.exe")
			}
			execItem = z
		}
	}
	if execItem == nil || execItem.FileInfo().IsDir() {
		return "", errors.New("в официальном CLI ZIP отсутствует bdcli.exe")
	}
	f, e := execItem.Open()
	if e != nil {
		return "", e
	}
	defer f.Close()
	outFile := filepath.Join(out, "bdcli.exe")
	dest, e := os.OpenFile(outFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", e
	}
	n, e := io.Copy(dest, io.LimitReader(f, int64(execItem.UncompressedSize64)+1))
	e2 := dest.Close()
	if e != nil {
		return "", e
	}
	if e2 != nil {
		return "", e2
	}
	if n != int64(execItem.UncompressedSize64) {
		return "", errors.New("размер распакованного CLI неверен")
	}
	if e = verifyWindowsPE(outFile); e != nil {
		return "", e
	}
	s, _, e := shaFile(outFile)
	return s, e
}

func (c *transferClient) stageGitHubExternal(ctx context.Context, root, component string) (preparedExternal, string, error) {
	var rec preparedExternal
	var api, prefix, assetName, checksumName string
	var isCLI bool
	switch component {
	case "plugin":
		api = pluginLatestURL
		prefix = pluginPrefix
		assetName = "DDS.plugin.js"
		checksumName = assetName + ".sha256"
	case "betterdiscord":
		api = bdcliLatestURL
		prefix = bdcliPrefix
		isCLI = true
	default:
		return rec, "", errors.New("неизвестный компонент")
	}
	if prev, path, ok := validExternalCache(root, component, c.AllowTestHTTP); ok {
		c.UsedCachedStage = true
		c.progress("Проверенный локальный staging использован без GitHub API; актуальность Stable онлайн не проверялась.")
		return prev, path, nil
	}
	// Read the exact tag before selecting a release asset: names are tag-bound.
	if c.AllowTestHTTP {
		if component == "plugin" && c.PluginURL != "" {
			api = c.PluginURL
		}
		if component == "betterdiscord" && c.BDCLIURL != "" {
			api = c.BDCLIURL
		}
	}
	c.progress("Запрашиваю сведения об официальном Stable Release...")
	content, e := c.get(ctx, api, 2<<20)
	if e != nil {
		return rec, "", e
	}
	var peek latestRelease
	if e = json.Unmarshal(content, &peek); e != nil {
		return rec, "", e
	}
	version := strings.TrimPrefix(peek.Tag, "v")
	if !finalVersionRE.MatchString(version) || peek.Draft || peek.Prerelease {
		return rec, "", errors.New("не Stable релиз")
	}
	if isCLI {
		assetName = "bdcli_" + version + "_windows_amd64.zip"
		checksumName = "bdcli_checksums.txt"
	}
	// Validate release identity and both assets using the same verified metadata snapshot.
	// c.get above is authoritative; do not make a second mutable latest request.
	var asset, side releaseAsset
	for _, a := range peek.Assets {
		if a.Name == assetName {
			asset = a
		}
		if a.Name == checksumName {
			side = a
		}
	}
	if asset.Name == "" || side.Name == "" || asset.Size <= 0 || asset.Size > externalMaxSize || side.Size <= 0 || side.Size > 8192 || asset.State != "uploaded" || side.State != "uploaded" {
		return rec, "", errors.New("неполный официальный релиз")
	}
	for _, a := range []releaseAsset{asset, side} {
		if !c.AllowTestHTTP && !strings.HasPrefix(a.URL, prefix+peek.Tag+"/") {
			return rec, "", errors.New("источник релиза не совпал")
		}
		if e = c.checkURL(a.URL); e != nil {
			return rec, "", e
		}
		if !strings.HasPrefix(a.Digest, "sha256:") || !hexDigestRE.MatchString(strings.TrimPrefix(a.Digest, "sha256:")) {
			return rec, "", errors.New("отсутствует опубликованный SHA-256")
		}
	}
	sha := strings.TrimPrefix(asset.Digest, "sha256:")
	b, e := c.get(ctx, side.URL, 8192)
	if e != nil {
		return rec, "", e
	}
	if int64(len(b)) != side.Size || h(b) != strings.TrimPrefix(side.Digest, "sha256:") {
		return rec, "", errors.New("контрольная сумма списка хэшей не совпадает")
	}
	listed, e := checksumEntry(b, asset.Name, isCLI)
	if e != nil || listed != sha {
		return rec, "", errors.New("контрольная сумма пакета не совпала со списком")
	}
	componentName := "DDS Plugin"
	payloadName := "DDS.plugin.js"
	if isCLI {
		componentName = "BetterDiscord CLI"
		payloadName = "bdcli.exe"
	}
	parent := filepath.Join(root, component)
	target := filepath.Join(parent, peek.Tag)
	// A verified receipt alone is insufficient; rehash staged files on reuse.
	if prev, ok := readExternalReceipt(target, componentName, version, sha, asset.Name); ok {
		c.progress("Проверенный staging уже существует — повторная загрузка не нужна.")
		return prev, target, nil
	}
	if e = os.MkdirAll(parent, 0700); e != nil {
		return rec, "", e
	}
	if e = requireSpace(c, parent, uint64(asset.Size)); e != nil {
		return rec, "", e
	}
	temp, e := os.MkdirTemp(parent, ".prepare-")
	if e != nil {
		return rec, "", e
	}
	defer os.RemoveAll(temp)
	if e = os.Mkdir(filepath.Join(temp, "package"), 0700); e != nil {
		return rec, "", e
	}
	if e = os.Mkdir(filepath.Join(temp, "payload"), 0700); e != nil {
		return rec, "", e
	}
	packagePath := filepath.Join(temp, "package", asset.Name)
	c.progress("Загружаю " + componentName + " (без установки)...")
	calculated, e := c.download(ctx, asset, packagePath)
	if e != nil {
		return rec, "", e
	}
	c.progress("Загрузка завершена. Проверяю содержимое и подготавливаю staging...")
	if calculated != sha {
		return rec, "", errors.New("неверный хэш скачанного пакета")
	}
	var payloadSHA string
	if isCLI {
		size, e := inspectExternalZIPSize(packagePath)
		if e != nil {
			return rec, "", e
		}
		if e = requireSpace(c, temp, size); e != nil {
			return rec, "", e
		}
		payloadSHA, e = extractBDCLI(packagePath, filepath.Join(temp, "payload"))
		if e != nil {
			return rec, "", e
		}
	} else {
		data, e := os.ReadFile(packagePath)
		if e != nil {
			return rec, "", e
		}
		m := pluginHeaderVersionRE.FindSubmatch(data)
		if len(m) != 2 || string(m[1]) != version {
			return rec, "", errors.New("версия @version DDS Plugin не совпадает с GitHub релизом")
		}
		payloadSHA = h(data)
		if e = os.WriteFile(filepath.Join(temp, "payload", payloadName), data, 0600); e != nil {
			return rec, "", e
		}
	}
	rec = preparedExternal{Schema: "dds-installer-external-stage-v1", Component: componentName, Version: version, Source: asset.URL, SHA256: calculated, ZIPSize: asset.Size, PayloadSHA256: payloadSHA, CreatedUTC: time.Now().UTC().Format(time.RFC3339), Status: "VERIFIED_AND_STAGED"}
	receipt, e := json.MarshalIndent(rec, "", "  ")
	if e != nil {
		return rec, "", e
	}
	if e = os.WriteFile(filepath.Join(temp, "stage_receipt.json"), receipt, 0600); e != nil {
		return rec, "", e
	}
	if e = commitStage(temp, target); e != nil {
		return rec, "", e
	}
	return rec, target, nil
}

func inspectExternalZIPSize(filename string) (uint64, error) {
	z, e := zip.OpenReader(filename)
	if e != nil {
		return 0, e
	}
	defer z.Close()
	var total uint64
	if len(z.File) > 100 {
		return 0, errors.New("слишком много файлов в CLI ZIP")
	}
	for _, f := range z.File {
		if _, er := safeZipMember(f.Name); er != nil {
			return 0, er
		}
		if f.UncompressedSize64 > uint64(50<<20) || total > uint64(50<<20)-f.UncompressedSize64 {
			return 0, errors.New("превышен лимит CLI ZIP")
		}
		total += f.UncompressedSize64
	}
	return total, nil
}

func officialDiscordHost(name string) bool {
	name = strings.ToLower(name)
	return name == "discord.com" || name == "dl.discordapp.net" || name == "discordapp.com" || strings.HasSuffix(name, ".discord.com") || strings.HasSuffix(name, ".discordapp.com") || strings.HasSuffix(name, ".discordapp.net")
}

// Downloaded Discord.exe has no GitHub release sidecar. Windows Authenticode
// verification is therefore mandatory; downloading over TLS alone is not enough.
func verifyDiscordSignature(filename string) error {
	if runtime.GOOS != "windows" {
		return errors.New("цифровая подпись Discord проверяется только на Windows")
	}
	script := `$ErrorActionPreference='Stop'; $s=Get-AuthenticodeSignature -LiteralPath $env:DDS_INSTALLER_VERIFY_PATH; if ($s.Status -ne 'Valid' -or $null -eq $s.SignerCertificate -or $s.SignerCertificate.Subject -notmatch '(?i)(^|,)\s*O=Discord(,?\s*Inc\.?|\s*$)') { Write-Error ('Неверная подпись Discord: '+$s.Status+' / '+$s.SignerCertificate.Subject); exit 1 }`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	hideInstallerCommand(cmd)
	cmd.Env = append(os.Environ(), "DDS_INSTALLER_VERIFY_PATH="+filename)
	out, e := cmd.CombinedOutput()
	if e != nil {
		return fmt.Errorf("подпись официального установщика Discord не подтверждена: %s (%v)", strings.TrimSpace(string(out)), e)
	}
	return nil
}

func (c *transferClient) stageDiscord(ctx context.Context, root string, signatureCheck func(string) error) (preparedExternal, string, error) {
	var rec preparedExternal
	u, e := url.Parse(discordInstallerURL)
	if e != nil || !officialDiscordHost(u.Hostname()) {
		return rec, "", errors.New("некорректный источник Discord")
	}
	// No global signature bypass in product code. The injected check is used by
	// hermetic tests; production always supplies verifyDiscordSignature.
	if signatureCheck == nil {
		return rec, "", errors.New("отсутствует проверка цифровой подписи Discord")
	}
	client := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 8 || r.URL.Scheme != "https" || !officialDiscordHost(r.URL.Hostname()) {
			return errors.New("переадресация Discord за пределы официальных доменов")
		}
		return nil
	}}
	if c.AllowTestHTTP && c.HTTP != nil {
		client = c.HTTP
	}
	target := filepath.Join(root, "discord", "official-stable-x64")
	staged := filepath.Join(target, "package", "DiscordSetup.exe")
	if b, e := os.ReadFile(filepath.Join(target, "stage_receipt.json")); e == nil {
		var old preparedExternal
		if json.Unmarshal(b, &old) == nil && old.Schema == "dds-installer-external-stage-v1" && old.Component == "Discord" && old.Status == "VERIFIED_AND_STAGED" && old.Source == discordInstallerURL {
			if d, n, er := shaFile(staged); er == nil && n == old.ZIPSize && d == old.SHA256 && signatureCheck(staged) == nil {
				c.progress("Подлинность существующего Discord staging проверена — загрузка не нужна.")
				return old, target, nil
			}
		}
	}
	parent := filepath.Dir(target)
	if e = os.MkdirAll(parent, 0700); e != nil {
		return rec, "", e
	}
	// Discord's rolling installer size is not provided as a release manifest.
	// Never demand the size ceiling as if it were actual available-space need.
	temp, e := os.MkdirTemp(parent, ".prepare-")
	if e != nil {
		return rec, "", e
	}
	defer os.RemoveAll(temp)
	if e = os.Mkdir(filepath.Join(temp, "package"), 0700); e != nil {
		return rec, "", e
	}
	p := filepath.Join(temp, "package", "DiscordSetup.exe")
	rawURL := discordInstallerURL
	if c.AllowTestHTTP && c.DiscordURL != "" {
		rawURL = c.DiscordURL
	}
	c.progress("Подключаюсь к официальному источнику Discord Stable...")
	req, e := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if e != nil {
		return rec, "", e
	}
	req.Header.Set("User-Agent", "DDS-Installer/"+installerVersion+"")
	resp, e := client.Do(req)
	if e != nil {
		return rec, "", e
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return rec, "", fmt.Errorf("официальная загрузка Discord: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > externalMaxSize {
		return rec, "", errors.New("слишком большой установщик Discord")
	}
	if resp.ContentLength > 0 {
		if e = requireSpace(c, temp, uint64(resp.ContentLength)); e != nil {
			return rec, "", e
		}
	} else {
		if e = requireSpace(c, temp, stageFreeMargin); e != nil {
			return rec, "", e
		}
	}
	f, e := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return rec, "", e
	}
	digest := sha256.New()
	n, e := copyDownload(io.MultiWriter(f, digest), resp.Body, externalMaxSize, resp.ContentLength, c.DownloadProgress)
	eClose := f.Close()
	if e != nil {
		return rec, "", e
	}
	if eClose != nil {
		return rec, "", eClose
	}
	if n <= 0 || n > externalMaxSize || (resp.ContentLength >= 0 && n != resp.ContentLength) {
		return rec, "", errors.New("неполная загрузка Discord")
	}
	c.progress("Загрузка Discord завершена. Проверяю PE и цифровую подпись издателя...")
	if e = verifyWindowsPE(p); e != nil {
		return rec, "", e
	}
	if e = signatureCheck(p); e != nil {
		return rec, "", e
	}
	sha := hex.EncodeToString(digest.Sum(nil))
	rec = preparedExternal{Schema: "dds-installer-external-stage-v1", Component: "Discord", Version: "official Stable x64 (installer)", Source: discordInstallerURL, SHA256: sha, ZIPSize: n, PayloadSHA256: "", CreatedUTC: time.Now().UTC().Format(time.RFC3339), Status: "VERIFIED_AND_STAGED"}
	data, e := json.MarshalIndent(rec, "", "  ")
	if e != nil {
		return rec, "", e
	}
	if e = os.WriteFile(filepath.Join(temp, "stage_receipt.json"), data, 0600); e != nil {
		return rec, "", e
	}
	if e = commitStage(temp, target); e != nil {
		return rec, "", e
	}
	return rec, target, nil
}
