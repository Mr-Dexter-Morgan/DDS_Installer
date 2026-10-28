//go:build windows

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"syscall"
)

func enableUTF8Console() {
	k32 := syscall.NewLazyDLL("kernel32.dll")
	setOut := k32.NewProc("SetConsoleOutputCP")
	setIn := k32.NewProc("SetConsoleCP")
	setOut.Call(uintptr(65001))
	setIn.Call(uintptr(65001))
}

func runningDDSProcesses() []CompanionProcess {
	script := `[Console]::OutputEncoding=[Text.UTF8Encoding]::UTF8; Get-CimInstance Win32_Process -Filter "Name='DDS.exe' OR Name='DDSApp.exe'" | Select-Object ExecutablePath,CommandLine | ConvertTo-Json -Compress`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return nil
	}
	raw := strings.TrimSpace(string(out))
	var many []CompanionProcess
	if strings.HasPrefix(raw, "[") {
		_ = json.Unmarshal(out, &many)
		return many
	}
	var one CompanionProcess
	if json.Unmarshal(out, &one) == nil && one.ExecutablePath != "" {
		return []CompanionProcess{one}
	}
	return nil
}
