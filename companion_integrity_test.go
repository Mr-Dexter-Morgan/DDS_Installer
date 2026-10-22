package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

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
