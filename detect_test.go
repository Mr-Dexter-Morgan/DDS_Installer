package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func touch(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkctx(t *testing.T) DetectContext {
	t.Helper()
	root := t.TempDir()
	return DetectContext{
		LocalAppData: filepath.Join(root, "local"),
		AppData:      filepath.Join(root, "roaming"),
		InstallerDir: filepath.Join(root, "setup"),
	}
}

func installDiscord(t *testing.T, c DetectContext, version string) {
	touch(t, filepath.Join(c.LocalAppData, "Discord", "Update.exe"), "x")
	touch(t, filepath.Join(c.LocalAppData, "Discord", "app-"+version, "Discord.exe"), "x")
}

func installBD(t *testing.T, c DetectContext) {
	touch(t, filepath.Join(c.AppData, "BetterDiscord", "data", "betterdiscord.asar"), "asar")
}

func installPlugin(t *testing.T, c DetectContext, version string) {
	touch(t, filepath.Join(c.AppData, "BetterDiscord", "plugins", "DDS.plugin.js"), "/**\n * @name DDS\n * @version "+version+"\n */\n")
}

func installCompanion(t *testing.T, root, version string) {
	touch(t, filepath.Join(root, "DDS.exe"), "pe")
	touch(t, filepath.Join(root, "current.json"), `{"schema":"dds-version-pointer-v1","version":"`+version+`","relative_path":"versions/`+version+`"}`)
}

func TestCleanMachineBuildsDependencyPlan(t *testing.T) {
	c := mkctx(t)
	inv := Detect(c)
	if inv.Discord.State != StateMissing {
		t.Fatalf("discord=%s", inv.Discord.State)
	}
	if inv.BetterDiscord.State != StateBlocked {
		t.Fatalf("bd=%s", inv.BetterDiscord.State)
	}
	if inv.Plugin.State != StateBlocked {
		t.Fatalf("plugin=%s", inv.Plugin.State)
	}
	if inv.Companion.State != StateNotDetected {
		t.Fatalf("companion=%s", inv.Companion.State)
	}
	joined := strings.Join(inv.Plan, "|")
	for _, want := range []string{"Discord Stable", "BetterDiscord", "DDS Plugin", "DDS Companion", "Финальная валидация"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("plan missing %q: %v", want, inv.Plan)
		}
	}
}

func TestHealthyMachineNeedsNoAction(t *testing.T) {
	c := mkctx(t)
	installDiscord(t, c, "1.0.9200")
	installBD(t, c)
	installPlugin(t, c, "0.5.3")
	compRoot := filepath.Join(c.LocalAppData, "Programs", "DDS")
	installCompanion(t, compRoot, "0.8.5")
	if err := os.MkdirAll(filepath.Join(c.LocalAppData, "DDS_Companion"), 0o755); err != nil {
		t.Fatal(err)
	}
	inv := Detect(c)
	if inv.Discord.State != StateOK || inv.BetterDiscord.State != StateOK || inv.Plugin.State != StateOK || inv.Companion.State != StateOK || inv.DataRoot.State != StateOK {
		t.Fatalf("unexpected states: %+v", inv)
	}
	if inv.Plugin.Version != "0.5.3" || inv.Companion.Version != "0.8.5" {
		t.Fatalf("versions: %+v", inv)
	}
	if len(inv.Plan) != 1 || !strings.HasPrefix(inv.Plan[0], "NONE") {
		t.Fatalf("plan=%v", inv.Plan)
	}
}

func TestRunningPortableCompanionDetected(t *testing.T) {
	c := mkctx(t)
	installDiscord(t, c, "1.0.9200")
	installBD(t, c)
	installPlugin(t, c, "0.5.3")
	root := filepath.Join(t.TempDir(), "DDSPortable")
	installCompanion(t, root, "0.8.5")
	if err := os.MkdirAll(filepath.Join(root, "Data"), 0o755); err != nil {
		t.Fatal(err)
	}
	c.Processes = []CompanionProcess{{ExecutablePath: filepath.Join(root, "DDS.exe"), CommandLine: `"` + filepath.Join(root, "DDS.exe") + `" --portable`}}
	inv := Detect(c)
	if inv.Companion.State != StateOK || inv.Profile != "portable" {
		t.Fatalf("companion/profile: %+v", inv)
	}
	if inv.DataRoot.Path != filepath.Join(root, "Data") || inv.DataRoot.State != StateOK {
		t.Fatalf("dataroot=%+v", inv.DataRoot)
	}
}

func TestDataRootPreservedWhenCompanionNotDetected(t *testing.T) {
	c := mkctx(t)
	if err := os.MkdirAll(filepath.Join(c.LocalAppData, "DDS_Companion"), 0o755); err != nil {
		t.Fatal(err)
	}
	inv := Detect(c)
	if inv.Companion.State != StateNotDetected {
		t.Fatalf("companion=%s", inv.Companion.State)
	}
	if inv.DataRoot.State != StateOK || !strings.Contains(inv.DataRoot.Detail, "PRESERVED") {
		t.Fatalf("dataroot=%+v", inv.DataRoot)
	}
	if !strings.Contains(strings.Join(inv.Plan, "|"), "DataRoot сохранить") {
		t.Fatalf("plan=%v", inv.Plan)
	}
}

