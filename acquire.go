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
	"path"
	"path/filepath"
	"regexp"
	"strings"
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
