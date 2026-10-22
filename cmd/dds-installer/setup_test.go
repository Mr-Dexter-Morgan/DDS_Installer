package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
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

func TestFinishChoicesWaitForParentAndStayIndependent(t *testing.T) {
	for _, d := range []bool{false, true} {
		for _, c := range []bool{false, true} {
			for _, exited := range []bool{false, true} {
				waited, ds, cs := false, 0, 0
				h := finishHooks{WaitParent: func() bool { waited = true; return exited }, DiscordRunning: func() bool { return false }, CompanionRunning: func() bool { return false }, StartDiscord: func() error {
					if !waited || !exited {
						t.Fatal("early Discord")
					}
					ds++
					return nil
				}, StartCompanion: func() error {
					if !waited || !exited {
						t.Fatal("early Companion")
					}
					cs++
					return nil
				}}
				finishHandoff(d, c, h)
				if (ds == 1) != (d && exited) || (cs == 1) != (c && exited) {
					t.Fatalf("choices %v/%v, parent %v: %d/%d", d, c, exited, ds, cs)
				}
			}
		}
	}
}

func TestHeartbeatMustBelongToCurrentLaunch(t *testing.T) {
	app, _ := mockNewBD(t)
	p := filepath.Join(app, "BetterDiscord", "DDS_Data", "plugin_heartbeat.json")
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	start := now.Add(-5 * time.Second)
	for _, tc := range []struct {
		name, state, version string
		at                   time.Time
		want                 bool
	}{
		{"before launch but only 10 seconds old", "RUNNING", "0.5.3", now.Add(-10 * time.Second), false},
		{"current launch", "RUNNING", "0.5.3", now.Add(-time.Second), true},
		{"stopped", "STOPPED", "0.5.3", now, false},
		{"wrong version", "RUNNING", "0.5.2", now, false},
		{"future", "RUNNING", "0.5.3", now.Add(time.Second), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(map[string]string{"state": tc.state, "pluginVersion": tc.version, "updatedAt": tc.at.Format(time.RFC3339Nano)})
			if e := os.WriteFile(p, b, 0600); e != nil {
				t.Fatal(e)
			}
			if got := freshPluginHeartbeatSince(app, "0.5.3", start, now); got != tc.want {
				t.Fatalf("%v", got)
			}
		})
	}
}
func goodFinishHooks() finishRuntimeHooks {
	return finishRuntimeHooks{DiscordRunning: func() bool { return true }, CompanionRunning: func() bool { return true }, Injection: func() error { return nil }, Heartbeat: func() bool { return true }, Archive: func() bool { return true }}
}
func TestFinishRuntimeChoiceIndependenceAndNoUncheckedLaunchError(t *testing.T) {
	h := goodFinishHooks()
	h.DiscordRunning = func() bool { t.Fatal("unchecked Discord probed"); return false }
	h.Injection = func() error { t.Fatal("unchecked BD"); return nil }
	h.Heartbeat = func() bool { t.Fatal("unchecked heartbeat"); return false }
	r := checkFinishRuntime(context.Background(), false, true, h)
	if len(r.Problems) != 0 || !strings.Contains(r.Plugin, "НЕ ПРОВЕРЯЛОСЬ") || !strings.HasPrefix(r.Companion, "ЗАПУЩЕНО") {
		t.Fatalf("%+v", r)
	}
	r = checkFinishRuntime(context.Background(), false, false, finishRuntimeHooks{})
	if len(r.Problems) != 0 {
		t.Fatal(r)
	}
}
func TestFinishRuntimeNeverAcceptsProcessOrStaticFilesAsPluginHealth(t *testing.T) {
	h := goodFinishHooks()
	h.Heartbeat = func() bool { return false }
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	r := checkFinishRuntime(ctx, true, false, h)
	if len(r.Problems) == 0 || strings.Contains(r.Plugin, "РАБОТАЕТ") {
		t.Fatal(r)
	}
	h = goodFinishHooks()
	h.Injection = func() error { return errors.New("loader absent") }
	r = checkFinishRuntime(context.Background(), true, false, h)
	if len(r.Problems) == 0 || strings.Contains(r.Plugin, "РАБОТАЕТ") {
		t.Fatal(r)
	}
	r = checkFinishRuntime(context.Background(), true, true, goodFinishHooks())
	if len(r.Problems) != 0 || !strings.Contains(r.Plugin, "РАБОТАЕТ") || !strings.Contains(r.BetterDiscord, "РАБОТАЕТ") {
		t.Fatal(r)
	}
	if strings.Contains(r.Text(), "RUNTIME_READY") {
		t.Fatal("unproven Companion consumption promoted")
	}
}

