package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
