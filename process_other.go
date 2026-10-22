//go:build !windows

package main

func enableUTF8Console()                      {}
func runningDDSProcesses() []CompanionProcess { return nil }
