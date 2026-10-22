package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
