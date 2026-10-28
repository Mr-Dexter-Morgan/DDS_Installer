//go:build windows

package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

func discordProcessStatus(ctx context.Context) (bool, error) {
	cmd := exec.CommandContext(ctx, "tasklist.exe", "/FI", "IMAGENAME eq Discord.exe", "/FO", "CSV", "/NH")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("tasklist: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return strings.Contains(strings.ToLower(string(out)), `"discord.exe"`), nil
}

func discordClientIsRunning() bool {
	// Fail closed, but shutdown itself diagnoses a probe failure separately.
	running, err := discordProcessStatus(context.Background())
	return err != nil || running
}
