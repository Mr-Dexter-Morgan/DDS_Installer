package main

import "strings"

type State string

const (
	StateOK          State = "OK"
	StateMissing     State = "MISSING"
	StateBlocked     State = "BLOCKED"
	StatePartial     State = "PARTIAL"
	StateNotDetected State = "NOT_DETECTED"
)

type Component struct {
	Name    string
	State   State
	Version string
	Path    string
	Detail  string
}

type CompanionProcess struct {
	ExecutablePath string
	CommandLine    string
}

type Inventory struct {
	Discord       Component
	BetterDiscord Component
	Plugin        Component
	Companion     Component
	DataRoot      Component
	Profile       string
	Plan          []string
}

func (c Component) summary() string {
	b := strings.Builder{}
	b.WriteString(c.Name)
	b.WriteString(": ")
	b.WriteString(string(c.State))
	if c.Version != "" {
		b.WriteString(" · ")
		b.WriteString(c.Version)
	}
	if c.Detail != "" {
		b.WriteString(" · ")
		b.WriteString(c.Detail)
	}
	return b.String()
}
