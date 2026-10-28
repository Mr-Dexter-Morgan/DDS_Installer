//go:build !windows

package main

func discordClientIsRunning() bool { return false } // tests only; product binary is Windows-only
