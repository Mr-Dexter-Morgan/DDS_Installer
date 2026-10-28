package main

import (
	"context"
	"strings"
	"testing"
)

func guiInventory(ds, bd, pl, cp State) Inventory {
	return Inventory{Discord: Component{Name: "Discord", State: ds}, BetterDiscord: Component{Name: "BetterDiscord", State: bd}, Plugin: Component{Name: "DDS Plugin", State: pl}, Companion: Component{Name: "DDS Companion", State: cp}, Profile: "installed"}
}
func TestGUISelectionRequiresDependency(t *testing.T) {
	i := guiInventory(StateMissing, StateMissing, StateMissing, StateMissing)
	for _, tc := range []struct {
		choices [4]bool
		substr  string
	}{
		{[4]bool{false, true, false, false}, "Discord"},
		{[4]bool{true, false, true, false}, "BetterDiscord"},
		{[4]bool{false, false, false, false}, "хотя бы один"},
	} {
		_, e := selectedSetupPlan(i, tc.choices)
		if e == nil || !strings.Contains(e.Error(), tc.substr) {
			t.Errorf("selection %v: expected %s, got %v", tc.choices, tc.substr, e)
		}
	}
}
func TestGUISelectionSkipsUserUncheckedComponents(t *testing.T) {
	i := guiInventory(StateMissing, StateMissing, StateMissing, StateMissing)
	got, e := selectedSetupPlan(i, [4]bool{true, false, false, false})
	if e != nil || len(got) != 1 || got[0] != "discord" {
		t.Fatalf("got %v, %v", got, e)
	}
}
func TestGUISelectionHealthyRequiresBDRepairStage(t *testing.T) {
	i := guiInventory(StateOK, StateOK, StateOK, StateOK)
	choices := [4]bool{true, true, true, true}
	plan, e := selectedSetupPlan(i, choices)
	if e != nil || len(plan) != 1 || plan[0] != "betterdiscord" {
		t.Fatalf("plan %v %v", plan, e)
	}
	report := executeSelectedFirstSetup(context.Background(), i, nil, firstSetupHooks{}, choices)
	if report.Status != "BLOCKED" {
		t.Fatalf("repair without verified CLI: %s", report.Status)
	}
}
func TestGUISelectionDoesNotPretendSkippedMissingIsInstalled(t *testing.T) {
	i := guiInventory(StateMissing, StateMissing, StateMissing, StateMissing)
	report := executeSelectedFirstSetup(context.Background(), i, nil, firstSetupHooks{}, [4]bool{false, false, false, true})
	if report.Status == "FILES_INTEGRATED_FIRST_START_PENDING" || report.Status == "SELECTED_FILES_INTEGRATED_FIRST_START_PENDING" {
		t.Fatal("must not report successful install without staged companion")
	}
}
