//go:build !windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// Called only by sandbox tests; production Apply is Windows-only.
func checkNoCompanionProcesses() error { return nil }

func discordClientIsRunning() bool { return false } // tests only; product binary is Windows-only

func freeDiskSpace(dir string) (uint64, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(dir, &s); err != nil {
		return 0, err
	}
	return s.Bavail * uint64(s.Bsize), nil
}

func hideInstallerCommand(cmd *exec.Cmd) {}

func defaultProgramRoot() string { return filepath.Join(os.Getenv("ProgramFiles"), "DDS") }

const installerVersion = "0.9.0-gui-r4"

func main() {
	enableUTF8Console()
	exe, _ := os.Executable()
	ctx := DetectContext{
		LocalAppData: os.Getenv("LOCALAPPDATA"),
		AppData:      os.Getenv("APPDATA"),
		InstallerDir: filepath.Dir(exe),
		Processes:    runningDDSProcesses(),
	}
	if runtime.GOOS != "windows" {
		fmt.Println("DDS Setup предназначен для Windows.")
		return
	}
	inv := Detect(ctx)
	printInventory(inv)
	runStageMenu(inv)
	if strings.EqualFold(os.Getenv("DDS_TEST_NONINTERACTIVE"), "1") {
		return
	}
	waitForEnter()
}

func enableUTF8Console()                      {}
func runningDDSProcesses() []CompanionProcess { return nil }

func stopDiscordForSetup(context.Context) error          { return errors.New("Windows only") }
func startInstalledDiscord(string, string, string) error { return errors.New("Windows only") }
func startInstalledCompanion(string) error               { return errors.New("Windows only") }
func companionClientIsRunning() bool                     { return false }

func atomicReplaceFile(src, dst string) error { return os.Rename(src, dst) }
