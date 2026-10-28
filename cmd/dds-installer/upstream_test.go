package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
