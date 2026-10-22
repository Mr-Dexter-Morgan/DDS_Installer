//go:build windows

package main

import (
	"errors"
	"os/exec"
	"strings"
	"syscall"
)

// A failed process query must NEVER be treated as 'DDS not running'.
// Also rejects DDS launched from other locations: conservative by design.
func checkNoCompanionProcesses() error {
	script := `$ErrorActionPreference='Stop'; $p=@(Get-CimInstance Win32_Process -Filter "Name='DDS.exe' OR Name='DDSApp.exe' OR Name='DDSUpdater.exe'"); if ($p.Count -gt 0) { Write-Output ('DDS_RUNNING: '+($p.Name -join ',')); exit 8 }; Write-Output 'DDS_CLOSED'`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.New("Companion запущен или проверка процессов недоступна (отказ в установке): " + strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(string(out)) != "DDS_CLOSED" {
		return errors.New("неоднозначный результат проверки процессов; установка запрещена")
	}
	return nil
}