func guiInventory(ds, bd, pl, cp State) Inventory {
	return Inventory{Discord: Component{Name: "Discord", State: ds}, BetterDiscord: Component{Name: "BetterDiscord", State: bd}, Plugin: Component{Name: "DDS Plugin", State: pl}, Companion: Component{Name: "DDS Companion", State: cp}, Profile: "installed"}
}
func TestGUISelectionRequiresDependency(t *testing.T) {
	i := guiInventory(StateMissing, StateMissing, StateMissing, StateMissing)
	for _, tc := range []struct {
		choices [4]bool
		substr  string
	}{
		{[4]bool{false, true, false, false}, "Discord"},
		{[4]bool{true, false, true, false}, "BetterDiscord"},
		{[4]bool{false, false, false, false}, "хотя бы один"},
	} {
		_, e := selectedSetupPlan(i, tc.choices)
		if e == nil || !strings.Contains(e.Error(), tc.substr) {
			t.Errorf("selection %v: expected %s, got %v", tc.choices, tc.substr, e)
		}
	}
}
func TestGUISelectionSkipsUserUncheckedComponents(t *testing.T) {
	i := guiInventory(StateMissing, StateMissing, StateMissing, StateMissing)
	got, e := selectedSetupPlan(i, [4]bool{true, false, false, false})
	if e != nil || len(got) != 1 || got[0] != "discord" {
		t.Fatalf("got %v, %v", got, e)
	}
}
func TestGUISelectionHealthyRequiresBDRepairStage(t *testing.T) {
	i := guiInventory(StateOK, StateOK, StateOK, StateOK)
	choices := [4]bool{true, true, true, true}
	plan, e := selectedSetupPlan(i, choices)
	if e != nil || len(plan) != 1 || plan[0] != "betterdiscord" {
		t.Fatalf("plan %v %v", plan, e)
	}
	report := executeSelectedFirstSetup(context.Background(), i, nil, firstSetupHooks{}, choices)
	if report.Status != "BLOCKED" {
		t.Fatalf("repair without verified CLI: %s", report.Status)
	}
}
func TestGUISelectionDoesNotPretendSkippedMissingIsInstalled(t *testing.T) {
	i := guiInventory(StateMissing, StateMissing, StateMissing, StateMissing)
	report := executeSelectedFirstSetup(context.Background(), i, nil, firstSetupHooks{}, [4]bool{false, false, false, true})
	if report.Status == "FILES_INTEGRATED_FIRST_START_PENDING" || report.Status == "SELECTED_FILES_INTEGRATED_FIRST_START_PENDING" {
		t.Fatal("must not report successful install without staged companion")
	}
}

func TestCustomProgramRootPreservesDataAndVersionLayout(t *testing.T) {
	inv, stage, base, dr := freshFixture(t)
	target := filepath.Join(base, "Моя программа DDS")
	got, err := freshCompanionInstallAt(inv, stage, target, func() error { return nil }, nil)
	if err != nil || got.Root != target {
		t.Fatalf("%+v %v", got, err)
	}
	if err := validateCompanionPackage(target, "0.8.5"); err != nil {
		t.Fatal(err)
	}
	assertPreviewReadOnly(t, dr)
	if exists(filepath.Join(target, "Data")) {
		t.Fatal("data moved into program directory")
	}
}

func TestPermissionFailureDoesNotPublishOrDamageArchive(t *testing.T) {
	inv, stage, base, dr := freshFixture(t)
	target := filepath.Join(base, "DDS")
	_, err := freshCompanionInstallAt(inv, stage, target, func() error { return nil }, func(string) error { return errors.New("ACL denied") })
	if err == nil {
		t.Fatal("permission error ignored")
	}
	if _, e := os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("failed root published")
	}
	assertNoFreshScratch(t, base)
	assertPreviewReadOnly(t, dr)
}

