package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func freshFixture(t *testing.T) (Inventory, string, string, string) {
	t.Helper()
	_, stage, dr, _ := previewFixture(t, "0.8.4")
	appData := t.TempDir()
	inv := Inventory{Companion: Component{State: StateNotDetected}, DataRoot: Component{State: StateOK, Path: dr}}
	return inv, stage, appData, dr
}

func assertNoFreshScratch(t *testing.T, appData string) {
	t.Helper()
	entries, err := os.ReadDir(appData)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".dds-installer-fresh-") {
			t.Fatalf("scratch not cleaned: %s", e.Name())
		}
	}
}

func TestFreshCompanionVerifiesAndPublishesOnlyNewRoot(t *testing.T) {
	inv, stage, base, dr := freshFixture(t)
	calls := 0
	got, err := freshCompanionInstall(inv, stage, base, func() error { calls++; return nil })
	if err != nil || got.Status != "FILES_INSTALLED_UNSTARTED" || got.Version != "0.8.5" || got.Root != filepath.Join(base, "DDS") || calls != 2 {
		t.Fatalf("%+v err=%v checks=%d", got, err, calls)
	}
	_, path, ok := validCompanionCache(stage, false)
	if !ok {
		t.Fatal("missing staging")
	}
	if err := compareVersionTree(filepath.Join(path, "payload"), got.Root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(got.Root, "Data")); !os.IsNotExist(err) {
		t.Fatalf("unexpected DataRoot: %v", err)
	}
	assertPreviewReadOnly(t, dr)
	assertNoFreshScratch(t, base)
}

func TestFreshCompanionRejectsExistingTargetEvenWhenEmpty(t *testing.T) {
	inv, stage, base, dr := freshFixture(t)
	target := filepath.Join(base, "DDS")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	_, err := freshCompanionInstall(inv, stage, base, func() error { return nil })
	if err == nil {
		t.Fatal("existing directory must block")
	}
	st, err := os.ReadDir(target)
	if err != nil || len(st) != 0 {
		t.Fatalf("existing folder altered: %v %v", st, err)
	}
	assertNoFreshScratch(t, base)
	assertPreviewReadOnly(t, dr)
}

func TestFreshCompanionRejectsUnsafeOrUncertainEnvironment(t *testing.T) {
	for _, tc := range []string{"existing_installed", "partial", "portable", "overlap_dataroot", "unverified_stage", "missing_process_guard"} {
		t.Run(tc, func(t *testing.T) {
			inv, stage, base, dr := freshFixture(t)
			guard := func() error { return nil }
			switch tc {
			case "existing_installed":
				inv.Companion.State = StateOK
			case "partial":
				inv.Companion.State = StatePartial
			case "portable":
				inv.Profile = "portable"
			case "overlap_dataroot":
				inv.DataRoot.Path = filepath.Join(base, "DDS", "Data")
			case "unverified_stage":
				f := filepath.Join(stage, "companion", "v0.8.5", "payload", "DDS.exe")
				if err := os.WriteFile(f, []byte("tampered"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing_process_guard":
				guard = nil
			}
			_, err := freshCompanionInstall(inv, stage, base, guard)
			if err == nil {
				t.Fatal("unsafe state accepted")
			}
			if _, err = os.Lstat(filepath.Join(base, "DDS")); !os.IsNotExist(err) {
				t.Fatalf("target changed: %v", err)
			}
			assertNoFreshScratch(t, base)
			assertPreviewReadOnly(t, dr)
		})
	}
}

func TestFreshCompanionGuardBeforePublishCleansScratch(t *testing.T) {
	inv, stage, base, dr := freshFixture(t)
	calls := 0
	_, err := freshCompanionInstall(inv, stage, base, func() error {
		calls++
		if calls == 2 {
			return errors.New("DDS started")
		}
		return nil
	})
	if err == nil || calls != 2 {
		t.Fatalf("guard failed: %v, %d", err, calls)
	}
	if _, err = os.Lstat(filepath.Join(base, "DDS")); !os.IsNotExist(err) {
		t.Fatalf("unexpected install: %v", err)
	}
	assertNoFreshScratch(t, base)
	assertPreviewReadOnly(t, dr)
}

func TestFreshCompanionGuardDetectsDestinationRace(t *testing.T) {
	inv, stage, base, dr := freshFixture(t)
	calls := 0
	target := filepath.Join(base, "DDS")
	_, err := freshCompanionInstall(inv, stage, base, func() error {
		calls++
		if calls == 2 {
			if err := os.Mkdir(target, 0700); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(target, "sentinel"), []byte("untouched"), 0600)
		}
		return nil
	})
	if err == nil {
		t.Fatal("unexpected install into newly created directory")
	}
	b, err := os.ReadFile(filepath.Join(target, "sentinel"))
	if err != nil || !bytes.Equal(b, []byte("untouched")) {
		t.Fatalf("sentinel overwritten %q %v", b, err)
	}
	assertNoFreshScratch(t, base)
	assertPreviewReadOnly(t, dr)
}

func TestFreshCompanionMenuRequiresExactApproval(t *testing.T) {
	inv, stage, base, dr := freshFixture(t)
	for _, typed := range []string{"\n", "да\n", "установить\n", "УСТАНОВИТЬ_\n"} {
		var out strings.Builder
		runCompanionFreshMenu(inv, strings.NewReader(typed), &out, stage, base, func() error { return nil })
		if !strings.Contains(out.String(), "ОТМЕНЕНО") {
			t.Fatal(out.String())
		}
		if _, err := os.Lstat(filepath.Join(base, "DDS")); !os.IsNotExist(err) {
			t.Fatal("menu changed root")
		}
	}
	assertPreviewReadOnly(t, dr)
}
