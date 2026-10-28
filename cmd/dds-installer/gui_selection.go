package main

import (
	"context"
	"errors"
	"fmt"
)

// A selected BetterDiscord floor is always re-injected, even when files exist.
// Other healthy applications remain no-op; DDS Plugin is checked/enabled after BD.
// The GUI never silently installs an unchecked floor.
func selectedSetupPlan(inv Inventory, choices [4]bool) ([]string, error) {
	checked := inv
	// A partially present BD core is repairable by the explicitly selected CLI install.
	if choices[1] && checked.BetterDiscord.State == StatePartial {
		checked.BetterDiscord.State = StateMissing
		if checked.Plugin.State == StatePartial && finalVersionRE.MatchString(checked.Plugin.Version) {
			checked.Plugin.State = StateMissing
		}
	}
	if _, e := planFirstSetup(checked); e != nil {
		return nil, e
	}
	selected := []string{}
	if !choices[0] && !choices[1] && !choices[2] && !choices[3] {
		return nil, errors.New("Отметь хотя бы один компонент для установки")
	}
	if (choices[2] || choices[3]) && !choices[1] {
		return nil, errors.New("Для установки DDS отметь BetterDiscord: восстановление внедрения обязательно")
	}
	for i, key := range firstSetupOrder {
		if choices[i] && (key == "betterdiscord" || setupComponent(inv, key).State != StateOK) {
			selected = append(selected, key)
		}
	}
	if choices[1] && inv.BetterDiscord.State != StateOK && inv.Discord.State != StateOK && !choices[0] {
		return nil, errors.New("Для BetterDiscord необходим Discord: отметь установку Discord")
	}
	if choices[2] {
		if inv.Discord.State != StateOK && !choices[0] {
			return nil, errors.New("Для DDS Plugin необходим Discord: отметь установку Discord")
		}
		if !choices[1] {
			return nil, errors.New("Для DDS Plugin обязательно восстановление BetterDiscord: отметь BetterDiscord")
		}
	}
	return selected, nil
}
func executeSelectedFirstSetup(ctx context.Context, initial Inventory, prepared []stageOutcome, h firstSetupHooks, choices [4]bool) setupReport {
	report := setupReport{Status: "BLOCKED"}
	planned, e := selectedSetupPlan(initial, choices)
	if e != nil {
		report.Steps = append(report.Steps, setupStep{"preflight", "BLOCKED", e.Error()})
		return report
	}
	if len(planned) == 0 {
		report.Status = "ALREADY_HEALTHY_NO_CHANGE"
		return report
	}
	if e = stageReady(planned, prepared); e != nil {
		report.Steps = append(report.Steps, setupStep{"stage", "BLOCKED", e.Error()})
		return report
	}
	if h.Detect == nil || h.InstallDiscord == nil || h.InstallBetterDiscord == nil || h.InstallPlugin == nil || h.InstallCompanion == nil {
		report.Steps = append(report.Steps, setupStep{"preflight", "BLOCKED", "неполные операции Setup"})
		return report
	}
	for i, key := range firstSetupOrder {
		if !choices[i] {
			report.Steps = append(report.Steps, setupStep{key, "SKIPPED_BY_USER", "снята галочка"})
			continue
		}
		if ctx.Err() != nil {
			report.Steps = append(report.Steps, setupStep{key, "BLOCKED", ctx.Err().Error()})
			return report
		}
		inv := h.Detect()
		state := setupComponent(inv, key).State
		if state == StateOK && key != "betterdiscord" && key != "plugin" {
			report.Steps = append(report.Steps, setupStep{key, "ALREADY_PRESENT", "файлы не изменены"})
			continue
		}
		if key == "betterdiscord" && inv.Discord.State != StateOK || key == "plugin" && (inv.Discord.State != StateOK || inv.BetterDiscord.State != StateOK) {
			report.Steps = append(report.Steps, setupStep{key, "BLOCKED", "не готов необходимый компонент"})
			report.Status = "PARTIAL_OR_BLOCKED"
			return report
		}
		if key == "discord" && state != StateMissing || key == "betterdiscord" && state != StateOK && state != StatePartial && state != StateMissing && state != StateBlocked || key == "plugin" && state != StateOK && state != StateMissing && state != StateBlocked || key == "companion" && state != StateMissing && state != StateNotDetected {
			report.Steps = append(report.Steps, setupStep{key, "BLOCKED", "состояние компонента изменилось"})
			report.Status = "PARTIAL_OR_BLOCKED"
			return report
		}
		var err error
		switch key {
		case "discord":
			err = h.InstallDiscord(ctx)
		case "betterdiscord":
			err = h.InstallBetterDiscord(ctx)
		case "plugin":
			err = h.InstallPlugin(inv)
		case "companion":
			err = h.InstallCompanion(inv)
		}
		if err != nil {
			report.Steps = append(report.Steps, setupStep{key, "FAILED", err.Error()})
			report.Status = "PARTIAL_OR_BLOCKED"
			return report
		}
		if setupComponent(h.Detect(), key).State != StateOK {
			report.Steps = append(report.Steps, setupStep{key, "FAILED", "компонент не обнаружен после установки"})
			report.Status = "PARTIAL_OR_BLOCKED"
			return report
		}
		report.Steps = append(report.Steps, setupStep{key, "FILES_VALIDATED", "установлено и подтверждено"})
	}
	report.Status = "SELECTED_FILES_INTEGRATED_FIRST_START_PENDING"
	return report
}
func reportError(report setupReport) error {
	details := ""
	for _, s := range report.Steps {
		if s.Result == "BLOCKED" || s.Result == "FAILED" {
			details += s.Component + ": " + s.Reason + "; "
		}
	}
	return fmt.Errorf("установка не завершена (%s): %s", report.Status, details)
}