func TestProgramTargetRejectsDataOverlapAndRelativePath(t *testing.T) {
	base := t.TempDir()
	data := filepath.Join(base, "Archive")
	_ = os.Mkdir(data, 0700)
	for _, target := range []string{"relative", data, filepath.Join(data, "DDS"), base} {
		if validateProgramTarget(target, data) == nil {
			t.Fatalf("unsafe target %s accepted", target)
		}
	}
}

func TestExistingInstallLocationIsPreservedAndCustomRootRediscovered(t *testing.T) {
	inv, stage, base, _ := freshFixture(t)
	target := filepath.Join(base, "CustomDDS")
	_, err := freshCompanionInstallAt(inv, stage, target, func() error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	local := t.TempDir()
	if err := saveOwnership(local, installerOwnership{Schema: "dds-installer-ownership-v1", Root: target, Version: "0.9.0-gui-r2"}); err != nil {
		t.Fatal(err)
	}
	detected, _ := detectCompanion(DetectContext{LocalAppData: local})
	if detected.State != StateOK || detected.Path != target {
		t.Fatalf("custom install lost: %+v", detected)
	}
	if got := preferredInstallRoot(Inventory{Companion: detected}); got != target {
		t.Fatal("existing root silently changed")
	}
}

func TestNumericConfirmationOnlyExactOne(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  bool
	}{
		{"1\n", true}, {" 1 \r\n", true},
		{"0\n", false}, {"\n", false}, {"2\n", false},
		{"01\n", false}, {"1 2\n", false}, {"yes\n", false},
		{"УСТАНОВИТЬ\n", false}, {"ПОДГОТОВИТЬ\n", false},
		{"ВОССТАНОВИТЬ\n", false}, {"ПРОДОЛЖИТЬ\n", false},
		{"1", false}, {"", false},
	} {
		t.Run(strings.ReplaceAll(tt.input, "\n", "-newline-"), func(t *testing.T) {
			var output strings.Builder
			got := confirmNumeric(strings.NewReader(tt.input), &output, "Тестовое действие?")
			if got != tt.want {
				t.Fatalf("input %q: got %t want %t", tt.input, got, tt.want)
			}
			if !strings.Contains(output.String(), "1 — Подтвердить") || !strings.Contains(output.String(), "0 — Отмена") {
				t.Fatalf("not a numeric prompt: %q", output.String())
			}
		})
	}
}

func TestNumericConfirmPreservesBufferedMenuLine(t *testing.T) {
	b := bufio.NewReader(strings.NewReader("8\n1\n0\n"))
	if selectStage(b, io.Discard) != "8" {
		t.Fatal("menu selection missing")
	}
	if !confirmNumeric(b, io.Discard, "Подготовить?") {
		t.Fatal("expected yes")
	}
	if confirmNumeric(b, io.Discard, "Установить?") {
		t.Fatal("expected explicit cancellation")
	}
}

