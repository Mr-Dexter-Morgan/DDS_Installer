package main

import (
	"bytes"
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
