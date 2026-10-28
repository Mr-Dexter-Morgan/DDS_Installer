package main

// DDS Installer 0.2.0-r2: independent source acquisition floors. These
// routines never invoke downloaded programs or write into installed locations.
import (
	"archive/zip"
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
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

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
	// Freshness gate: integrity of a local stage does not prove that it is
	// still the current public Stable. Resolve the current release identity
	// first, then reuse staged bytes only on exact version+SHA-256 match.
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
	componentName := "DDS Plugin"
	payloadName := "DDS.plugin.js"
	if isCLI {
		componentName = "BetterDiscord CLI"
		payloadName = "bdcli.exe"
	}
	parent := filepath.Join(root, component)
	target := filepath.Join(parent, peek.Tag)
	// The GitHub metadata snapshot above establishes current Stable identity.
	// A matching receipt is still insufficient on its own: readExternalReceipt
	// rehashes the staged package/payload before reuse.
	if prev, ok := readExternalReceipt(target, componentName, version, sha, asset.Name); ok {
		c.UsedCachedStage = true
		c.progress("Staging совпадает с текущим Stable по версии и SHA-256 — повторная загрузка не нужна.")
		return prev, target, nil
	}
	// No exact current-stage match: authenticate the release sidecar before any
	// new bytes are staged.
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