func cleanSetupInventory() Inventory {
	return Inventory{
		Discord:       Component{Name: "Discord", State: StateMissing},
		BetterDiscord: Component{Name: "BetterDiscord", State: StateBlocked},
		Plugin:        Component{Name: "DDS Plugin", State: StateBlocked},
		Companion:     Component{Name: "DDS Companion", State: StateNotDetected},
		DataRoot:      Component{Name: "DataRoot", State: StateOK, Path: "protected-existing-data"},
		Profile:       "unknown",
	}
}
func fullSetupStaged() []stageOutcome {
	out := []stageOutcome{}
	for _, key := range firstSetupOrder {
		out = append(out, stageOutcome{Component: key, Result: "VERIFIED_AND_STAGED", Version: "test"})
	}
	return out
}
func simulatedSetup(i *Inventory, attempts *[]string) firstSetupHooks {
	return firstSetupHooks{
		Detect: func() Inventory { return *i },
		InstallDiscord: func(context.Context) error {
			*attempts = append(*attempts, "discord")
			i.Discord.State = StateOK
			i.BetterDiscord.State = StateMissing
			return nil
		},
		InstallBetterDiscord: func(context.Context) error {
			*attempts = append(*attempts, "betterdiscord")
			i.BetterDiscord.State = StateOK
			i.Plugin.State = StateMissing
			return nil
		},
		InstallPlugin: func(Inventory) error { *attempts = append(*attempts, "plugin"); i.Plugin.State = StateOK; return nil },
		InstallCompanion: func(Inventory) error {
			*attempts = append(*attempts, "companion")
			i.Companion.State = StateOK
			return nil
		},
	}
}
func TestSetupIntegrationFreshFourFloors(t *testing.T) {
	i := cleanSetupInventory()
	before := i.DataRoot
	var calls []string
	r := executeFirstSetup(context.Background(), i, fullSetupStaged(), simulatedSetup(&i, &calls))
	if r.Status != "FILES_INTEGRATED_FIRST_START_PENDING" || r.CompanionFirstStartVerified || !reflect.DeepEqual(calls, firstSetupOrder) || i.DataRoot != before {
		t.Fatalf("bad report %+v steps %+v dataroot %+v", r, calls, i.DataRoot)
	}
	for _, s := range r.Steps {
		if s.Result != "FILES_VALIDATED" {
			t.Fatalf("not postverified: %+v", s)
		}
	}
	// A second run does not call *any* installer or download action.
	calls = nil
	next, err := planFirstSetup(i)
	if err != nil || len(next) != 0 {
		t.Fatalf("healthy plan %v %v", next, err)
	}
	r = executeFirstSetup(context.Background(), i, nil, simulatedSetup(&i, &calls))
	if r.Status != "ALREADY_HEALTHY_NO_CHANGE" || len(calls) != 0 {
		t.Fatalf("healthy rewrite %+v %v", r, calls)
	}
}
func TestSetupIntegrationAllPackageStagesRequiredBeforeAnyWrite(t *testing.T) {
	for _, name := range firstSetupOrder {
		t.Run(name, func(t *testing.T) {
			i := cleanSetupInventory()
			var calls []string
			stages := fullSetupStaged()
			for idx := range stages {
				if stages[idx].Component == name {
					stages[idx].Result = "FAILED"
					stages[idx].Reason = "offline"
				}
			}
			r := executeFirstSetup(context.Background(), i, stages, simulatedSetup(&i, &calls))
			if r.Status != "BLOCKED" || len(calls) != 0 {
				t.Fatalf("stage failure applied %+v %v", r, calls)
			}
		})
	}
	i := cleanSetupInventory()
	var calls []string
	wrong := fullSetupStaged()
	wrong[0], wrong[1] = wrong[1], wrong[0]
	r := executeFirstSetup(context.Background(), i, wrong, simulatedSetup(&i, &calls))
	if r.Status != "BLOCKED" || len(calls) != 0 {
		t.Fatalf("wrong order applied %+v", r)
	}
}
func TestSetupIntegrationStopsOnPostValidationFailure(t *testing.T) {
	for _, name := range []string{"discord", "betterdiscord", "plugin", "companion"} {
		t.Run(name, func(t *testing.T) {
			i := cleanSetupInventory()
			var calls []string
			ops := simulatedSetup(&i, &calls)
			switch name {
			case "discord":
				ops.InstallDiscord = func(context.Context) error { calls = append(calls, "discord"); return nil }
			case "betterdiscord":
				ops.InstallBetterDiscord = func(context.Context) error { calls = append(calls, "betterdiscord"); return nil }
			case "plugin":
				ops.InstallPlugin = func(Inventory) error { calls = append(calls, "plugin"); return nil }
			case "companion":
				ops.InstallCompanion = func(Inventory) error { calls = append(calls, "companion"); return nil }
			}
			r := executeFirstSetup(context.Background(), i, fullSetupStaged(), ops)
			if r.Status != "PARTIAL_OR_BLOCKED" || calls[len(calls)-1] != name {
				t.Fatalf("wrong failure %+v %v", r, calls)
			}
			for _, s := range r.Steps {
				if s.Component == name && s.Result != "FAILED" {
					t.Fatalf("false success %+v", s)
				}
			}
		})
	}
}
func TestSetupIntegrationNoLaterWriteAfterError(t *testing.T) {
	i := cleanSetupInventory()
	var calls []string
	ops := simulatedSetup(&i, &calls)
	ops.InstallBetterDiscord = func(context.Context) error {
		calls = append(calls, "betterdiscord")
		return errors.New("CLI install failed")
	}
	r := executeFirstSetup(context.Background(), i, fullSetupStaged(), ops)
	if r.Status != "PARTIAL_OR_BLOCKED" || len(calls) != 2 || calls[1] != "betterdiscord" {
		t.Fatalf("dependents modified %+v %v", r, calls)
	}
}
func TestSetupIntegrationNoRepairForPartialPortable(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Inventory)
	}{
		{"discord partial", func(i *Inventory) { i.Discord.State = StatePartial }},
		{"BD partial", func(i *Inventory) { i.BetterDiscord.State = StatePartial }},
		{"plugin partial", func(i *Inventory) { i.Plugin.State = StatePartial }},
		{"companion partial", func(i *Inventory) { i.Companion.State = StatePartial }},
		{"portable", func(i *Inventory) { i.Profile = "portable" }},
		{"portable uncertain", func(i *Inventory) { i.Profile = "portable?" }},
		{"discord unknown", func(i *Inventory) { i.Discord.State = StateNotDetected }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i := cleanSetupInventory()
			tc.change(&i)
			var calls []string
			_, err := planFirstSetup(i)
			if err == nil {
				t.Fatal("ambiguous state accepted")
			}
			r := executeFirstSetup(context.Background(), i, fullSetupStaged(), simulatedSetup(&i, &calls))
			if r.Status != "BLOCKED" || len(calls) != 0 {
				t.Fatalf("unsafe apply %+v %v", r, calls)
			}
		})
	}
}
func TestSetupIntegrationResumeOnlyRemainingFloors(t *testing.T) {
	i := cleanSetupInventory()
	i.Discord.State = StateOK
	i.BetterDiscord.State = StateOK
	i.Plugin.State = StateMissing
	var calls []string
	prepared := fullSetupStaged()[2:]
	r := executeFirstSetup(context.Background(), i, prepared, simulatedSetup(&i, &calls))
	if r.Status != "FILES_INTEGRATED_FIRST_START_PENDING" || !reflect.DeepEqual(calls, []string{"plugin", "companion"}) {
		t.Fatalf("resume result %+v calls=%v", r, calls)
	}
}
func TestSetupIntegrationCancelledAndMissingHook(t *testing.T) {
	i := cleanSetupInventory()
	var calls []string
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := executeFirstSetup(ctx, i, fullSetupStaged(), simulatedSetup(&i, &calls))
	if r.Status != "BLOCKED" || len(calls) != 0 {
		t.Fatalf("cancel ignored %+v", r)
	}
	ops := simulatedSetup(&i, &calls)
	ops.InstallPlugin = nil
	r = executeFirstSetup(context.Background(), i, fullSetupStaged(), ops)
	if r.Status != "BLOCKED" || len(calls) != 0 {
		t.Fatalf("missing hook ignored %+v", r)
	}
}
func TestSetupIntegrationPlanMismatchBlocks(t *testing.T) {
	i := cleanSetupInventory()
	i.Discord.State = StateOK
	i.BetterDiscord.State = StateOK
	i.Plugin.State = StateBlocked
	if _, err := planFirstSetup(i); err == nil {
		t.Fatal("inconsistent plugin state accepted")
	}
	i.Plugin.State = StateOK
	i.BetterDiscord.State = StateBlocked
	if _, err := planFirstSetup(i); err == nil {
		t.Fatal("inconsistent BD state accepted")
	}
}
func TestSetupIntegrationExternalExecutionGuard(t *testing.T) {
	if err := externalRun(context.Background(), "nonexistent.exe"); err == nil || runtime.GOOS != "windows" && !strings.Contains(err.Error(), "Windows") || runtime.GOOS == "windows" && !os.IsNotExist(err) {
		t.Fatalf("sandbox attempted execution: %v", err)
	}
}

