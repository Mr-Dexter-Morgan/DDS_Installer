package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSelectedSetupAlwaysRepairsPresentBDAndChecksPresentPlugin(t *testing.T) {
	inv := guiInventory(StateOK, StateOK, StateOK, StateOK)
	var calls []string
	hooks := firstSetupHooks{
		Detect:               func() Inventory { return inv },
		InstallDiscord:       func(context.Context) error { t.Fatal("healthy Discord reinstalled"); return nil },
		InstallBetterDiscord: func(context.Context) error { calls = append(calls, "BD repair"); return nil },
		InstallPlugin:        func(Inventory) error { calls = append(calls, "DDS enable/check"); return nil },
		InstallCompanion:     func(Inventory) error { t.Fatal("healthy Companion reinstalled"); return nil },
	}
	staged := []stageOutcome{{Component: "betterdiscord", Result: "VERIFIED_CACHED"}}
	r := executeSelectedFirstSetup(context.Background(), inv, staged, hooks, [4]bool{true, true, true, true})
	if r.Status != "SELECTED_FILES_INTEGRATED_FIRST_START_PENDING" || !reflect.DeepEqual(calls, []string{"BD repair", "DDS enable/check"}) {
		t.Fatalf("%+v calls=%v", r, calls)
	}
	hooks.InstallBetterDiscord = func(context.Context) error { return errors.New("official CLI failure") }
	calls = nil
	r = executeSelectedFirstSetup(context.Background(), inv, staged, hooks, [4]bool{true, true, true, true})
	if r.Status != "PARTIAL_OR_BLOCKED" || len(calls) != 0 {
		t.Fatalf("failure continued: %+v %v", r, calls)
	}
}
func TestSelectedDDSCannotBypassRequiredBDRepair(t *testing.T) {
	inv := guiInventory(StateOK, StateOK, StateOK, StateOK)
	for _, choices := range [][4]bool{{false, false, true, false}, {false, false, false, true}} {
		if _, err := selectedSetupPlan(inv, choices); err == nil {
			t.Fatal("DDS setup without required BD repair accepted")
		}
	}
	// Opening inventory and planning never calls a mutating hook.
	if inv.BetterDiscord.State != StateOK {
		t.Fatal("inventory mutated")
	}
}
func TestSelectedPartialBDIsRepairableButPortableIsNotMigrated(t *testing.T) {
	inv := guiInventory(StateOK, StatePartial, StatePartial, StateOK)
	inv.Plugin.Version = "0.5.3"
	p, e := selectedSetupPlan(inv, [4]bool{true, true, true, true})
	if e != nil || !reflect.DeepEqual(p, []string{"betterdiscord", "plugin"}) {
		t.Fatalf("%v %v", p, e)
	}
	inv.Profile = "portable"
	if _, e = selectedSetupPlan(inv, [4]bool{true, true, true, true}); e == nil {
		t.Fatal("portable auto migration")
	}
}
func TestBDRepairStopsBeforeAndAfterCLIAndFailsClosed(t *testing.T) {
	for _, fail := range []string{"", "stop", "install", "verify"} {
		t.Run(fail, func(t *testing.T) {
			running := true
			calls := []string{}
			err := repairBetterDiscord(context.Background(), func() bool { return running }, func(context.Context) error {
				calls = append(calls, "stop")
				if fail == "stop" {
					return errors.New("blocked")
				}
				running = false
				return nil
			}, func() error {
				calls = append(calls, "install")
				if running {
					t.Fatal("install while running")
				}
				running = true
				if fail == "install" {
					return errors.New("CLI failed")
				}
				return nil
			}, func() error {
				calls = append(calls, "verify")
				if running {
					t.Fatal("verify before close")
				}
				if fail == "verify" {
					return errors.New("loader missing")
				}
				return nil
			})
			if (err != nil) != (fail != "") {
				t.Fatal(err)
			}
			expected := []string{"stop", "install", "stop", "verify"}
			if fail == "stop" {
				expected = []string{"stop"}
			}
			if fail == "install" {
				expected = expected[:3]
			}
			if !reflect.DeepEqual(calls, expected) {
				t.Fatalf("%v", calls)
			}
		})
	}
}
func TestBDInjectionFilesAreSeparateFromCoreAndRuntime(t *testing.T) {
	ctx := mkctx(t)
	installDiscord(t, ctx, "1.0.9999")
	installBD(t, ctx)
	discord := detectDiscord(ctx)
	if verifyBetterDiscordInjection(discord, ctx.AppData) == nil {
		t.Fatal("core file mistaken for injection")
	}
	resources := filepath.Join(filepath.Dir(discord.Path), "resources")
	touch(t, filepath.Join(resources, "betterdiscord.app.asar"), "original")
	touch(t, filepath.Join(resources, "app", "package.json"), `{"main":"./index.js"}`)
	touch(t, filepath.Join(resources, "app", "index.js"), `require("BetterDiscord/data/betterdiscord.asar");module.exports=require("../betterdiscord.app.asar");`)
	if err := verifyBetterDiscordInjection(discord, ctx.AppData); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(resources, "app.asar"), "new Discord shadows loader")
	if verifyBetterDiscordInjection(discord, ctx.AppData) == nil {
		t.Fatal("shadowed injection accepted")
	}
}
func TestExistingPluginIdentityAndOtherSettingsPreserved(t *testing.T) {
	ctx := mkctx(t)
	installBD(t, ctx)
	installPlugin(t, ctx, "0.5.3")
	if v, e := installedDDSPluginVersion(ctx.AppData); e != nil || v != "0.5.3" {
		t.Fatal(v, e)
	}
	p := filepath.Join(ctx.AppData, "BetterDiscord", "data", "stable", "plugins.json")
	touch(t, p, `{"DDS":false,"Other":true,"Disabled":false}`)
	theme := filepath.Join(ctx.AppData, "BetterDiscord", "themes", "my.theme.css")
	touch(t, theme, "theme")
	settings := filepath.Join(ctx.AppData, "BetterDiscord", "data", "stable", "settings.json")
	touch(t, settings, "settings")
	if err := enableNewDDSPlugin(ctx.AppData); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"Other": true`) || !strings.Contains(string(b), `"Disabled": false`) {
		t.Fatal(string(b))
	}
	for p, want := range map[string]string{theme: "theme", settings: "settings"} {
		b, _ := os.ReadFile(p)
		if string(b) != want {
			t.Fatal("other data changed")
		}
	}
	touch(t, filepath.Join(ctx.AppData, "BetterDiscord", "plugins", "DDS.plugin.js"), "/**\n * @name Other\n * @version 0.5.3\n */")
	if _, e := installedDDSPluginVersion(ctx.AppData); e == nil {
		t.Fatal("wrong plugin accepted")
	}
}

func testPluginFixture(t *testing.T) (Inventory, string, string, string, string) {
	t.Helper()
	base := t.TempDir()
	appData := filepath.Join(base, "Roaming")
	bd := filepath.Join(appData, "BetterDiscord")
	for _, p := range []string{filepath.Join(bd, "data"), filepath.Join(bd, "plugins")} {
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	stage := filepath.Join(base, "Temp", "DDS_Installer_Stage")
	d := filepath.Join(stage, "plugin", "v0.5.3")
	for _, p := range []string{filepath.Join(d, "package"), filepath.Join(d, "payload")} {
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	content := []byte("/**\n * @version 0.5.3\n */\nmodule.exports={}\n")
	sha := h(content)
	rec := preparedExternal{Schema: "dds-installer-external-stage-v1", Component: "DDS Plugin", Version: "0.5.3", Source: pluginPrefix + "v0.5.3/DDS.plugin.js", SHA256: sha, PayloadSHA256: sha, ZIPSize: int64(len(content)), Status: "VERIFIED_AND_STAGED"}
	receipt, _ := json.Marshal(rec)
	for p, b := range map[string][]byte{filepath.Join(d, "stage_receipt.json"): receipt, filepath.Join(d, "package", "DDS.plugin.js"): content, filepath.Join(d, "payload", "DDS.plugin.js"): content} {
		if e := os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	inv := Inventory{Discord: Component{State: StateOK}, BetterDiscord: Component{State: StateOK}, Plugin: Component{State: StateMissing}}
	target := filepath.Join(bd, "plugins", "DDS.plugin.js")
	dataRoot := filepath.Join(base, "DataRoot")
	if e := os.MkdirAll(dataRoot, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dataRoot, "sentinel.sqlite"), []byte("NEVER CHANGE"), 0600); e != nil {
		t.Fatal(e)
	}
	return inv, stage, appData, target, dataRoot
}
func assertDataUnchanged(t *testing.T, dataRoot string) {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(dataRoot, "sentinel.sqlite"))
	if e != nil || string(b) != "NEVER CHANGE" {
		t.Fatalf("DataRoot touched: %q %v", b, e)
	}
}
func TestPluginApplyMissingAndValidate(t *testing.T) {
	inv, stage, appData, target, dataRoot := testPluginFixture(t)
	r, e := applyPlugin(inv, stage, appData)
	if e != nil || r.Status != "APPLIED_AND_VALIDATED" || r.Backup != "" {
		t.Fatalf("%+v %v", r, e)
	}
	if b, e := os.ReadFile(target); e != nil || !strings.Contains(string(b), "0.5.3") {
		t.Fatalf("missing payload: %s %v", b, e)
	}
	assertDataUnchanged(t, dataRoot)
}
func TestPluginApplyAlreadyCurrent(t *testing.T) {
	inv, stage, appData, target, dataRoot := testPluginFixture(t)
	a, e := applyPlugin(inv, stage, appData)
	if e != nil {
		t.Fatal(e)
	}
	stBefore, e := os.Stat(target)
	if e != nil {
		t.Fatal(e)
	}
	b, e := applyPlugin(inv, stage, appData)
	if e != nil || b.Status != "ALREADY_CURRENT" || b.Backup != "" {
		t.Fatalf("%+v %v", b, e)
	}
	stAfter, _ := os.Stat(target)
	if !stBefore.ModTime().Equal(stAfter.ModTime()) {
		t.Fatal("unnecessary rewrite")
	}
	assertDataUnchanged(t, dataRoot)
	_ = a
}
func TestPluginApplyBackupOnReplacement(t *testing.T) {
	inv, stage, appData, target, dataRoot := testPluginFixture(t)
	old := []byte("/**\n * @version 0.5.2\n */\nOLD\n")
	if e := os.WriteFile(target, old, 0600); e != nil {
		t.Fatal(e)
	}
	r, e := applyPlugin(inv, stage, appData)
	if e != nil || r.Status != "APPLIED_AND_VALIDATED" || r.Backup == "" {
		t.Fatalf("%+v %v", r, e)
	}
	bak, e := os.ReadFile(r.Backup)
	if e != nil || string(bak) != string(old) {
		t.Fatalf("backup failure: %s %v", bak, e)
	}
	assertDataUnchanged(t, dataRoot)
}
func TestPluginApplyRollbackAfterValidationFailure(t *testing.T) {
	inv, stage, appData, target, dataRoot := testPluginFixture(t)
	old := []byte("/**\n * @version 0.5.2\n */\nOLD\n")
	if e := os.WriteFile(target, old, 0600); e != nil {
		t.Fatal(e)
	}
	_, e := applyPluginWithValidation(inv, stage, appData, func(string, string) error { return os.ErrPermission })
	if e == nil {
		t.Fatal("expected failure")
	}
	got, e := os.ReadFile(target)
	if e != nil || string(got) != string(old) {
		t.Fatalf("rollback not restored: %q %v", got, e)
	}
	assertDataUnchanged(t, dataRoot)
}
func TestPluginApplyRejectTamperedStage(t *testing.T) {
	inv, stage, appData, target, dataRoot := testPluginFixture(t)
	e := os.WriteFile(filepath.Join(stage, "plugin", "v0.5.3", "payload", "DDS.plugin.js"), []byte("TAMPERED"), 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, e = applyPlugin(inv, stage, appData)
	if e == nil {
		t.Fatal("must reject tampered stage")
	}
	if _, e = os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("should not create plugin")
	}
	assertDataUnchanged(t, dataRoot)
}
func TestPluginApplyRejectNewerInstalledVersion(t *testing.T) {
	inv, stage, appData, target, _ := testPluginFixture(t)
	if e := os.WriteFile(target, []byte("/**\n * @version 0.6.0\n */\nNEW\n"), 0600); e != nil {
		t.Fatal(e)
	}
	_, e := applyPlugin(inv, stage, appData)
	if e == nil || !strings.Contains(e.Error(), "откат") {
		t.Fatalf("downgrade permitted: %v", e)
	}
}
func TestPluginApplyRejectUnavailableDependency(t *testing.T) {
	inv, stage, appData, target, _ := testPluginFixture(t)
	inv.BetterDiscord.State = StateMissing
	_, e := applyPlugin(inv, stage, appData)
	if e == nil {
		t.Fatal("must require BD")
	}
	if _, e = os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("should not install")
	}
}
func TestPluginApplyRejectSymlinkTarget(t *testing.T) {
	inv, stage, appData, target, _ := testPluginFixture(t)
	external := filepath.Join(t.TempDir(), "remote.js")
	if e := os.WriteFile(external, []byte("safe"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(external, target); e != nil {
		t.Skip(e)
	}
	_, e := applyPlugin(inv, stage, appData)
	if e == nil {
		t.Fatal("symlink accepted")
	}
	b, _ := os.ReadFile(external)
	if string(b) != "safe" {
		t.Fatal("symlink target touched")
	}
}
func TestPluginApplyMenuExplicitConfirmation(t *testing.T) {
	inv, stage, appData, target, _ := testPluginFixture(t)
	var out strings.Builder
	runPluginApplyMenu(inv, strings.NewReader("NO\n"), &out, stage, appData)
	if _, e := os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("prompt bypassed")
	}
	if !strings.Contains(out.String(), "Отменено") {
		t.Fatal(out.String())
	}
}
func TestPluginApplyNoStaging(t *testing.T) {
	inv, stage, appData, _, _ := testPluginFixture(t)
	if e := os.RemoveAll(stage); e != nil {
		t.Fatal(e)
	}
	_, e := applyPlugin(inv, stage, appData)
	if e == nil {
		t.Fatal("missing stage accepted")
	}
}

func TestPluginApplyMenuConfirmed(t *testing.T) {
	inv, stage, appData, target, dataRoot := testPluginFixture(t)
	var out strings.Builder
	runPluginApplyMenu(inv, strings.NewReader("1\n"), &out, stage, appData)
	if !strings.Contains(out.String(), "APPLIED_AND_VALIDATED") {
		t.Fatal(out.String())
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal(err)
	}
	assertDataUnchanged(t, dataRoot)
}

// Regression: the fixed .failed-apply pathname may pre-exist. It must neither
// be overwritten nor prevent recovery of the original plugin on Windows.
func TestPluginRollbackWithExistingFailureMarker(t *testing.T) {
	inv, stage, appData, target, dataRoot := testPluginFixture(t)
	old := []byte("/**\n * @version 0.5.2\n */\nORIGINAL\n")
	if err := os.WriteFile(target, old, 0600); err != nil {
		t.Fatal(err)
	}
	marker := target + ".failed-apply"
	if err := os.WriteFile(marker, []byte("UNRELATED_FILE"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := applyPluginWithValidation(inv, stage, appData, func(string, string) error { return os.ErrPermission })
	if err == nil || !strings.Contains(err.Error(), "восстановлен") {
		t.Fatalf("must fail and restore: %v", err)
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil || string(got) != string(old) {
		t.Fatalf("previous plugin not restored: %q %v", got, readErr)
	}
	got, readErr = os.ReadFile(marker)
	if readErr != nil || string(got) != "UNRELATED_FILE" {
		t.Fatalf("foreign file overwritten: %q %v", got, readErr)
	}
	residue, err := filepath.Glob(filepath.Join(filepath.Dir(target), ".dds-plugin-failed-*"))
	if err != nil || len(residue) > 0 {
		t.Fatalf("rollback residue %v: %v", residue, err)
	}
	assertDataUnchanged(t, dataRoot)
}

func TestPluginFreshRollbackWithExistingFailureMarker(t *testing.T) {
	inv, stage, appData, target, dataRoot := testPluginFixture(t)
	marker := target + ".failed-apply"
	if err := os.WriteFile(marker, []byte("UNRELATED_FILE"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := applyPluginWithValidation(inv, stage, appData, func(string, string) error { return os.ErrPermission })
	if err == nil || !strings.Contains(err.Error(), "не установлен") {
		t.Fatalf("fresh failure not reported: %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("failed candidate left installed: %v", err)
	}
	got, readErr := os.ReadFile(marker)
	if readErr != nil || string(got) != "UNRELATED_FILE" {
		t.Fatalf("foreign file changed: %q %v", got, readErr)
	}
	assertDataUnchanged(t, dataRoot)
}

func TestPluginStageSymlinkedPayloadDirectoryIsRejected(t *testing.T) {
	inv, stage, appData, target, _ := testPluginFixture(t)
	dir := filepath.Join(stage, "plugin", "v0.5.3", "payload")
	safe := filepath.Join(t.TempDir(), "safe")
	if err := os.Rename(dir, safe); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(safe, dir); err != nil {
		t.Skip(err)
	}
	if _, err := applyPlugin(inv, stage, appData); err == nil {
		t.Fatal("stage symlink accepted")
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("unexpected plugin: %v", err)
	}
}

func TestPluginSymlinkedAppDataRejected(t *testing.T) {
	inv, stage, appData, target, _ := testPluginFixture(t)
	parent := filepath.Dir(appData)
	alias := filepath.Join(parent, "RoamingAlias")
	if err := os.Symlink(appData, alias); err != nil {
		t.Skip(err)
	}
	if _, err := applyPlugin(inv, stage, alias); err == nil {
		t.Fatal("app data symlink accepted")
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("unexpected write: %v", err)
	}
}