func TestPartialBetterDiscordDoesNotPretendHealthy(t *testing.T) {
	c := mkctx(t)
	installDiscord(t, c, "1.0.9200")
	if err := os.MkdirAll(filepath.Join(c.AppData, "BetterDiscord"), 0o755); err != nil {
		t.Fatal(err)
	}
	installPlugin(t, c, "0.5.3")
	inv := Detect(c)
	if inv.BetterDiscord.State != StatePartial {
		t.Fatalf("bd=%s", inv.BetterDiscord.State)
	}
	if inv.Plugin.State != StatePartial {
		t.Fatalf("plugin=%s", inv.Plugin.State)
	}
	if !strings.Contains(strings.Join(inv.Plan, "|"), "Восстановить BetterDiscord") {
		t.Fatalf("plan=%v", inv.Plan)
	}
}

func TestPluginVersionParser(t *testing.T) {
	c := mkctx(t)
	installDiscord(t, c, "1.0.9200")
	installBD(t, c)
	installPlugin(t, c, "0.5.3")
	p := detectPlugin(c, Component{State: StateOK})
	if p.Version != "0.5.3" {
		t.Fatalf("version=%s", p.Version)
	}
}

func TestCorruptCompanionPointerIsNotAcceptedAsHealthy(t *testing.T) {
	c := mkctx(t)
	root := filepath.Join(c.LocalAppData, "Programs", "DDS")
	touch(t, filepath.Join(root, "DDS.exe"), "pe")
	touch(t, filepath.Join(root, "current.json"), `{not-json`)
	inv := Detect(c)
	if inv.Companion.State != StateNotDetected {
		// Discovery deliberately accepts only a valid pointer; an invalid arbitrary root is not promoted as a valid candidate.
		t.Fatalf("companion=%s", inv.Companion.State)
	}
}

func TestDiscordPartialIsReportedTruthfully(t *testing.T) {
	c := mkctx(t)
	touch(t, filepath.Join(c.LocalAppData, "Discord", "Update.exe"), "x")
	inv := Detect(c)
	if inv.Discord.State != StatePartial {
		t.Fatalf("discord=%s", inv.Discord.State)
	}
	if !strings.Contains(strings.Join(inv.Plan, "|"), "Восстановить Discord Stable") {
		t.Fatalf("plan=%v", inv.Plan)
	}
}

func TestRunningVersionedDDSAppDetectedFromArbitraryRoot(t *testing.T) {
	c := mkctx(t)
	installDiscord(t, c, "1.0.9200")
	installBD(t, c)
	installPlugin(t, c, "0.5.3")
	root := filepath.Join(t.TempDir(), "UserExtractedDDS")
	version := "0.8.5"
	touch(t, filepath.Join(root, "DDS.exe"), "launcher")
	touch(t, filepath.Join(root, "current.json"), `{"schema":"dds-version-pointer-v1","version":"`+version+`","relative_path":"versions/`+version+`"}`)
	payload := filepath.Join(root, "versions", version, "DDSApp.exe")
	touch(t, payload, "pe")
	if err := os.MkdirAll(filepath.Join(c.LocalAppData, "DDS_Companion"), 0o755); err != nil {
		t.Fatal(err)
	}
	c.Processes = []CompanionProcess{{ExecutablePath: payload, CommandLine: `"` + payload + `"`}}

	inv := Detect(c)
	if inv.Companion.State != StateOK || inv.Companion.Version != version {
		t.Fatalf("companion=%+v", inv.Companion)
	}
	if inv.Companion.Path != root {
		t.Fatalf("root=%q want=%q", inv.Companion.Path, root)
	}
	if inv.Profile != "installed" {
		t.Fatalf("profile=%s", inv.Profile)
	}
	if len(inv.Plan) != 1 || !strings.HasPrefix(inv.Plan[0], "NONE") {
		t.Fatalf("plan=%v", inv.Plan)
	}
}

func TestUnrelatedDDSAppIsNotAcceptedWithoutCurrentPointerMatch(t *testing.T) {
	c := mkctx(t)
	root := filepath.Join(t.TempDir(), "DDS")
	touch(t, filepath.Join(root, "DDS.exe"), "launcher")
	touch(t, filepath.Join(root, "current.json"), `{"schema":"dds-version-pointer-v1","version":"0.8.5","relative_path":"versions/0.8.5"}`)
	wrongPayload := filepath.Join(root, "versions", "0.8.4", "DDSApp.exe")
	touch(t, wrongPayload, "pe")
	c.Processes = []CompanionProcess{{ExecutablePath: wrongPayload, CommandLine: wrongPayload}}

	inv := Detect(c)
	if inv.Companion.State != StateNotDetected {
		t.Fatalf("companion=%+v", inv.Companion)
	}
}