func TestSetupBufferedMenuInputNotLost(t *testing.T) {
	// selectStage must not prefetch the next typed confirmation.
	b := bufio.NewReader(strings.NewReader("8\n1\n1\n"))
	var out bytes.Buffer
	if choice := selectStage(b, &out); choice != "8" {
		t.Fatalf("wrong choice %q", choice)
	}
	s, _ := sharedBufferedReader(b).ReadString('\n')
	if strings.TrimSpace(s) != "1" {
		t.Fatalf("next answer lost %q", s)
	}
	s, _ = sharedBufferedReader(b).ReadString('\n')
	if strings.TrimSpace(s) != "1" {
		t.Fatalf("approval lost %q", s)
	}
}
func TestSetupWaitsForActualInstallState(t *testing.T) {
	attempts := 0
	if err := awaitSetupComponent(context.Background(), 100*time.Millisecond, func() bool { attempts++; return attempts == 1 }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := awaitSetupComponent(ctx, time.Minute, func() bool { return false }); err == nil {
		t.Fatal("cancel ignored")
	}
}
func TestSetupExternalLogBounded(t *testing.T) {
	b := &boundedOutput{}
	sample := strings.Repeat("a", 10000)
	n, e := b.Write([]byte(sample))
	if e != nil || n != len(sample) || len(b.last) != 4096 {
		t.Fatalf("output cap: %v %v %d", e, n, len(b.last))
	}
	b.Write([]byte("tail"))
	if len(b.last) != 4096 || !strings.HasSuffix(string(b.last), "tail") {
		t.Fatal("last output not retained")
	}
}

func TestSetupExactPluginApprovedStage(t *testing.T) {
	_, stage, _, _, dataRoot := testPluginFixture(t)
	rec, payload, e := verifiedPluginStage(stage)
	if e != nil {
		t.Fatal(e)
	}
	expected := stageOutcome{Component: "plugin", Result: "VERIFIED_AND_STAGED", Version: rec.Version, Location: filepath.Dir(filepath.Dir(payload)), SHA256: rec.SHA256}
	if e = verifyExactSetupStage(stage, expected); e != nil {
		t.Fatal(e)
	}
	mismatch := expected
	mismatch.SHA256 = strings.Repeat("f", 64)
	if e = verifyExactSetupStage(stage, mismatch); e == nil {
		t.Fatal("unapproved hash accepted")
	}
	if e = os.WriteFile(payload, []byte("TAMPERED"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = verifyExactSetupStage(stage, expected); e == nil {
		t.Fatal("tampered stage accepted")
	}
	assertDataUnchanged(t, dataRoot)
}

func TestSetupDiscordClosesAutomatically(t *testing.T) {
	running := true
	calls := 0
	err := ensureDiscordStopped(context.Background(), func() bool { return running }, func(context.Context) error { calls++; running = false; return nil })
	if err != nil || calls != 1 {
		t.Fatalf("auto stop %v calls=%d", err, calls)
	}
	calls = 0
	err = ensureDiscordStopped(context.Background(), func() bool { return false }, func(context.Context) error { calls++; return nil })
	if err != nil || calls != 0 {
		t.Fatalf("already closed: %v %d", err, calls)
	}
	err = ensureDiscordStopped(context.Background(), func() bool { return true }, func(context.Context) error { return errors.New("failed") })
	if err == nil {
		t.Fatal("failed shutdown accepted")
	}
}

func mockNewBD(t *testing.T) (string, string) {
	t.Helper()
	app := filepath.Join(t.TempDir(), "Roaming")
	bd := filepath.Join(app, "BetterDiscord")
	for _, d := range []string{filepath.Join(bd, "data", "stable"), filepath.Join(bd, "plugins")} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bd, "plugins", "DDS.plugin.js"), []byte("DDS"), 0600); err != nil {
		t.Fatal(err)
	}
	return app, filepath.Join(bd, "data", "stable", "plugins.json")
}
func TestDDSPluginEnableKeepsOtherPlugins(t *testing.T) {
	app, file := mockNewBD(t)
	before := []byte(`{"OtherPlugin":false,"SecondPlugin":true,"nested":{"value":2}}`)
	if e := os.WriteFile(file, before, 0600); e != nil {
		t.Fatal(e)
	}
	if enabled, err := ddsPluginEnabled(app); err != nil || enabled {
		t.Fatalf("unexpected DDS state: %v %v", enabled, err)
	}
	if e := enableNewDDSPlugin(app); e != nil {
		t.Fatal(e)
	}
	if enabled, err := ddsPluginEnabled(app); err != nil || !enabled {
		t.Fatalf("DDS activation not detected: %v %v", enabled, err)
	}
	b, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	var obj map[string]interface{}
	if json.Unmarshal(b, &obj) != nil || obj["DDS"] != true || obj["OtherPlugin"] != false || obj["SecondPlugin"] != true {
		t.Fatalf("corrupt settings: %s", b)
	}
	if e = enableNewDDSPlugin(app); e != nil {
		t.Fatal(e)
	}
	b2, _ := os.ReadFile(file)
	if string(b) != string(b2) {
		t.Fatal("no-op rewrote settings")
	}
}
func TestDDSPluginEnableWithoutPreexistingSettings(t *testing.T) {
	app, file := mockNewBD(t)
	if e := enableNewDDSPlugin(app); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(file)
	if !strings.Contains(string(b), `"DDS": true`) {
		t.Fatalf("not enabled: %s", b)
	}
}
func TestDDSPluginEnableOnlyOverridesDDS(t *testing.T) {
	app, file := mockNewBD(t)
	old := []byte(`{"DDS":false,"OtherPlugin":true}`)
	if e := os.WriteFile(file, old, 0600); e != nil {
		t.Fatal(e)
	}
	if e := enableNewDDSPlugin(app); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(file)
	var settings map[string]bool
	if json.Unmarshal(after, &settings) != nil || settings["DDS"] != true || settings["OtherPlugin"] != true {
		t.Fatalf("other plugin changed: %s", after)
	}
}
func TestDDSPluginEnableRejectsBrokenSettings(t *testing.T) {
	app, file := mockNewBD(t)
	bad := []byte(`[]`)
	if e := os.WriteFile(file, bad, 0600); e != nil {
		t.Fatal(e)
	}
	if e := enableNewDDSPlugin(app); e == nil {
		t.Fatal("accepted non-object JSON")
	}
	after, _ := os.ReadFile(file)
	if string(after) != string(bad) {
		t.Fatal("mutated invalid file")
	}
}
func TestFirstStartChecksAllSteps(t *testing.T) {
	d, c, h, a := false, false, false, false
	var calls []string
	hook := startupHooks{
		StartDiscord:   func() error { calls = append(calls, "discord"); d = true; return nil },
		StartCompanion: func() error { calls = append(calls, "companion"); c = true; return nil },
		DiscordRunning: func() bool { return d }, CompanionRunning: func() bool { return c },
		PluginHeartbeat: func() bool { return h }, ArchiveReady: func() bool { return a },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	err := runSetupFirstStart(ctx, os.Stdout, hook)
	if err == nil || strings.Join(calls, ",") != "discord,companion" {
		t.Fatalf("failed gate %+v %v", calls, err)
	}
	h = true
	a = true
	err = runSetupFirstStart(context.Background(), os.Stdout, hook)
	if err != nil {
		t.Fatal(err)
	}
}
func TestFirstStartNeverStartsCompanionAfterDiscordFailure(t *testing.T) {
	calls := 0
	h := startupHooks{StartDiscord: func() error { return errors.New("failed") }, StartCompanion: func() error { calls++; return nil }, DiscordRunning: func() bool { return false }, CompanionRunning: func() bool { return false }, PluginHeartbeat: func() bool { return false }, ArchiveReady: func() bool { return false }}
	if e := runSetupFirstStart(context.Background(), os.Stdout, h); e == nil || calls != 0 {
		t.Fatal("unexpected launch")
	}
}
func TestFirstStartFreshHeartbeat(t *testing.T) {
	app, _ := mockNewBD(t)
	p := filepath.Join(app, "BetterDiscord", "DDS_Data", "plugin_heartbeat.json")
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	write := func(state string, at time.Time) {
		t.Helper()
		b, _ := json.Marshal(map[string]interface{}{"state": state, "pluginVersion": "0.5.3", "updatedAt": at.UTC().Format(time.RFC3339Nano)})
		if e := os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	write("RUNNING", time.Now())
	if !freshPluginHeartbeat(app) {
		t.Fatal("fresh heartbeat rejected")
	}
	write("RUNNING", time.Now().Add(-20*time.Minute))
	if freshPluginHeartbeat(app) {
		t.Fatal("stale heartbeat accepted")
	}
	write("STOPPED", time.Now())
	if freshPluginHeartbeat(app) {
		t.Fatal("STOPPED accepted")
	}
}
func TestFirstStartArchiveHeader(t *testing.T) {
	local := t.TempDir()
	p := filepath.Join(local, "DDS_Companion", "database", "dds.sqlite3")
	if sqliteHeaderPresent(local) {
		t.Fatal("missing DB accepted")
	}
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte("not sqlite"), 0600); e != nil {
		t.Fatal(e)
	}
	if sqliteHeaderPresent(local) {
		t.Fatal("fake DB accepted")
	}
	if e := os.WriteFile(p, []byte("SQLite format 3\x00test"), 0600); e != nil {
		t.Fatal(e)
	}
	if !sqliteHeaderPresent(local) {
		t.Fatal("SQLite header not accepted")
	}
}
