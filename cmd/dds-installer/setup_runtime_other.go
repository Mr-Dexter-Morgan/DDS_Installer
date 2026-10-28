//go:build !windows

package main

import (
	"context"
	"errors"
	"os"
)

func stopDiscordForSetup(context.Context) error          { return errors.New("Windows only") }
func startInstalledDiscord(string, string, string) error { return errors.New("Windows only") }
func startInstalledCompanion(string) error               { return errors.New("Windows only") }
func companionClientIsRunning() bool                     { return false }

func atomicReplaceFile(src, dst string) error { return os.Rename(src, dst) }
