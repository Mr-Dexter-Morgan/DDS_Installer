package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

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
