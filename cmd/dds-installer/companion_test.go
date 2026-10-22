package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func upgradeFixture(t *testing.T) (Inventory, string, string) {
	t.Helper()
	inv, stage, dr, _ := previewFixture(t, "0.8.4")
	root := inv.Companion.Path
	if e := os.MkdirAll(filepath.Join(root, "versions", "0.8.4"), 0700); e != nil {
		t.Fatal(e)
	}
	for _, pair := range [][2]string{{"DDSUpdater.exe", "old updater"}, {"versions/0.8.4/DDSApp.exe", "old app"}} {
		p := filepath.Join(root, filepath.FromSlash(pair[0]))
		if e := os.WriteFile(p, []byte(pair[1]), 0600); e != nil {
			t.Fatal(e)
		}
	}
	return inv, stage, dr
}
func readRootBootstraps(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, n := range bootstrapRootNames {
		b, e := os.ReadFile(filepath.Join(root, n))
		if e != nil {
			t.Fatal(e)
		}
		out[n] = b
	}
	return out
}
func checkRootBootstraps(t *testing.T, root string, expect map[string][]byte) {
	t.Helper()
	for n, b := range expect {
		actual, e := os.ReadFile(filepath.Join(root, n))
		if e != nil || !bytes.Equal(b, actual) {
			t.Fatalf("%s changed: %q %v", n, actual, e)
		}
	}
}
func TestCompanionApplyUpgradeWithBackupAndDataRootPreserved(t *testing.T) {
	inv, stage, dr := upgradeFixture(t)
	old := readRootBootstraps(t, inv.Companion.Path)
	checks := 0
	r, e := applyCompanionWithValidation(inv, stage, func() error { checks++; return nil }, nil)
	if e != nil || r.Status != "APPLIED_AND_VALIDATED" || checks != 3 {
		t.Fatalf("%+v %v checks=%d", r, e, checks)
	}
	if e := validateCompanionPackage(r.Root, "0.8.5"); e == nil {
		// An installed root intentionally still holds the previous version,
		// so the single-version release package validator must reject it.
		t.Fatal("multi-version installed root must not be mistaken for pristine ZIP")
	}
	b, e := os.ReadFile(filepath.Join(r.Root, "current.json"))
	var ptr currentPointer
	if e != nil || json.Unmarshal(b, &ptr) != nil || ptr.Version != "0.8.5" {
		t.Fatalf("wrong pointer: %s %v", b, e)
	}
	for n, content := range old {
		saved, e := os.ReadFile(filepath.Join(r.Backup, "previous-"+n))
		if e != nil || !bytes.Equal(content, saved) {
			t.Fatalf("backup %s mismatch %v", n, e)
		}
	}
	if _, e := os.Stat(filepath.Join(r.Root, "versions", "0.8.4", "DDSApp.exe")); e != nil {
		t.Fatal("lost previous version", e)
	}
	if _, e := os.Stat(filepath.Join(r.Root, "versions", "0.8.5", "DDSApp.exe")); e != nil {
		t.Fatal("new version missing", e)
	}
	assertPreviewReadOnly(t, dr)
}
func TestCompanionApplyRollbackOnInjectedPostvalidationFailure(t *testing.T) {
	inv, stage, dr := upgradeFixture(t)
	old := readRootBootstraps(t, inv.Companion.Path)
	r, e := applyCompanionWithValidation(inv, stage, func() error { return nil }, func(string, string) error { return errors.New("injected check fail") })
	if e == nil || !strings.Contains(e.Error(), "предыдущие файлы восстановлены") {
		t.Fatalf("%+v %v", r, e)
	}
	checkRootBootstraps(t, inv.Companion.Path, old)
	assertPreviewReadOnly(t, dr)
	if _, e := os.Stat(r.Backup); e != nil {
		t.Fatal("rollback evidence lost", e)
	}
}
func TestCompanionApplyNoopWhenVersionMatches(t *testing.T) {
	inv, stage, dr, _ := previewFixture(t, "0.8.5")
	if e := os.WriteFile(filepath.Join(inv.Companion.Path, "DDSUpdater.exe"), []byte("dummy updater"), 0600); e != nil {
		t.Fatal(e)
	}
	old := readRootBootstraps(t, inv.Companion.Path)
	called := 0
	r, e := applyCompanionWithValidation(inv, stage, func() error { called++; return nil }, nil)
	if e != nil || r.Status != "VERSION_MATCH_NOOP" || called != 0 {
		t.Fatalf("%+v %v %d", r, e, called)
	}
	checkRootBootstraps(t, inv.Companion.Path, old)
	assertPreviewReadOnly(t, dr)
}
func TestCompanionApplyGuardsBeforeAnyWrite(t *testing.T) {
	for _, kind := range []string{"running", "portable", "newer", "tampered_stage", "existing_version", "unsafe_root"} {
		t.Run(kind, func(t *testing.T) {
			inv, stage, dr := upgradeFixture(t)
			root := inv.Companion.Path
			old := readRootBootstraps(t, root)
			checker := func() error { return nil }
			switch kind {
			case "running":
				checker = func() error { return errors.New("still running") }
			case "portable":
				inv.Profile = "portable"
			case "newer":
				inv.Companion.Version = "0.8.6"
				ptr, _ := json.Marshal(currentPointer{Schema: "dds-version-pointer-v1", Version: "0.8.6", RelativePath: "versions/0.8.6"})
				os.WriteFile(filepath.Join(root, "current.json"), ptr, 0600)
				old = readRootBootstraps(t, root)
			case "tampered_stage":
				os.WriteFile(filepath.Join(stage, "companion", "v0.8.5", "payload", "DDS.exe"), []byte("bad"), 0600)
			case "existing_version":
				os.MkdirAll(filepath.Join(root, "versions", "0.8.5"), 0700)
			case "unsafe_root":
				os.Remove(filepath.Join(root, "DDSUpdater.exe"))
			}
			_, e := applyCompanionWithValidation(inv, stage, checker, nil)
			if e == nil {
				t.Fatal("expected BLOCKED")
			}
			if kind != "unsafe_root" {
				checkRootBootstraps(t, root, old)
			}
			assertPreviewReadOnly(t, dr)
		})
	}
}
func TestCompanionApplyMenuRequiresExactTypedApproval(t *testing.T) {
	inv, stage, dr := upgradeFixture(t)
	old := readRootBootstraps(t, inv.Companion.Path)
	for _, input := range []string{"\n", "yes\n", "установить\n", "УСТАНОВИТЬ_\n"} {
		var output strings.Builder
		runCompanionApplyMenu(inv, strings.NewReader(input), &output, stage, func() error { return nil })
		if !strings.Contains(output.String(), "ОТМЕНЕНО") {
			t.Fatalf("%q: %s", input, output.String())
		}
		checkRootBootstraps(t, inv.Companion.Path, old)
	}
	assertPreviewReadOnly(t, dr)
}
func TestCompanionApplyGuardDetectsProcessBeforeCommit(t *testing.T) {
	inv, stage, dr := upgradeFixture(t)
	old := readRootBootstraps(t, inv.Companion.Path)
	calls := 0
	_, e := applyCompanionWithValidation(inv, stage, func() error {
		calls++
		if calls == 3 {
			return errors.New("DDS became active before commit")
		}
		return nil
	}, nil)
	if e == nil || calls != 3 {
		t.Fatalf("guard %d %v", calls, e)
	}
	checkRootBootstraps(t, inv.Companion.Path, old)
	if _, e := os.Stat(filepath.Join(inv.Companion.Path, "versions", "0.8.5")); !os.IsNotExist(e) {
		t.Fatal("new version published despite guard", e)
	}
	assertPreviewReadOnly(t, dr)
}
func TestCompanionApplyCopyTreeZeroByteIsAllowed(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	os.Mkdir(src, 0700)
	os.Mkdir(dst, 0700)
	os.WriteFile(filepath.Join(src, "empty.txt"), nil, 0600)
	if e := copyVersionFiles(src, dst); e != nil {
		t.Fatal(e)
	}
}

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

