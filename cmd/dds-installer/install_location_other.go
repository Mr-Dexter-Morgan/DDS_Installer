//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
)

func hideInstallerCommand(cmd *exec.Cmd) {}

func defaultProgramRoot() string { return filepath.Join(os.Getenv("ProgramFiles"), "DDS") }
