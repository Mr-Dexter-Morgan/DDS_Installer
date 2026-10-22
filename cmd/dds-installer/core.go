package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func runPluginApplyMenu(inv Inventory, input io.Reader, out io.Writer, stageRoot, appData string) {
	rec, _, target, needed, e := preparePluginApply(inv, stageRoot, appData)
	if e != nil {
		fmt.Fprintln(out, "НЕВОЗМОЖНО:", e)
		return
	}
	if !needed {
		fmt.Fprintf(out, "DDS Plugin %s уже соответствует проверенному staging. Файлы не изменялись.\n", rec.Version)
		return
	}
	if discordClientIsRunning() {
		fmt.Fprintln(out, "ОТМЕНА: Discord запущен. Закрой Discord перед изменением DDS Plugin.")
		return
	}
	fmt.Fprintf(out, "Только DDS Plugin %s -> %s\n", rec.Version, target)
	fmt.Fprintln(out, "Будет создана резервная копия прежнего DDS.plugin.js (если существует).")
	fmt.Fprintln(out, "Discord / BetterDiscord / Companion / DDS_Data / SQLite / DataRoot не изменяются.")
	if !confirmNumeric(input, out, "Установить проверенный DDS Plugin?") {
		fmt.Fprintln(out, "Отменено без изменений.")
		return
	}
	if discordClientIsRunning() {
		fmt.Fprintln(out, "ОТМЕНА: Discord запущен.")
		return
	}
	result, e := applyPlugin(inv, stageRoot, appData)
	if e != nil {
		fmt.Fprintln(out, "НЕ УДАЛОСЬ:", e)
		return
	}
	fmt.Fprintf(out, "%s: DDS Plugin %s\nПуть: %s\n", result.Status, result.Version, result.Target)
	if result.Backup != "" {
		fmt.Fprintln(out, "Резервная копия:", result.Backup)
	}
}

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

// Only expose bounded status/header diagnostics; do not print response bodies,
// access tokens or request URLs that may contain credentials.
func githubHTTPError(resp *http.Response) error {
	base := fmt.Sprintf("сервер вернул HTTP %d", resp.StatusCode)
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return fmt.Errorf("%s", base)
	}
	remaining := strings.TrimSpace(resp.Header.Get("X-RateLimit-Remaining"))
	reset := strings.TrimSpace(resp.Header.Get("X-RateLimit-Reset"))
	retry := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if remaining == "0" {
		if reset != "" {
			return fmt.Errorf("%s: GitHub API rate limit исчерпан; reset UNIX %s; повторные запросы не выполнялись", base, reset)
		}
		return fmt.Errorf("%s: GitHub API rate limit исчерпан; повторные запросы не выполнялись", base)
	}
	if retry != "" {
		return fmt.Errorf("%s: GitHub запросил паузу Retry-After=%s; повторные запросы не выполнялись", base, retry)
	}
	return fmt.Errorf("%s: причина неизвестна (возможны ограничения GitHub API или сети); повторные запросы не выполнялись", base)
}

type installerOwnership struct {
	Schema           string `json:"schema"`
	Root             string `json:"install_root"`
	Shortcut         string `json:"desktop_shortcut,omitempty"`
	Version          string `json:"installer_version"`
	PermissionPolicy string `json:"permission_policy,omitempty"`
}

func ownershipPath(local string) string {
	return filepath.Join(local, "DDS_Installer", "installed.json")
}

func readOwnership(local string) installerOwnership {
	var result installerOwnership
	b, err := os.ReadFile(ownershipPath(local))
	if err != nil || json.Unmarshal(b, &result) != nil || result.Schema != "dds-installer-ownership-v1" || !filepath.IsAbs(result.Root) {
		return installerOwnership{}
	}
	return result
}

func overlapsPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = strings.ToLower(filepath.Clean(a)), strings.ToLower(filepath.Clean(b))
	return a == b || strings.HasPrefix(a, b+string(os.PathSeparator)) || strings.HasPrefix(b, a+string(os.PathSeparator))
}

func validateProgramTarget(target, dataRoot string) error {
	if !filepath.IsAbs(target) || strings.TrimSpace(target) != target {
		return errors.New("Выбери полный локальный путь для программных файлов DDS")
	}
	target = filepath.Clean(target)
	if strings.HasPrefix(target, `\\`) || filepath.Dir(target) == target || overlapsPath(target, dataRoot) {
		return errors.New("Папка программы не должна пересекаться с архивом или быть корнем диска/сетевым путём")
	}
	// No new root may be published through an ancestor junction/symlink.
	for parent := filepath.Dir(target); ; parent = filepath.Dir(parent) {
		if err := requireNormalDir(parent); err != nil {
			return errors.New("Родительская папка недоступна или содержит ссылку: " + parent)
		}
		if filepath.Dir(parent) == parent {
			break
		}
	}
	return nil
}

func preferredInstallRoot(inv Inventory) string {
	if inv.Companion.Path != "" {
		return inv.Companion.Path
	}
	return defaultProgramRoot()
}

func saveOwnership(local string, record installerOwnership) error {
	path := ownershipPath(local)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".ownership-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(append(b, '\n'))
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return atomicReplaceFile(f.Name(), path)
}

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

// confirmNumeric is fail-closed: only the complete line "1" approves an
// operation. 0, Enter, words, malformed values, and input errors cancel it.
// Passing an existing *bufio.Reader preserves buffered menu input.
func confirmNumeric(input io.Reader, out io.Writer, action string) bool {
	fmt.Fprintf(out, "\n%s\n  1 — Подтвердить\n  0 — Отмена\nВыбор: ", action)
	answer, err := sharedBufferedReader(input).ReadString('\n')
	return err == nil && strings.TrimSpace(answer) == "1"
}
