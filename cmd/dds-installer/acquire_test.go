package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func hashTest(data []byte) string { s := sha256.Sum256(data); return hex.EncodeToString(s[:]) }
func fakePackage(version string, add func(*zip.Writer)) []byte {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	files := map[string][]byte{"DDS.exe": []byte("launcher"), "DDSUpdater.exe": []byte("updater"), "versions/" + version + "/DDSApp.exe": []byte("PE dummy")}
	p, _ := json.Marshal(currentPointer{Version: version, RelativePath: "versions/" + version, Schema: "dds-version-pointer-v1"})
	files["current.json"] = p
	manifest := struct {
		Schema  string                   `json:"schema"`
		Version string                   `json:"version"`
		Files   []map[string]interface{} `json:"files"`
	}{Schema: "dds-install-manifest-v1", Version: version}
	manifest.Files = append(manifest.Files, map[string]interface{}{"path": "DDSApp.exe", "sha256": hashTest(files["versions/"+version+"/DDSApp.exe"]), "size_bytes": len(files["versions/"+version+"/DDSApp.exe"])})
	mb, _ := json.Marshal(manifest)
	files["versions/"+version+"/DDS.install.json"] = mb
	for p, d := range files {
		w, _ := z.Create(p)
		w.Write(d)
	}
	if add != nil {
		add(z)
	}
	z.Close()
	return b.Bytes()
}
func mockServer(t *testing.T, zipBytes []byte, sidecarOverride string, modify func(*latestRelease)) (*transferClient, *int) {
	t.Helper()
	requests := new(int)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requests++
		version := "0.8.5"
		name := "DDS-" + version + "-windows-x64.zip"
		switch r.URL.Path {
		case "/latest":
			a := latestRelease{Tag: "v" + version, Assets: []releaseAsset{
				{Name: name, URL: server.URL + "/full.zip", Digest: "sha256:" + hashTest(zipBytes), Size: int64(len(zipBytes)), State: "uploaded"},
				{Name: name + ".sha256", URL: server.URL + "/sha256", Size: int64(len(h(zipBytes) + "  " + name + "\n")), State: "uploaded"},
			}}
			if modify != nil {
				modify(&a)
			}
			json.NewEncoder(w).Encode(a)
		case "/full.zip":
			w.Write(zipBytes)
		case "/sha256":
			s := hashTest(zipBytes) + "  " + name + "\n"
			if sidecarOverride != "" {
				s = sidecarOverride
			}
			w.Write([]byte(s))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return &transferClient{HTTP: server.Client(), LatestURL: server.URL + "/latest", AllowTestHTTP: true}, requests
}
func TestStageVerifiedAndIdempotent(t *testing.T) {
	data := fakePackage("0.8.5", nil)
	c, calls := mockServer(t, data, "", nil)
	dir := t.TempDir()
	ctx := context.Background()
	got, path, err := c.stageCompanion(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "VERIFIED_AND_STAGED" || got.SHA256 != hashTest(data) || got.Files != 5 {
		t.Fatalf("unexpected receipt %+v", got)
	}
	if _, err = os.Stat(filepath.Join(path, "payload", "versions", "0.8.5", "DDSApp.exe")); err != nil {
		t.Fatal(err)
	}
	firstCalls := *calls
	second, p2, e := c.stageCompanion(ctx, dir)
	if e != nil || path != p2 || second.SHA256 != got.SHA256 {
		t.Fatalf("idempotence %+v %q %v", second, p2, e)
	}
	if *calls != firstCalls {
		t.Fatalf("unnecessary GitHub API request %d -> %d", firstCalls, *calls)
	}
}
func TestWrongSidecarLeavesNoStaging(t *testing.T) {
	data := fakePackage("0.8.5", nil)
	c, _ := mockServer(t, data, strings.Repeat("0", 64)+"  DDS-0.8.5-windows-x64.zip\n", nil)
	dir := t.TempDir()
	_, _, err := c.stageCompanion(context.Background(), dir)
	if err == nil || !strings.Contains(err.Error(), "sidecar") {
		t.Fatalf("err=%v", err)
	}
	if x, _ := filepath.Glob(filepath.Join(dir, "companion", "*")); len(x) > 0 {
		t.Fatalf("unexpected files: %v", x)
	}
}
func TestNoDigestFailsClosed(t *testing.T) {
	data := fakePackage("0.8.5", nil)
	c, _ := mockServer(t, data, "", func(r *latestRelease) { r.Assets[0].Digest = "" })
	_, _, err := c.stageCompanion(context.Background(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("err=%v", err)
	}
}
func TestNoReleaseOrNetworkFailsClosed(t *testing.T) {
	c, _ := mockServer(t, fakePackage("0.8.5", nil), "", func(r *latestRelease) { r.Prerelease = true })
	_, _, err := c.stageCompanion(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("prerelease accepted")
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer s.Close()
	c = &transferClient{HTTP: s.Client(), LatestURL: s.URL, AllowTestHTTP: true}
	_, _, err = c.stageCompanion(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("HTTP 503 accepted")
	}
}
func TestRejectMaliciousZIPAndKeepLiveData(t *testing.T) {
	cases := map[string]func(*zip.Writer){
		"traversal":        func(z *zip.Writer) { w, _ := z.Create("../escape.txt"); w.Write([]byte("bad")) },
		"windows-absolute": func(z *zip.Writer) { w, _ := z.Create("C:/Windows/suspicious.exe"); w.Write([]byte("bad")) },
		"duplicate":        func(z *zip.Writer) { w, _ := z.Create("dds.exe"); w.Write([]byte("bad")) },
		"backslash":        func(z *zip.Writer) { w, _ := z.Create(`versions\\x`); w.Write([]byte("bad")) },
		"symlink": func(z *zip.Writer) {
			hdr := &zip.FileHeader{Name: "link"}
			hdr.SetMode(os.ModeSymlink | 0777)
			w, _ := z.CreateHeader(hdr)
			w.Write([]byte("../bad"))
		},
	}
	for name, add := range cases {
		t.Run(name, func(t *testing.T) {
			data := fakePackage("0.8.5", add)
			c, _ := mockServer(t, data, "", nil)
			root := t.TempDir()
			live := filepath.Join(root, "live", "DDS_Data", "sentinel.txt")
			os.MkdirAll(filepath.Dir(live), 0700)
			os.WriteFile(live, []byte("immutable"), 0600)
			stageRoot := filepath.Join(root, "staging")
			_, _, err := c.stageCompanion(context.Background(), stageRoot)
			if err == nil {
				t.Fatal("malicious archive staged")
			}
			got, _ := os.ReadFile(live)
			if string(got) != "immutable" {
				t.Fatal("live data changed")
			}
			if _, err := os.Stat(filepath.Join(root, "escape.txt")); !os.IsNotExist(err) {
				t.Fatal("ZIP escaped staging")
			}
			if names, _ := filepath.Glob(filepath.Join(stageRoot, "companion", "*")); len(names) > 0 {
				t.Fatalf("stage leftover: %v", names)
			}
		})
	}
}
func TestCorruptInternalManifestRejected(t *testing.T) {
	// ZIP hash is genuine, but the embedded file digest disagrees with the contents.
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	ver := "0.8.5"
	for _, p := range []string{"DDS.exe", "DDSUpdater.exe", "versions/0.8.5/DDSApp.exe"} {
		w, _ := z.Create(p)
		w.Write([]byte("x"))
	}
	w, _ := z.Create("current.json")
	fmt.Fprint(w, `{"schema":"dds-version-pointer-v1","version":"0.8.5","relative_path":"versions/0.8.5"}`)
	w, _ = z.Create("versions/0.8.5/DDS.install.json")
	fmt.Fprint(w, `{"schema":"dds-install-manifest-v1","version":"0.8.5","files":[{"path":"DDSApp.exe","sha256":"`+strings.Repeat("0", 64)+`","size_bytes":1}]}`)
	z.Close()
	c, _ := mockServer(t, b.Bytes(), "", nil)
	_, _, err := c.stageCompanion(context.Background(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "хэш") {
		t.Fatalf("err=%v", err)
	}
	_ = ver
}
func TestPromptNeedsExplicitOne(t *testing.T) {
	for _, input := range []string{"\n", "0\n", "yes\n", "2\n"} {
		if promptForStage(strings.NewReader(input), io.Discard) {
			t.Fatalf("triggered: %q", input)
		}
	}
	if !promptForStage(strings.NewReader("1\n"), io.Discard) {
		t.Fatal("explicit one ignored")
	}
}
func TestNoPartialPublishedOnCancel(t *testing.T) {
	c, _ := mockServer(t, fakePackage("0.8.5", nil), "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := t.TempDir()
	_, _, err := c.stageCompanion(ctx, root)
	if err == nil {
		t.Fatal("cancelled stage worked")
	}
	if names, _ := filepath.Glob(filepath.Join(root, "companion", "*")); len(names) > 0 {
		t.Fatalf("left staging %v", names)
	}
}
func TestHelpers(t *testing.T) {
	if !allowedGithubHost("release-assets.githubusercontent.com") || allowedGithubHost("github.com.evil.net") {
		t.Fatal("host verification wrong")
	}
	if _, e := parseSHA256Sidecar([]byte(strings.Repeat("f", 64)+"  DDS-0.8.5-windows-x64.zip\n"), "DDS-0.8.5-windows-x64.zip"); e != nil {
		t.Fatal(e)
	}
	if _, e := parseSHA256Sidecar([]byte(strings.Repeat("f", 64)+"  other.zip\n"), "DDS-0.8.5-windows-x64.zip"); e == nil {
		t.Fatal("sidecar wrong filename accepted")
	}
	// Stage refresh path should never accidentally overwrite a previously accepted stage on network failures.
	_ = time.Second
}

func TestLowDiskRejectedBeforeDownload(t *testing.T) {
	c, calls := mockServer(t, fakePackage("0.8.5", nil), "", nil)
	c.FreeBytes = func(string) (uint64, error) { return 1, nil }
	_, _, err := c.stageCompanion(context.Background(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "недостаточно места") {
		t.Fatalf("err=%v", err)
	}
	if *calls != 1 {
		t.Fatalf("download attempted before disk preflight: %d", *calls)
	}
}
func TestInvalidOldStageCanRecoverWithoutTouchingLiveData(t *testing.T) {
	c, _ := mockServer(t, fakePackage("0.8.5", nil), "", nil)
	root := t.TempDir()
	target := filepath.Join(root, "companion", "v0.8.5")
	os.MkdirAll(target, 0700)
	os.WriteFile(filepath.Join(target, "stage_receipt.json"), []byte("corrupt"), 0600)
	out, path, err := c.stageCompanion(context.Background(), root)
	if err != nil || path != target || out.Status != "VERIFIED_AND_STAGED" {
		t.Fatalf("%v %s %+v", err, path, out)
	}
	stageFiles, _ := filepath.Glob(filepath.Join(root, "companion", ".*"))
	if len(stageFiles) > 0 {
		t.Fatalf("stale directories remain %v", stageFiles)
	}
}
func TestWrongArchiveBytesRejected(t *testing.T) {
	original := fakePackage("0.8.5", nil)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := "DDS-0.8.5-windows-x64.zip"
		switch r.URL.Path {
		case "/latest":
			json.NewEncoder(w).Encode(latestRelease{Tag: "v0.8.5", Assets: []releaseAsset{
				{Name: name, URL: srv.URL + "/full.zip", Digest: "sha256:" + hashTest(original), Size: int64(len(original)), State: "uploaded"},
				{Name: name + ".sha256", URL: srv.URL + "/sha256", Size: int64(len(hashTest(original) + "  " + name + "\n")), State: "uploaded"}}})
		case "/full.zip":
			corrupt := bytes.Repeat([]byte("x"), len(original))
			w.Write(corrupt)
		case "/sha256":
			fmt.Fprintln(w, hashTest(original)+"  "+name)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &transferClient{HTTP: srv.Client(), LatestURL: srv.URL + "/latest", AllowTestHTTP: true, FreeBytes: func(string) (uint64, error) { return 10 << 30, nil }}
	root := t.TempDir()
	_, _, err := c.stageCompanion(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "SHA-256 скачанного") {
		t.Fatalf("err=%v", err)
	}
	if staged, _ := filepath.Glob(filepath.Join(root, "companion", "v*")); len(staged) > 0 {
		t.Fatalf("corrupt package staged %v", staged)
	}
}

func TestUnlistedFileInsideValidArchiveRejected(t *testing.T) {
	archive := fakePackage("0.8.5", func(z *zip.Writer) { w, _ := z.Create("versions/0.8.5/unlisted.exe"); w.Write([]byte("extra")) })
	c, _ := mockServer(t, archive, "", nil)
	_, _, err := c.stageCompanion(context.Background(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "вне внутреннего манифеста") {
		t.Fatalf("err=%v", err)
	}
}

func TestSpacePreflightUsesPackageSizeNotBombLimit(t *testing.T) {
	data := fakePackage("0.8.5", nil)
	c, calls := mockServer(t, data, "", nil)
	queries := 0
	c.FreeBytes = func(string) (uint64, error) {
		queries++
		if queries == 1 {
			return uint64(len(data)) + stageFreeMargin, nil
		}
		// Only the real small expanded payload plus margin is necessary,
		// even though the anti-ZIP-bomb safety ceiling is 1.5 GiB.
		return 100 << 20, nil
	}
	rec, _, err := c.stageCompanion(context.Background(), t.TempDir())
	if err != nil || rec.Status != "VERIFIED_AND_STAGED" {
		t.Fatalf("%+v %v", rec, err)
	}
	if queries != 2 || *calls != 3 {
		t.Fatalf("queries=%d requests=%d", queries, *calls)
	}
	if uint64(len(data))+stageFreeMargin >= uint64(maxUnpacked) {
		t.Fatal("test was not exercising a smaller disk than ZIP safety ceiling")
	}
}

func TestLowSpaceAfterDownloadLeavesNoPublishedStage(t *testing.T) {
	data := fakePackage("0.8.5", nil)
	c, _ := mockServer(t, data, "", nil)
	check := 0
	c.FreeBytes = func(string) (uint64, error) {
		check++
		if check == 1 {
			return uint64(len(data)) + stageFreeMargin, nil
		}
		return stageFreeMargin - 1, nil
	}
	root := t.TempDir()
	_, _, err := c.stageCompanion(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "для распаковки") {
		t.Fatalf("error=%v", err)
	}
	if check != 2 {
		t.Fatalf("unexpected disk check count=%d", check)
	}
	if paths, _ := filepath.Glob(filepath.Join(root, "companion", "*")); len(paths) != 0 {
		t.Fatalf("published invalid stage %v", paths)
	}
	if paths, _ := filepath.Glob(filepath.Join(root, "companion", ".*")); len(paths) != 0 {
		t.Fatalf("temp stage leaked %v", paths)
	}
}

func TestFreeSpaceGuardOverflowAndExactBoundary(t *testing.T) {
	if enoughStageSpace(1, ^uint64(0)) || enoughStageSpace(stageFreeMargin, 1) {
		t.Fatal("accepted overrun or insufficient space")
	}
	if !enoughStageSpace(1234+stageFreeMargin, 1234) {
		t.Fatal("minimum margin rejected")
	}
	if enoughStageSpace(1234+stageFreeMargin-1, 1234) {
		t.Fatal("off-by-one disk guard")
	}
}

func TestInspectZIPUsesRealExpandedSizeAndRejectsInvalidEntry(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ok.zip")
	data := fakePackage("0.8.5", nil)
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	z, e := zip.OpenReader(p)
	if e != nil {
		t.Fatal(e)
	}
	var actual uint64
	for _, f := range z.File {
		actual += f.UncompressedSize64
	}
	z.Close()
	got, err := inspectZIPForStaging(p)
	if err != nil || got != actual {
		t.Fatalf("size=%d expected=%d err=%v", got, actual, err)
	}
	bad := fakePackage("0.8.5", func(z *zip.Writer) { w, _ := z.Create("../escape"); w.Write([]byte("bad")) })
	if e = os.WriteFile(p, bad, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = inspectZIPForStaging(p); e == nil {
		t.Fatal("unsafe ZIP metadata passed preflight")
	}
}

func TestCacheOnlyFourFloorsUseNoGithubAPI(t *testing.T) {
	c, visits := makeSamples(t, nil)
	root := t.TempDir()
	initial := runCascade(context.Background(), c, root, []string{"discord", "betterdiscord", "plugin", "companion"}, func(string) error { return nil })
	for _, x := range initial {
		if x.Result != "VERIFIED_AND_STAGED" {
			t.Fatalf("initial: %#v", initial)
		}
	}
	prev := make(map[string]int)
	for key, count := range visits {
		prev[key] = count
	}
	second := runCascade(context.Background(), c, root, []string{"discord", "betterdiscord", "plugin", "companion"}, func(string) error { return nil })
	for _, x := range second {
		if x.Component != "discord" && x.Result != "VERIFIED_CACHED" {
			t.Fatalf("cached: %#v", second)
		}
		if x.Component == "discord" && x.Result != "VERIFIED_AND_STAGED" {
			t.Fatalf("discord: %#v", second)
		}
	}
	for key, count := range visits {
		if prev[key] != count {
			t.Fatalf("network used for cached floor: %s = %d -> %d", key, prev[key], count)
		}
	}
}

func TestExternalCacheRejectsTamperedReceiptOriginAndVersion(t *testing.T) {
	c, _ := makeSamples(t, nil)
	root := t.TempDir()
	_, path, e := c.stageGitHubExternal(context.Background(), root, "plugin")
	if e != nil {
		t.Fatal(e)
	}
	c.AllowTestHTTP = false // enforce official GitHub identity even in sandbox
	if _, _, ok := validExternalCache(root, "plugin", false); ok {
		t.Fatal("test server origin wrongly trusted")
	}
	c.AllowTestHTTP = true
	for _, file := range []string{filepath.Join(path, "package", "DDS.plugin.js"), filepath.Join(path, "payload", "DDS.plugin.js")} {
		b, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(file, []byte("tampered"), 0600); e != nil {
			t.Fatal(e)
		}
		if _, _, ok := validExternalCache(root, "plugin", true); ok {
			t.Fatal("tampered file reused")
		}
		if e = os.WriteFile(file, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if _, _, ok := validExternalCache(root, "plugin", true); !ok {
		t.Fatal("original valid cache rejected")
	}
}

func TestCompanionCacheRejectsCorruptionWithoutNetwork(t *testing.T) {
	c, calls := mockServer(t, fakePackage("0.8.5", nil), "", nil)
	root := t.TempDir()
	_, path, e := c.stageCompanion(context.Background(), root)
	if e != nil {
		t.Fatal(e)
	}
	old := *calls
	_, _, e = c.stageCompanion(context.Background(), root)
	if e != nil || *calls != old {
		t.Fatalf("cache triggered network: %d->%d, %v", old, *calls, e)
	}
	app := filepath.Join(path, "payload", "versions", "0.8.5", "DDSApp.exe")
	if e = os.WriteFile(app, []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, ok := validCompanionCache(root, true); ok {
		t.Fatal("corrupt manifest payload used")
	}
}

func TestGithubHTTP403DiagnosticsNoRetry(t *testing.T) {
	cases := []struct {
		name    string
		headers http.Header
		want    string
	}{
		{"rate-limit", http.Header{"X-Ratelimit-Remaining": []string{"0"}, "X-Ratelimit-Reset": []string{"1790000000"}}, "rate limit"},
		{"retry", http.Header{"Retry-After": []string{"120"}}, "Retry-After=120"},
		{"unknown", http.Header{}, "причина неизвестна"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Response{StatusCode: 403, Header: tt.headers}
			msg := githubHTTPError(r).Error()
			if !strings.Contains(msg, "HTTP 403") || !strings.Contains(msg, tt.want) {
				t.Fatalf("%s", msg)
			}
		})
	}
}

func TestWhenNoCacheGithub403IsNotSilentlyIgnored(t *testing.T) {
	c, _ := makeSamples(t, nil)
	c.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Header: http.Header{}, Body: http.NoBody, Request: r}, nil
	})}
	_, _, err := c.stageGitHubExternal(context.Background(), t.TempDir(), "plugin")
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("missing block: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProgressKnownSizeIsMeasured(t *testing.T) {
	src := bytes.Repeat([]byte("A"), 64<<10)
	var dst bytes.Buffer
	var seen []downloadProgress
	n, err := copyDownload(&dst, bytes.NewReader(src), int64(len(src)), int64(len(src)), func(v downloadProgress) { seen = append(seen, v) })
	if err != nil || n != int64(len(src)) || !bytes.Equal(src, dst.Bytes()) {
		t.Fatal("bytes changed", n, err)
	}
	if len(seen) < 2 || seen[0].Bytes != 0 || seen[0].Finished || !seen[len(seen)-1].Finished || seen[len(seen)-1].Bytes != n || seen[len(seen)-1].Total != n {
		t.Fatalf("incorrect progress: %+v", seen)
	}
	line := formatDownloadLine(n, n, 0, 2*time.Second, true)
	if !strings.Contains(line, "100%") || !strings.Contains(line, "[##################]") {
		t.Fatal(line)
	}
}

func TestProgressUnknownSizeDoesNotInventPercent(t *testing.T) {
	line := formatDownloadLine(512<<10, -1, 2, 3*time.Second, false)
	if strings.Contains(line, "%") || !strings.Contains(line, "0.5 МиБ") || !strings.Contains(line, "3 с") {
		t.Fatal(line)
	}
	line2 := formatDownloadLine(512<<10, -1, 2, 3*time.Second, true)
	if strings.Contains(line2, "%") || !strings.Contains(line2, "размер неизвестен") {
		t.Fatal(line2)
	}
}

func TestProgressPrematureEndDoesNotClaimComplete(t *testing.T) {
	var out bytes.Buffer
	var final downloadProgress
	_, err := copyDownload(&out, strings.NewReader("abc"), 99, 30, func(x downloadProgress) { final = x })
	if err != nil || !final.Finished || final.Bytes != 3 || final.Total != 30 {
		t.Fatal(final, err)
	}
	line := formatDownloadLine(final.Bytes, final.Total, 0, 0, true)
	if strings.Contains(line, "100%") || !strings.Contains(line, " 10%") {
		t.Fatal(line)
	}
}

type failingDownloadReader struct{ read bool }

func (f *failingDownloadReader) Read(b []byte) (int, error) {
	if !f.read {
		f.read = true
		copy(b, []byte("abc"))
		return 3, nil
	}
	return 0, errors.New("connection lost")
}
func TestProgressErrorTerminatesDisplay(t *testing.T) {
	var target bytes.Buffer
	display := newConsoleDownloadDisplay(&target)
	_, err := copyDownload(io.Discard, &failingDownloadReader{}, 100, 10, display.update)
	if err == nil || !strings.Contains(err.Error(), "connection lost") {
		t.Fatal(err)
	}
	text := target.String()
	if !strings.HasSuffix(text, "\n") || strings.Contains(text, "100%") || !strings.Contains(text, "30%") {
		t.Fatal(text)
	}
}

func TestDownloadProgressDoesNotChangeSizeLimit(t *testing.T) {
	var dst bytes.Buffer
	n, err := copyDownload(&dst, strings.NewReader("abcdef"), 3, 6, nil)
	if err != nil || n != 4 || dst.String() != "abcd" {
		t.Fatal(n, err, dst.String())
	}
}

func TestProgressHeartbeatWhenNetworkStalls(t *testing.T) {
	var b bytes.Buffer
	display := newConsoleDownloadDisplay(&b)
	display.update(downloadProgress{Bytes: 0, Total: 100})
	time.Sleep(1100 * time.Millisecond)
	display.update(downloadProgress{Bytes: 10, Total: 100, Finished: true})
	if strings.Count(b.String(), "\r") < 3 || strings.Contains(b.String(), "100%") {
		t.Fatalf("no honest heartbeat during stall: %q", b.String())
	}
}

func fakeCLIArchive(extra func(*zip.Writer)) []byte {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, _ := z.Create("bdcli.exe")
	f.Write([]byte("MZ-bdcli-windows64"))
	if extra != nil {
		extra(z)
	}
	z.Close()
	return b.Bytes()
}

func fakeSources(t *testing.T, plugin, bd, comp []byte, sideOverride map[string]string) (*transferClient, map[string]int) {
	t.Helper()
	visits := map[string]int{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		visits[r.URL.Path]++
		makeRelease := func(tag string, a []byte, name, shaFile string, side []byte) {
			json.NewEncoder(w).Encode(latestRelease{Tag: tag, Assets: []releaseAsset{
				{Name: name, URL: server.URL + "/assets/" + name, Size: int64(len(a)), Digest: "sha256:" + h(a), State: "uploaded"},
				{Name: shaFile, URL: server.URL + "/assets/" + shaFile, Size: int64(len(side)), Digest: "sha256:" + h(side), State: "uploaded"},
			}})
		}
		pluginSHA := h(plugin) + "  DDS.plugin.js\n"
		cliSHA := h(bd) + "  bdcli_1.0.0_windows_amd64.zip\n" + strings.Repeat("a", 64) + "  other.zip\n"
		if v, ok := sideOverride["plugin"]; ok {
			pluginSHA = v
		}
		if v, ok := sideOverride["betterdiscord"]; ok {
			cliSHA = v
		}
		compSHA := h(comp) + "  DDS-0.8.5-windows-x64.zip\n"
		switch r.URL.Path {
		case "/plugin/latest":
			makeRelease("v0.5.3", plugin, "DDS.plugin.js", "DDS.plugin.js.sha256", []byte(pluginSHA))
		case "/bd/latest":
			makeRelease("v1.0.0", bd, "bdcli_1.0.0_windows_amd64.zip", "bdcli_checksums.txt", []byte(cliSHA))
		case "/companion/latest":
			makeRelease("v0.8.5", comp, "DDS-0.8.5-windows-x64.zip", "DDS-0.8.5-windows-x64.zip.sha256", []byte(compSHA))
		case "/assets/DDS.plugin.js":
			w.Write(plugin)
		case "/assets/DDS.plugin.js.sha256":
			w.Write([]byte(pluginSHA))
		case "/assets/bdcli_1.0.0_windows_amd64.zip":
			w.Write(bd)
		case "/assets/bdcli_checksums.txt":
			w.Write([]byte(cliSHA))
		case "/assets/DDS-0.8.5-windows-x64.zip":
			w.Write(comp)
		case "/assets/DDS-0.8.5-windows-x64.zip.sha256":
			w.Write([]byte(compSHA))
		case "/discord":
			w.Write([]byte("MZ-signed-Discord-setup"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	c := &transferClient{HTTP: server.Client(), AllowTestHTTP: true, LatestURL: server.URL + "/companion/latest", PluginURL: server.URL + "/plugin/latest", BDCLIURL: server.URL + "/bd/latest", DiscordURL: server.URL + "/discord"}
	return c, visits
}
func samplePlugin() []byte {
	return []byte("/**\n * @name DDS\n * @version 0.5.3\n */\nmodule.exports = {};\n")
}
func makeSamples(t *testing.T, overrides map[string]string) (*transferClient, map[string]int) {
	return fakeSources(t, samplePlugin(), fakeCLIArchive(nil), fakePackage("0.8.5", nil), overrides)
}
func TestFourIndependentFloors(t *testing.T) {
	c, visits := makeSamples(t, nil)
	live := filepath.Join(t.TempDir(), "LIVE_UNTOUCHABLE")
	os.WriteFile(live, []byte("archived"), 0600)
	out := runCascade(context.Background(), c, t.TempDir(), []string{"discord", "betterdiscord", "plugin", "companion"}, func(s string) error { return nil })
	if len(out) != 4 {
		t.Fatal(out)
	}
	for _, o := range out {
		if o.Result != "VERIFIED_AND_STAGED" {
			t.Fatalf("%s: %v", o.Component, o.Reason)
		}
		if _, e := os.Stat(o.Location); e != nil {
			t.Fatal(e)
		}
	}
	v, e := os.ReadFile(live)
	if e != nil || string(v) != "archived" {
		t.Fatal("data touched")
	}
	if visits["/assets/DDS.plugin.js"] != 1 || visits["/discord"] != 1 || visits["/assets/bdcli_1.0.0_windows_amd64.zip"] != 1 {
		t.Fatal(visits)
	}
}
func TestIndependentFailureDoesNotDestroyNextFloor(t *testing.T) {
	c, _ := makeSamples(t, map[string]string{"betterdiscord": strings.Repeat("0", 64) + "  bdcli_1.0.0_windows_amd64.zip\n"})
	out := runCascade(context.Background(), c, t.TempDir(), []string{"discord", "betterdiscord", "plugin", "companion"}, func(string) error { return nil })
	if out[0].Result != "VERIFIED_AND_STAGED" || out[1].Result != "FAILED" || out[2].Result != "VERIFIED_AND_STAGED" || out[3].Result != "VERIFIED_AND_STAGED" {
		t.Fatalf("unexpected isolation %#v", out)
	}
}
func TestPluginStageReuseAvoidsRedownload(t *testing.T) {
	c, visits := makeSamples(t, nil)
	root := t.TempDir()
	r, p, e := c.stageGitHubExternal(context.Background(), root, "plugin")
	if e != nil {
		t.Fatal(e)
	}
	if r.Version != "0.5.3" || r.SHA256 != h(samplePlugin()) {
		t.Fatal(r)
	}
	a := visits["/assets/DDS.plugin.js"]
	_, p2, e := c.stageGitHubExternal(context.Background(), root, "plugin")
	if e != nil || p != p2 || visits["/assets/DDS.plugin.js"] != a {
		t.Fatalf("unexpected re-download %v", e)
	}
}
func TestChangedStagedPluginRepairsOnlyItsOwnStage(t *testing.T) {
	c, visits := makeSamples(t, nil)
	root := t.TempDir()
	_, p, e := c.stageGitHubExternal(context.Background(), root, "plugin")
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(p, "payload", "DDS.plugin.js"), []byte("tampered"), 0600)
	_, _, e = c.stageGitHubExternal(context.Background(), root, "plugin")
	if e != nil {
		t.Fatal(e)
	}
	if visits["/assets/DDS.plugin.js"] != 2 {
		t.Fatal("tampered stage reused", visits)
	}
}
func TestRejectPluginVersionMismatch(t *testing.T) {
	c, _ := fakeSources(t, []byte("/**\n * @version 0.5.2\n*/\n"), fakeCLIArchive(nil), fakePackage("0.8.5", nil), nil)
	_, _, e := c.stageGitHubExternal(context.Background(), t.TempDir(), "plugin")
	if e == nil || !strings.Contains(e.Error(), "@version") {
		t.Fatalf("unverified plugin %v", e)
	}
}
func TestRejectMaliciousBDArchive(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    func(*zip.Writer)
	}{
		{"traversal", func(z *zip.Writer) { f, _ := z.Create("../evil.exe"); f.Write([]byte("oops")) }},
		{"duplicate", func(z *zip.Writer) { f, _ := z.Create("BDCLI.exe"); f.Write([]byte("MZ-duplicate")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := fakeSources(t, samplePlugin(), fakeCLIArchive(tc.f), fakePackage("0.8.5", nil), nil)
			_, _, e := c.stageGitHubExternal(context.Background(), t.TempDir(), "betterdiscord")
			if e == nil {
				t.Fatal("unsafe ZIP accepted")
			}
		})
	}
}
func TestDiscordSignatureFailureLeavesNoReadyStage(t *testing.T) {
	c, _ := makeSamples(t, nil)
	root := t.TempDir()
	_, _, e := c.stageDiscord(context.Background(), root, func(string) error { return errors.New("not trusted") })
	if e == nil || !strings.Contains(e.Error(), "not trusted") {
		t.Fatal(e)
	}
	_, e = os.Stat(filepath.Join(root, "discord", "official-stable-x64"))
	if !os.IsNotExist(e) {
		t.Fatal("unsigned stage published")
	}
}
func TestDiscordStageReuseAndSignerRechecked(t *testing.T) {
	c, visits := makeSamples(t, nil)
	root := t.TempDir()
	calls := 0
	sign := func(file string) error { calls++; return verifyWindowsPE(file) }
	_, p, e := c.stageDiscord(context.Background(), root, sign)
	if e != nil {
		t.Fatal(e)
	}
	_, q, e := c.stageDiscord(context.Background(), root, sign)
	if e != nil || p != q || visits["/discord"] != 1 || calls != 2 {
		t.Fatalf("not reused safely: %v %s %s calls %d", e, p, q, calls)
	}
}
func TestActualPublishedAssetConventions(t *testing.T) {
	cli := "bdcli_" + "1.0.0" + "_windows_amd64.zip"
	if cli != "bdcli_1.0.0_windows_amd64.zip" {
		t.Fatal(cli)
	}
	if !officialDiscordHost("discord.com") || !officialDiscordHost("dl.discordapp.net") || officialDiscordHost("discord.com.evil.org") {
		t.Fatal("url validation failed")
	}
	if _, err := checksumEntry([]byte(fmt.Sprintf("%s  %s\n%s  other\n", strings.Repeat("a", 64), cli, strings.Repeat("b", 64))), cli, true); err != nil {
		t.Fatal(err)
	}
}
func TestPlanOnlyMissingAndFull(t *testing.T) {
	i := Inventory{Discord: Component{State: StateOK}, BetterDiscord: Component{State: StateMissing}, Plugin: Component{State: StateBlocked}, Companion: Component{State: StateOK}}
	all := planStage(i, true)
	need := planStage(i, false)
	if len(all) != 4 || len(need) != 2 || need[0] != "betterdiscord" || need[1] != "plugin" {
		t.Fatalf("bad plan %v %v", all, need)
	}
}
func TestNoAutomaticNetworkOnQuit(t *testing.T) {
	for _, s := range []string{"\n", "n\n"} {
		var out bytes.Buffer
		if selectStage(strings.NewReader(s), &out) != "" && s == "\n" {
			t.Fatal("quit failed")
		}
	}
}
func TestLowDiskProtectsExternalComponents(t *testing.T) {
	c, _ := makeSamples(t, nil)
	c.FreeBytes = func(string) (uint64, error) { return 1, nil }
	_, _, err := c.stageGitHubExternal(context.Background(), t.TempDir(), "plugin")
	if err == nil || !strings.Contains(err.Error(), "места") {
		t.Fatalf("no disk guard: %v", err)
	}
}
func TestInvalidChecksumsFailClosed(t *testing.T) {
	c, _ := makeSamples(t, map[string]string{"plugin": "no valid digest"})
	_, _, e := c.stageGitHubExternal(context.Background(), t.TempDir(), "plugin")
	if e == nil {
		t.Fatal("malformed digest accepted")
	}
}
