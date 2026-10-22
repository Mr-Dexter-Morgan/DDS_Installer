//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

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
