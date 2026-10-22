//go:build windows

package main

import (
	"os/exec"
	"strings"
	"syscall"
)

func discordClientIsRunning() bool {
	// Fail closed if the process probe itself fails.
	cmd := exec.Command("tasklist.exe", "/FI", "IMAGENAME eq Discord.exe", "/FO", "CSV", "/NH")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	b, e := cmd.Output()
	if e != nil {
		return true
	}
	return strings.Contains(strings.ToLower(string(b)), `"discord.exe"`)
}
