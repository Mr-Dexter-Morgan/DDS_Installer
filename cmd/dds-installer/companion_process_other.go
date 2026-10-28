//go:build !windows

package main

// Called only by sandbox tests; production Apply is Windows-only.
func checkNoCompanionProcesses() error { return nil }
