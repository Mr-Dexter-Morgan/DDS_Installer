package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type mutableCompanionRelease struct {
	version string
	payload []byte
	visits  map[string]int
}

func validVariantPackage(version string, app []byte) []byte {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	files := map[string][]byte{
		"DDS.exe":                             []byte("launcher"),
		"DDSUpdater.exe":                      []byte("updater"),
		"versions/" + version + "/DDSApp.exe": app,
	}
	pointer, _ := json.Marshal(currentPointer{Version: version, RelativePath: "versions/" + version, Schema: "dds-version-pointer-v1"})
	files["current.json"] = pointer
	manifest := struct {
		Schema  string                   `json:"schema"`
		Version string                   `json:"version"`
		Files   []map[string]interface{} `json:"files"`
	}{Schema: "dds-install-manifest-v1", Version: version}
	manifest.Files = append(manifest.Files, map[string]interface{}{
		"path":       "DDSApp.exe",
		"sha256":     hashTest(app),
		"size_bytes": len(app),
	})
	manifestBytes, _ := json.Marshal(manifest)
	files["versions/"+version+"/DDS.install.json"] = manifestBytes
	for name, data := range files {
		w, _ := z.Create(name)
		_, _ = w.Write(data)
	}
	_ = z.Close()
	return b.Bytes()
}

func companionFreshnessServer(t *testing.T, state *mutableCompanionRelease) (*transferClient, *httptest.Server) {
	t.Helper()
	if state.visits == nil {
		state.visits = map[string]int{}
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.visits[r.URL.Path]++
		version := state.version
		payload := state.payload
		name := "DDS-" + version + "-windows-x64.zip"
		side := h(payload) + "  " + name + "\n"
		switch r.URL.Path {
		case "/latest":
			_ = json.NewEncoder(w).Encode(latestRelease{Tag: "v" + version, Assets: []releaseAsset{
				{Name: name, URL: server.URL + "/full.zip", Digest: "sha256:" + h(payload), Size: int64(len(payload)), State: "uploaded"},
				{Name: name + ".sha256", URL: server.URL + "/sha256", Digest: "sha256:" + h([]byte(side)), Size: int64(len(side)), State: "uploaded"},
			}})
		case "/full.zip":
			_, _ = w.Write(payload)
		case "/sha256":
			_, _ = w.Write([]byte(side))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return &transferClient{HTTP: server.Client(), LatestURL: server.URL + "/latest", AllowTestHTTP: true}, server
}

func TestCompanionStaleCacheIsNotReused(t *testing.T) {
	state := &mutableCompanionRelease{version: "0.8.5", payload: validVariantPackage("0.8.5", []byte("PE old fixture"))}
	c, _ := companionFreshnessServer(t, state)
	root := t.TempDir()
	first, firstPath, err := c.stageCompanion(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	state.version = "0.8.6"
	state.payload = validVariantPackage("0.8.6", []byte("PE new fixture"))
	c.UsedCachedStage = false
	second, secondPath, err := c.stageCompanion(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if c.UsedCachedStage || second.Version != "0.8.6" || second.SHA256 == first.SHA256 || secondPath == firstPath {
		t.Fatalf("stale cache reused: first=%+v %s second=%+v %s cached=%v", first, firstPath, second, secondPath, c.UsedCachedStage)
	}
	if _, err := os.Stat(filepath.Join(secondPath, "payload", "versions", "0.8.6", "DDSApp.exe")); err != nil {
		t.Fatal(err)
	}
}

func TestCompanionCurrentCacheReusedOnlyAfterMetadataCheck(t *testing.T) {
	state := &mutableCompanionRelease{version: "0.8.5", payload: validVariantPackage("0.8.5", []byte("PE current fixture"))}
	c, _ := companionFreshnessServer(t, state)
	root := t.TempDir()
	if _, _, err := c.stageCompanion(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	beforeLatest := state.visits["/latest"]
	beforeFull := state.visits["/full.zip"]
	beforeSide := state.visits["/sha256"]
	c.UsedCachedStage = false
	if _, _, err := c.stageCompanion(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if !c.UsedCachedStage {
		t.Fatal("matching current cache was not reported as reused")
	}
	if state.visits["/latest"] != beforeLatest+1 || state.visits["/full.zip"] != beforeFull || state.visits["/sha256"] != beforeSide {
		t.Fatalf("unexpected requests latest=%d full=%d side=%d", state.visits["/latest"]-beforeLatest, state.visits["/full.zip"]-beforeFull, state.visits["/sha256"]-beforeSide)
	}
}

func TestCompanionSameVersionChangedSHAReacquires(t *testing.T) {
	state := &mutableCompanionRelease{version: "0.8.5", payload: validVariantPackage("0.8.5", []byte("PE first fixture"))}
	c, _ := companionFreshnessServer(t, state)
	root := t.TempDir()
	first, _, err := c.stageCompanion(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	state.payload = validVariantPackage("0.8.5", []byte("PE second fixture with changed bytes"))
	beforeFull := state.visits["/full.zip"]
	c.UsedCachedStage = false
	second, _, err := c.stageCompanion(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if c.UsedCachedStage || second.SHA256 == first.SHA256 || state.visits["/full.zip"] != beforeFull+1 {
		t.Fatalf("same-version changed SHA not reacquired: first=%s second=%s cached=%v downloads=%d", first.SHA256, second.SHA256, c.UsedCachedStage, state.visits["/full.zip"]-beforeFull)
	}
}

func TestValidCompanionCacheDoesNotMaskLatestLookupFailure(t *testing.T) {
	state := &mutableCompanionRelease{version: "0.8.5", payload: validVariantPackage("0.8.5", []byte("PE fixture"))}
	c, _ := companionFreshnessServer(t, state)
	root := t.TempDir()
	if _, _, err := c.stageCompanion(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	c.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Header: http.Header{}, Body: http.NoBody, Request: r}, nil
	})}
	c.UsedCachedStage = false
	_, _, err := c.stageCompanion(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "503") || c.UsedCachedStage {
		t.Fatalf("lookup failure hidden by cache: err=%v cached=%v", err, c.UsedCachedStage)
	}
}

func TestValidPluginCacheDoesNotMaskLatestLookupFailure(t *testing.T) {
	c, _ := makeSamples(t, nil)
	root := t.TempDir()
	if _, _, err := c.stageGitHubExternal(context.Background(), root, "plugin"); err != nil {
		t.Fatal(err)
	}
	c.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Header: http.Header{}, Body: http.NoBody, Request: r}, nil
	})}
	c.UsedCachedStage = false
	_, _, err := c.stageGitHubExternal(context.Background(), root, "plugin")
	if err == nil || !strings.Contains(err.Error(), "503") || c.UsedCachedStage {
		t.Fatalf("plugin lookup failure hidden by cache: err=%v cached=%v", err, c.UsedCachedStage)
	}
}
