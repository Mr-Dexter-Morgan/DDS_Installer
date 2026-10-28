//go:build windows

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"testing"
)

func TestWindowsShortcutRealLinkAndNoDuplicates(t *testing.T) {
	root, desktop := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "DDS.exe"), []byte("test launcher identity"), 0600); err != nil {
		t.Fatal(err)
	}
	if path, err := writeDesktopShortcut(root, desktop, false, installerOwnership{}); err != nil || path != "" {
		t.Fatal(path, err)
	}
	entries, _ := os.ReadDir(desktop)
	if len(entries) != 0 {
		t.Fatal("unchecked shortcut created")
	}
	link, err := writeDesktopShortcut(root, desktop, true, installerOwnership{})
	if err != nil {
		t.Fatal(err)
	}
	script := "$s=(New-Object -ComObject WScript.Shell).CreateShortcut(" + powershellLiteral(link) + "); @{Target=$s.TargetPath;Icon=$s.IconLocation}|ConvertTo-Json -Compress"
	out, err := hiddenPowerShell(context.Background(), script)
	if err != nil {
		t.Fatal(err)
	}
	var props struct{ Target, Icon string }
	if json.Unmarshal(out, &props) != nil || props.Target != filepath.Join(root, "DDS.exe") || props.Icon != filepath.Join(root, "DDS.exe")+",0" {
		t.Fatal(string(out))
	}
	first, _ := os.Stat(link)
	if _, err = writeDesktopShortcut(root, desktop, true, installerOwnership{}); err != nil {
		t.Fatal(err)
	}
	last, _ := os.Stat(link)
	entries, _ = os.ReadDir(desktop)
	if len(entries) != 1 || !first.ModTime().Equal(last.ModTime()) {
		t.Fatal("duplicate or needless rewrite")
	}
	other := t.TempDir()
	_ = os.WriteFile(filepath.Join(other, "DDS.exe"), []byte("other"), 0600)
	if _, err = writeDesktopShortcut(other, desktop, true, installerOwnership{}); err == nil {
		t.Fatal("unrelated shortcut replaced")
	}
}

func TestWindowsShortcutMigratesInstallerOwnedLegacyTarget(t *testing.T) {
	oldRoot, newRoot, desktop := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(oldRoot, "DDS.exe"), []byte("legacy launcher"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newRoot, "DDS.exe"), []byte("new launcher"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(desktop, "DDS.lnk")
	script := "$s=(New-Object -ComObject WScript.Shell).CreateShortcut(" + powershellLiteral(link) + "); $s.TargetPath=" + powershellLiteral(filepath.Join(oldRoot, "DDS.exe")) + "; $s.WorkingDirectory=" + powershellLiteral(oldRoot) + "; $s.IconLocation=" + powershellLiteral(filepath.Join(oldRoot, "DDS.exe")+",0") + "; $s.Save()"
	if out, err := hiddenPowerShell(context.Background(), script); err != nil {
		t.Fatal(err, string(out))
	}
	previous := installerOwnership{Schema: "dds-installer-ownership-v1", Root: oldRoot, Shortcut: link, Version: "0.9.0-gui-r4"}
	if _, err := writeDesktopShortcut(newRoot, desktop, true, previous); err != nil {
		t.Fatal(err)
	}
	check := "$s=(New-Object -ComObject WScript.Shell).CreateShortcut(" + powershellLiteral(link) + "); @{Target=$s.TargetPath;Work=$s.WorkingDirectory}|ConvertTo-Json -Compress"
	out, err := hiddenPowerShell(context.Background(), check)
	if err != nil {
		t.Fatal(err)
	}
	var props struct{ Target, Work string }
	if json.Unmarshal(out, &props) != nil || props.Target != filepath.Join(newRoot, "DDS.exe") || props.Work != newRoot {
		t.Fatal(string(out))
	}
}

func TestWindowsShortcutDoesNotTrustMismatchedOwnershipRecord(t *testing.T) {
	ownedRoot, foreignRoot, newRoot, desktop := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	for _, root := range []string{ownedRoot, foreignRoot, newRoot} {
		if err := os.WriteFile(filepath.Join(root, "DDS.exe"), []byte(root), 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(desktop, "DDS.lnk")
	script := "$s=(New-Object -ComObject WScript.Shell).CreateShortcut(" + powershellLiteral(link) + "); $s.TargetPath=" + powershellLiteral(filepath.Join(foreignRoot, "DDS.exe")) + "; $s.Save()"
	if out, err := hiddenPowerShell(context.Background(), script); err != nil {
		t.Fatal(err, string(out))
	}
	previous := installerOwnership{Schema: "dds-installer-ownership-v1", Root: ownedRoot, Shortcut: link, Version: "0.9.0-gui-r4"}
	if _, err := writeDesktopShortcut(newRoot, desktop, true, previous); err == nil {
		t.Fatal("foreign shortcut replaced despite mismatched ownership target")
	}
}

func TestWindowsOwnerCanMaintainVersionedProgramFiles(t *testing.T) {
	root := t.TempDir()
	owner, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if err = ownerModifyPermission(root, owner.Uid); err != nil {
		t.Fatal(err)
	}
	versions := filepath.Join(root, "versions", "0.8.5")
	if err = os.MkdirAll(versions, 0700); err != nil {
		t.Fatal(err)
	}
	script := "$sid=" + powershellLiteral(owner.Uid) + "; $found=@(([System.IO.Directory]::GetAccessControl(" + powershellLiteral(versions) + ")).Access | Where-Object {$_.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value -eq $sid -and ($_.FileSystemRights -band [System.Security.AccessControl.FileSystemRights]::Modify) -eq [System.Security.AccessControl.FileSystemRights]::Modify}); if($found.Count -eq 0){throw 'Owner Modify ACE missing'}"
	if out, err := hiddenPowerShell(context.Background(), script); err != nil {
		t.Fatal(err, string(out))
	}
	p := filepath.Join(root, "current.json")
	_ = os.WriteFile(p, []byte("old"), 0600)
	next := filepath.Join(root, "next.json")
	_ = os.WriteFile(next, []byte("new"), 0600)
	if err = atomicReplaceFile(next, p); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(next, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = atomicReplaceFile(next, p); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsProgramFilesKnownFolderAndShellLayout(t *testing.T) {
	root := defaultProgramRoot()
	if !filepath.IsAbs(root) || filepath.Base(root) != "DDS" || requireNormalDir(filepath.Dir(root)) != nil {
		t.Fatalf("invalid system Program Files: %s", root)
	}
}
