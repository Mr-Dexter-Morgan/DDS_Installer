package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

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
