//go:build windows

package main

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/user"
	"path/filepath"
	"testing"
	"unsafe"
)

func TestUserElevationPolicy(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		elevated, disabled, blocked bool
	}{
		{"ordinary UAC desktop", false, false, false},
		{"explicit elevation with UAC", true, false, true},
		{"ordinary admin desktop without UAC", true, true, false},
		{"standard user without UAC", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := elevationBlockedByPolicy(tc.elevated, tc.disabled); got != tc.blocked {
				t.Fatalf("blocked=%v, want %v", got, tc.blocked)
			}
		})
	}
}

func TestReadActualElevationPolicy(t *testing.T) {
	t.Logf("elevated=%v UAC explicitly disabled=%v blocked=%v", processElevated(), uacExplicitlyDisabled(), userProcessElevationBlocked())
	if got, want := userProcessElevationBlocked(), elevationBlockedByPolicy(processElevated(), uacExplicitlyDisabled()); got != want {
		t.Fatalf("blocked=%v, want %v", got, want)
	}
}

// In-process Win32 rendering only. No production install, UI input, process
// control, registry changes or user archive access is performed by this test.
func TestNativeWizardRender(t *testing.T) {
	destination := os.Getenv("DDS_RENDER_OUTPUT")
	if destination == "" {
		t.Skip("render requested separately")
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		t.Fatal(err)
	}
	ui.fontSmall = makeFont(18, 400, "Segoe UI")
	ui.fontNormal = makeFont(22, 400, "Segoe UI")
	ui.fontBold = makeFont(22, 600, "Segoe UI")
	ui.fontTitle = makeFont(26, 600, "Segoe UI")
	defer func() {
		for _, f := range []uintptr{ui.fontSmall, ui.fontNormal, ui.fontBold, ui.fontTitle} {
			pDeleteObj.Call(f)
		}
	}()
	ui.state = guiInventory(StateOK, StateOK, StateOK, StateOK)
	ui.selectedFloors = [4]bool{true, true, true, true}
	ui.createShortcut = true
	ui.progress = 65
	ui.activity = "Устанавливаем BetterDiscord"
	ui.detail = "Подлинность и целостность пакетов проверены"
	dc, _, _ := gdi32.NewProc("CreateCompatibleDC").Call(0)
	if dc == 0 {
		t.Fatal("CreateCompatibleDC")
	}
	defer gdi32.NewProc("DeleteDC").Call(dc)
	info := bitmapInfo{Header: bitmapHeader{Size: 40, Width: guiW, Height: -guiH, Planes: 1, BitCount: 32}}
	var bits unsafe.Pointer
	bitmap, _, _ := gdi32.NewProc("CreateDIBSection").Call(dc, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 {
		t.Fatal("CreateDIBSection")
	}
	defer pDeleteObj.Call(bitmap)
	old, _, _ := pSelectObj.Call(dc, bitmap)
	defer pSelectObj.Call(dc, old)
	for page := 0; page < 3; page++ {
		ui.page = page
		ui.successful = page == 2
		paintPage(dc)
		if page == 0 {
			fill(dc, 64, 268, 660, 32, 1, 12, 30)
			edge(dc, 64, 268, 660, 32, 101, 149, 194)
			textAt(dc, defaultProgramRoot(), 74, 268, 640, 32, ui.fontSmall, rgb(244, 249, 255), 0)
			textAt(dc, "Обзор…", 734, 268, 100, 32, ui.fontSmall, rgb(94, 193, 255), dtCenter)
		}
		gdi32.NewProc("GdiFlush").Call()
		raw := unsafe.Slice((*byte)(bits), guiW*guiH*4)
		img := image.NewRGBA(image.Rect(0, 0, guiW, guiH))
		for y := 0; y < guiH; y++ {
			for x := 0; x < guiW; x++ {
				i := (y*guiW + x) * 4
				img.SetRGBA(x, y, color.RGBA{raw[i+2], raw[i+1], raw[i], 255})
			}
		}
		f, err := os.Create(filepath.Join(destination, []string{"setup.png", "install.png", "finish.png"}[page]))
		if err != nil {
			t.Fatal(err)
		}
		if err = png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}

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
