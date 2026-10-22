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
	if path, err := writeDesktopShortcut(root, desktop, false); err != nil || path != "" {
		t.Fatal(path, err)
	}
	entries, _ := os.ReadDir(desktop)
	if len(entries) != 0 {
		t.Fatal("unchecked shortcut created")
	}
	link, err := writeDesktopShortcut(root, desktop, true)
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
	if _, err = writeDesktopShortcut(root, desktop, true); err != nil {
		t.Fatal(err)
	}
	last, _ := os.Stat(link)
	entries, _ = os.ReadDir(desktop)
	if len(entries) != 1 || !first.ModTime().Equal(last.ModTime()) {
		t.Fatal("duplicate or needless rewrite")
	}
	other := t.TempDir()
	_ = os.WriteFile(filepath.Join(other, "DDS.exe"), []byte("other"), 0600)
	if _, err = writeDesktopShortcut(other, desktop, true); err == nil {
		t.Fatal("unrelated shortcut replaced")
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