func TestCompanionStagingDetectsEveryUnmanifestedBootstrapMutation(t *testing.T) {
	for _, name := range bootstrapPaths("0.8.5") {
		t.Run(name, func(t *testing.T) {
			c, _ := mockServer(t, fakePackage("0.8.5", nil), "", nil)
			root := t.TempDir()
			rec, stage, err := c.stageCompanion(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if len(rec.BootstrapSHA256) != 4 {
				t.Fatalf("receipt did not bind all bootstrap files: %+v", rec)
			}
			if _, _, ok := validCompanionCache(root, true); !ok {
				t.Fatal("untampered stage rejected")
			}
			target := filepath.Join(stage, "payload", filepath.FromSlash(name))
			if err := os.WriteFile(target, []byte("unauthorized mutation"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, ok := validCompanionCache(root, true); ok {
				t.Fatalf("tampered %s wrongly reused", name)
			}
		})
	}
}

func TestLegacyCompanionReceiptCannotBeUpgradedByBlindlyTrustingFiles(t *testing.T) {
	c, requests := mockServer(t, fakePackage("0.8.5", nil), "", nil)
	root := t.TempDir()
	rec, stage, err := c.stageCompanion(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	rec.BootstrapSHA256 = nil // simulate 0.3.0-r2 receipt
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(stage, "stage_receipt.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := validCompanionCache(root, true); ok {
		t.Fatal("unbound legacy stage is not safe for preview/apply")
	}
	initialRequests := *requests
	replacement, _, err := c.stageCompanion(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if *requests <= initialRequests {
		t.Fatal("legacy receipt must be regenerated from reauthenticated archive")
	}
	if len(replacement.BootstrapSHA256) != 4 {
		t.Fatal("replacement staging must bind bootstrap digests")
	}
	if _, _, ok := validCompanionCache(root, true); !ok {
		t.Fatal("reverified replacement must be reusable")
	}
}

func TestCompanionStageRefusesForgedOrMissingBootstrapDigests(t *testing.T) {
	for _, kind := range []string{"missing", "malformed", "incorrect"} {
		t.Run(kind, func(t *testing.T) {
			c, _ := mockServer(t, fakePackage("0.8.5", nil), "", nil)
			root := t.TempDir()
			rec, stage, err := c.stageCompanion(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "missing":
				delete(rec.BootstrapSHA256, "DDS.exe")
			case "malformed":
				rec.BootstrapSHA256["DDS.exe"] = "not-a-sha"
			case "incorrect":
				rec.BootstrapSHA256["DDS.exe"] = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			}
			data, err := json.Marshal(rec)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(stage, "stage_receipt.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, ok := validCompanionCache(root, true); ok {
				t.Fatal("incorrect receipt must not be trusted")
			}
		})
	}
}

func previewFixture(t *testing.T, installedVersion string) (Inventory, string, string, *int) {
	t.Helper()
	base := t.TempDir()
	stageRoot := filepath.Join(base, "staging")
	c, calls := mockServer(t, fakePackage("0.8.5", nil), "", nil)
	rec, path, err := c.stageCompanion(context.Background(), stageRoot)
	if err != nil {
		t.Fatal(err)
	}
	rec.Source = companionAssetsPrefix + "v0.8.5/DDS-0.8.5-windows-x64.zip"
	b, _ := json.Marshal(rec)
	if err = os.WriteFile(filepath.Join(path, "stage_receipt.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "app")
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "DDS.exe"), []byte("live launcher"), 0600); err != nil {
		t.Fatal(err)
	}
	p, _ := json.Marshal(currentPointer{Version: installedVersion, RelativePath: "versions/" + installedVersion, Schema: "dds-version-pointer-v1"})
	if err = os.WriteFile(filepath.Join(root, "current.json"), p, 0600); err != nil {
		t.Fatal(err)
	}
	dr := filepath.Join(base, "DataRoot")
	if err = os.MkdirAll(dr, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dr, "archive.sqlite"), []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	inv := Inventory{Companion: Component{State: StateOK, Version: installedVersion, Path: root}, Profile: "installed", DataRoot: Component{State: StateOK, Path: dr}}
	return inv, stageRoot, dr, calls
}
func assertPreviewReadOnly(t *testing.T, dr string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dr, "archive.sqlite"))
	if err != nil || string(b) != "untouched" {
		t.Fatalf("DataRoot changed %q %v", b, err)
	}
}
func TestCompanionPreviewVersionMatchNoWrite(t *testing.T) {
	inv, stage, dr, calls := previewFixture(t, "0.8.5")
	before := *calls
	current := prepareCompanionPreview(inv, stage)
	if current.Status != "VERSION_MATCH" || current.StagedVersion != "0.8.5" || current.InstalledVersion != "0.8.5" {
		t.Fatalf("%+v", current)
	}
	if *calls != before {
		t.Fatal("preview must not use GitHub")
	}
	assertPreviewReadOnly(t, dr)
}
func TestCompanionPreviewUpgradeIsNotApply(t *testing.T) {
	inv, stage, dr, _ := previewFixture(t, "0.8.4")
	got := prepareCompanionPreview(inv, stage)
	if got.Status != "PREVIEW_ONLY" {
		t.Fatalf("%+v", got)
	}
	b, e := os.ReadFile(filepath.Join(inv.Companion.Path, "current.json"))
	if e != nil || !strings.Contains(string(b), "0.8.4") {
		t.Fatal("unexpected root modification", e)
	}
	assertPreviewReadOnly(t, dr)
}
func TestCompanionPreviewRefusesDowngrade(t *testing.T) {
	inv, stage, dr, _ := previewFixture(t, "0.8.6")
	got := prepareCompanionPreview(inv, stage)
	if got.Status != "BLOCKED" || !strings.Contains(got.Message, "Понижение") {
		t.Fatalf("%+v", got)
	}
	assertPreviewReadOnly(t, dr)
}
func TestCompanionPreviewRefusesPortable(t *testing.T) {
	inv, stage, _, _ := previewFixture(t, "0.8.5")
	inv.Profile = "portable?"
	if got := prepareCompanionPreview(inv, stage); got.Status != "BLOCKED" {
		t.Fatalf("%+v", got)
	}
}
func TestCompanionPreviewRefusesCorruptPointer(t *testing.T) {
	inv, stage, _, _ := previewFixture(t, "0.8.5")
	os.WriteFile(filepath.Join(inv.Companion.Path, "current.json"), []byte(`{"version":"0.8.5","relative_path":"../../outside","schema":"dds-version-pointer-v1"}`), 0600)
	if got := prepareCompanionPreview(inv, stage); got.Status != "BLOCKED" {
		t.Fatalf("%+v", got)
	}
}
func TestCompanionPreviewRefusesUnverifiedStage(t *testing.T) {
	inv, stage, dr, _ := previewFixture(t, "0.8.5")
	os.WriteFile(filepath.Join(stage, "companion", "v0.8.5", "payload", "DDS.exe"), []byte("tampered"), 0600)
	got := prepareCompanionPreview(inv, stage)
	if got.Status != "BLOCKED" || !strings.Contains(got.Message, "staging") {
		t.Fatalf("%+v", got)
	}
	assertPreviewReadOnly(t, dr)
}
func TestCompanionPreviewBlocksMissingRoot(t *testing.T) {
	inv, stage, _, _ := previewFixture(t, "0.8.5")
	inv.Companion.State = StateNotDetected
	if got := prepareCompanionPreview(inv, stage); got.Status != "BLOCKED" {
		t.Fatalf("%+v", got)
	}
}
func TestCompanionPreviewNeverClaimsInstallation(t *testing.T) {
	inv, stage, _, _ := previewFixture(t, "0.8.4")
	var out strings.Builder
	runCompanionPreview(inv, &out, stage)
	if !strings.Contains(out.String(), "БЕЗ УСТАНОВКИ") || !strings.Contains(out.String(), "PREVIEW_ONLY") {
		t.Fatal(out.String())
	}
}

func setupCrashFixture(t *testing.T, switched int) (Inventory, string, string, map[string][]byte, string) {
	t.Helper()
	inv, stage, dr := upgradeFixture(t)
	root := inv.Companion.Path
	original := readRootBootstraps(t, root)
	rec, stagePath, ok := validCompanionCache(stage, false)
	if !ok {
		t.Fatal("missing fixture stage")
	}
	backup, e := os.MkdirTemp(root, ".dds-installer-backup-")
	if e != nil {
		t.Fatal(e)
	}
	next := map[string]string{}
	previous := map[string]string{}
	for _, n := range bootstrapRootNames {
		h, _, e := shaFile(filepath.Join(root, n))
		if e != nil {
			t.Fatal(e)
		}
		previous[n] = h
		next[n] = rec.BootstrapSHA256[n]
		if e = copyCheckedFile(filepath.Join(root, n), filepath.Join(backup, "previous-"+n), h); e != nil {
			t.Fatal(e)
		}
	}
	j := companionTransaction{Schema: companionTxnSchema, PreviousVersion: "0.8.4", NextVersion: "0.8.5", PreviousSHA256: previous, NextSHA256: next}
	if e = writeCompanionTransactionPending(backup, j); e != nil {
		t.Fatal(e)
	}
	for i, n := range bootstrapRootNames {
		if i >= switched {
			break
		}
		bs, e := os.ReadFile(filepath.Join(stagePath, "payload", n))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(root, n), bs, 0600); e != nil {
			t.Fatal(e)
		}
	}
	return inv, stage, dr, original, backup
}
func TestInterruptedRecoveryEveryCrashPosition(t *testing.T) {
	for _, i := range []int{0, 1, 2, 3} {
		t.Run(string(rune('0'+i)), func(t *testing.T) {
			inv, stage, dr, old, backup := setupCrashFixture(t, i)
			if e := ensureNoPendingCompanionTransaction(inv.Companion.Path); e == nil {
				t.Fatal("pending transaction did not block apply")
			}
			if _, e := applyCompanionWithValidation(inv, stage, func() error { return nil }, nil); e == nil {
				t.Fatal("Apply permitted during pending recovery")
			}
			got, e := recoverCompanionInterrupted(inv.Companion.Path, func() error { return nil })
			if e != nil || got != backup {
				t.Fatalf("recovery %q: %v", got, e)
			}
			checkRootBootstraps(t, inv.Companion.Path, old)
			if _, e := os.Stat(filepath.Join(backup, companionTxnRecovered)); e != nil {
				t.Fatal("missing completed recovery journal", e)
			}
			if _, e := os.Stat(filepath.Join(backup, companionTxnPending)); !os.IsNotExist(e) {
				t.Fatal("still pending after recovery")
			}
			if e := ensureNoPendingCompanionTransaction(inv.Companion.Path); e != nil {
				t.Fatal(e)
			}
			assertPreviewReadOnly(t, dr)
		})
	}
}
func TestInterruptedRecoveryResumeAfterSecondInterruption(t *testing.T) {
	inv, _, dr, old, backup := setupCrashFixture(t, 3)
	checks := 0
	_, e := recoverCompanionInterrupted(inv.Companion.Path, func() error {
		checks++
		if checks == 4 {
			return errors.New("simulated second interruption")
		}
		return nil
	})
	if e == nil || !strings.Contains(e.Error(), "simulated second") {
		t.Fatalf("missing simulated interruption %v checks=%d", e, checks)
	}
	if _, e := os.Stat(filepath.Join(backup, companionTxnPending)); e != nil {
		t.Fatal("recovery journal lost", e)
	}
	_, e = recoverCompanionInterrupted(inv.Companion.Path, func() error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	checkRootBootstraps(t, inv.Companion.Path, old)
	assertPreviewReadOnly(t, dr)
}
func TestInterruptedRecoveryCorruptionFailClosed(t *testing.T) {
	for _, scenario := range []string{"backup", "independent_root", "journal", "multiple_transactions", "running"} {
		t.Run(scenario, func(t *testing.T) {
			inv, _, dr, _, backup := setupCrashFixture(t, 1)
			root := inv.Companion.Path
			currentBefore := readRootBootstraps(t, root)
			check := func() error { return nil }
			switch scenario {
			case "backup":
				os.WriteFile(filepath.Join(backup, "previous-DDS.exe"), []byte("bad"), 0600)
			case "independent_root":
				os.WriteFile(filepath.Join(root, "current.json"), []byte("independent edit"), 0600)
			case "journal":
				os.WriteFile(filepath.Join(backup, companionTxnPending), []byte("{bad"), 0600)
			case "multiple_transactions":
				second, e := os.MkdirTemp(root, ".dds-installer-backup-")
				if e != nil {
					t.Fatal(e)
				}
				b, e := os.ReadFile(filepath.Join(backup, companionTxnPending))
				if e != nil {
					t.Fatal(e)
				}
				os.WriteFile(filepath.Join(second, companionTxnPending), b, 0600)
			case "running":
				check = func() error { return errors.New("DDS process running") }
			}
			if _, e := recoverCompanionInterrupted(root, check); e == nil {
				t.Fatal("unsafe recovery succeeded")
			}
			for n, b := range currentBefore {
				now, e := os.ReadFile(filepath.Join(root, n))
				if e != nil || !bytes.Equal(now, b) && !(scenario == "independent_root" && n == "current.json") {
					t.Fatalf("unexpected mutation %s: %v", n, e)
				}
			}
			if _, e := os.Stat(filepath.Join(backup, companionTxnPending)); e != nil {
				t.Fatal("pending marker lost", e)
			}
			assertPreviewReadOnly(t, dr)
		})
	}
}
func TestInterruptedRecoveryMenuRequiresExactApproval(t *testing.T) {
	inv, _, dr, old, _ := setupCrashFixture(t, 0)
	for _, response := range []string{"\n", "yes\n", "восстановить\n", "ВОССТАНОВИТЬ_\n"} {
		var out strings.Builder
		runCompanionRecoveryMenu(inv, strings.NewReader(response), &out, func() error { return nil })
		if !strings.Contains(out.String(), "ОТМЕНЕНО") {
			t.Fatalf("bad confirmation %q: %s", response, out.String())
		}
		checkRootBootstraps(t, inv.Companion.Path, old)
	}
	assertPreviewReadOnly(t, dr)
}
func TestCompanionApplyJournalCommittedOrRecovered(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		inv, stage, _ := upgradeFixture(t)
		result, e := applyCompanionWithValidation(inv, stage, func() error { return nil }, nil)
		if e != nil {
			t.Fatal(e)
		}
		if _, e := os.Stat(filepath.Join(result.Backup, companionTxnCompleted)); e != nil {
			t.Fatal("missing committed journal", e)
		}
		if e := ensureNoPendingCompanionTransaction(inv.Companion.Path); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("rollback", func(t *testing.T) {
		inv, stage, _ := upgradeFixture(t)
		result, e := applyCompanionWithValidation(inv, stage, func() error { return nil }, func(string, string) error { return errors.New("fault") })
		if e == nil {
			t.Fatal("missing fault")
		}
		if _, e := os.Stat(filepath.Join(result.Backup, companionTxnRecovered)); e != nil {
			t.Fatal("missing recovered journal", e)
		}
		if e := ensureNoPendingCompanionTransaction(inv.Companion.Path); e != nil {
			t.Fatal(e)
		}
	})
}
func TestInterruptedRecoveryMenuAcceptedOnSyntheticRoot(t *testing.T) {
	inv, _, dr, old, _ := setupCrashFixture(t, 3)
	var out strings.Builder
	runCompanionRecoveryMenu(inv, strings.NewReader("1\n"), &out, func() error { return nil })
	if !strings.Contains(out.String(), "RECOVERED_AND_VERIFIED") {
		t.Fatalf("recovery menu: %s", out.String())
	}
	checkRootBootstraps(t, inv.Companion.Path, old)
	assertPreviewReadOnly(t, dr)
}

func TestInterruptedRecoveryExplicitRootWhenDetectionUnavailable(t *testing.T) {
	inv, _, dr, old, _ := setupCrashFixture(t, 2)
	root := inv.Companion.Path
	inv.Companion.Path = ""
	inv.Profile = "unknown"
	var out strings.Builder
	runCompanionRecoveryMenu(inv, strings.NewReader(root+"\n1\n"), &out, func() error { return nil })
	if !strings.Contains(out.String(), "RECOVERED_AND_VERIFIED") {
		t.Fatalf("recovery unavailable: %s", out.String())
	}
	checkRootBootstraps(t, root, old)
	assertPreviewReadOnly(t, dr)
}
func TestInterruptedRecoveryExplicitRootRejectsPortable(t *testing.T) {
	inv, _, dr, old, _ := setupCrashFixture(t, 1)
	root := inv.Companion.Path
	os.Mkdir(filepath.Join(root, "Data"), 0700)
	inv.Profile = "unknown"
	inv.Companion.Path = ""
	var out strings.Builder
	runCompanionRecoveryMenu(inv, strings.NewReader(root+"\n1\n"), &out, func() error { return nil })
	if !strings.Contains(out.String(), "BLOCKED") {
		t.Fatalf("portable candidate permitted: %s", out.String())
	}
	// No recovery writes should have occurred.
	for n, b := range old {
		if n != "DDS.exe" {
			now, _ := os.ReadFile(filepath.Join(root, n))
			if !bytes.Equal(b, now) {
				t.Fatal("unsafe write", n)
			}
		}
	}
	assertPreviewReadOnly(t, dr)
}
