//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

func stopDiscordForSetup(ctx context.Context) error {
	return shutdownDiscordWith(ctx, discordStopActions{
		probe: discordProcessStatus,
		graceful: func(ctx context.Context) error {
			script := `$ErrorActionPreference='Stop'; Get-Process -Name Discord -ErrorAction SilentlyContinue | ForEach-Object { if ($_.MainWindowHandle -ne 0) { [void]$_.CloseMainWindow() } }`
			cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("PowerShell: %w (%s)", err, strings.TrimSpace(string(out)))
			}
			return nil
		},
		force: func(ctx context.Context) (string, error) {
			cmd := exec.CommandContext(ctx, "taskkill.exe", "/F", "/T", "/IM", "Discord.exe")
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			out, err := cmd.CombinedOutput()
			return strings.TrimSpace(string(out)), err
		},
		sleep: discordShutdownSleep,
	})
}
func spawnVerifiedClient(exe string, args ...string) error {
	if err := requireNormalFile(exe); err != nil {
		return err
	}
	cmd := exec.Command(exe, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
func startInstalledDiscord(localAppData, appData, installerDir string) error {
	c := detectDiscord(DetectContext{LocalAppData: localAppData, AppData: appData, InstallerDir: installerDir})
	if c.State != StateOK || !strings.EqualFold(filepath.Base(c.Path), "Discord.exe") {
		return errors.New("Discord Stable не обнаружен")
	}
	return spawnVerifiedClient(c.Path)
}
func startInstalledCompanion(localAppData string) error {
	root := filepath.Join(localAppData, "DDS")
	if err := requireNormalDir(root); err != nil {
		return err
	}
	ptr, err := readCurrent(root)
	if err != nil {
		return err
	}
	if ptr.Schema != "dds-version-pointer-v1" || ptr.RelativePath != "versions/"+ptr.Version {
		return errors.New("неверный current.json DDS")
	}
	if err := requireNormalFile(filepath.Join(root, filepath.FromSlash(ptr.RelativePath), "DDSApp.exe")); err != nil {
		return err
	}
	return spawnVerifiedClient(filepath.Join(root, "DDS.exe"))
}
func companionClientIsRunning() bool { return len(runningDDSProcesses()) > 0 }

func atomicReplaceFile(src, dst string) error {
	from, _ := syscall.UTF16PtrFromString(src)
	to, _ := syscall.UTF16PtrFromString(dst)
	// MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH
	r, _, e := syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW").Call(uintptr(unsafe.Pointer(from)), uintptr(unsafe.Pointer(to)), 0x1|0x8)
	if r == 0 {
		return fmt.Errorf("MoveFileExW: %w", e)
	}
	return nil
}

// Confirm a payload belonging to the selected installation, not any DDS process.
func companionAtRunning(root string) bool {
	for _, p := range runningDDSProcesses() {
		if candidate := rootFromCompanionProcess(p); candidate != "" && strings.EqualFold(filepath.Clean(candidate), filepath.Clean(root)) {
			return true
		}
	}
	return false
}
