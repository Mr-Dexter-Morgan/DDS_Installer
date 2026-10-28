package main

import "fmt"

type finishHooks struct {
	WaitParent                       func() bool
	DiscordRunning, CompanionRunning func() bool
	StartDiscord, StartCompanion     func() error
}

// No launch happens without confirmed parent exit, and each choice is independent.
func finishHandoff(discord, companion bool, h finishHooks) []string {
	if !discord && !companion {
		return nil
	}
	if h.WaitParent == nil || !h.WaitParent() {
		return []string{"Завершение Installer не подтверждено; запуск отменён"}
	}
	var problems []string
	if discord && !h.DiscordRunning() {
		if err := h.StartDiscord(); err != nil {
			problems = append(problems, fmt.Sprintf("Discord: %v", err))
		}
	}
	if companion && !h.CompanionRunning() {
		if err := h.StartCompanion(); err != nil {
			problems = append(problems, fmt.Sprintf("Companion: %v", err))
		}
	}
	return problems
}
