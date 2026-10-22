package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
