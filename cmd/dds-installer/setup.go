package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

type DetectContext struct {
	InstallRoot  string
	LocalAppData string
	AppData      string
	InstallerDir string
	Processes    []CompanionProcess
}

type currentPointer struct {
	Version      string `json:"version"`
	RelativePath string `json:"relative_path"`
	Schema       string `json:"schema"`
}

var pluginVersionRE = regexp.MustCompile(`(?m)^\s*\*\s*@version\s+([^\s]+)\s*$`)

func exists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func detectDiscord(ctx DetectContext) Component {
	root := filepath.Join(ctx.LocalAppData, "Discord")
	update := filepath.Join(root, "Update.exe")
	apps, _ := filepath.Glob(filepath.Join(root, "app-*"))
	sort.Strings(apps)
	for i := len(apps) - 1; i >= 0; i-- {
		exe := filepath.Join(apps[i], "Discord.exe")
		if exists(exe) {
			ver := strings.TrimPrefix(filepath.Base(apps[i]), "app-")
			detail := "Stable"
			if !exists(update) {
				detail += "; Update.exe не найден"
				return Component{Name: "Discord", State: StatePartial, Version: ver, Path: exe, Detail: detail}
			}
			return Component{Name: "Discord", State: StateOK, Version: ver, Path: exe, Detail: detail}
		}
	}
	if dirExists(root) || exists(update) {
		return Component{Name: "Discord", State: StatePartial, Path: root, Detail: "папка найдена, рабочий Discord.exe не найден"}
	}
	return Component{Name: "Discord", State: StateMissing, Detail: "Stable не найден"}
}

func detectBetterDiscord(ctx DetectContext, discord Component) Component {
	if discord.State == StateMissing {
		return Component{Name: "BetterDiscord", State: StateBlocked, Detail: "требуется Discord"}
	}
	root := filepath.Join(ctx.AppData, "BetterDiscord")
	asar := filepath.Join(root, "data", "betterdiscord.asar")
	if exists(asar) {
		return Component{Name: "BetterDiscord", State: StateOK, Path: root, Detail: "core-файл найден"}
	}
	if dirExists(root) {
		return Component{Name: "BetterDiscord", State: StatePartial, Path: root, Detail: "папка есть, betterdiscord.asar не найден"}
	}
	return Component{Name: "BetterDiscord", State: StateMissing, Detail: "не найден"}
}

func detectPlugin(ctx DetectContext, bd Component) Component {
	pluginPath := filepath.Join(ctx.AppData, "BetterDiscord", "plugins", "DDS.plugin.js")
	if !exists(pluginPath) {
		if bd.State == StateBlocked || bd.State == StateMissing {
			return Component{Name: "DDS Plugin", State: StateBlocked, Path: pluginPath, Detail: "требуется BetterDiscord"}
		}
		return Component{Name: "DDS Plugin", State: StateMissing, Path: pluginPath, Detail: "DDS.plugin.js не найден"}
	}
	data, err := os.ReadFile(pluginPath)
	if err != nil {
		return Component{Name: "DDS Plugin", State: StatePartial, Path: pluginPath, Detail: "файл найден, версию прочитать не удалось"}
	}
	version := "unknown"
	if m := pluginVersionRE.FindSubmatch(data); len(m) == 2 {
		version = string(m[1])
	}
	if bd.State != StateOK {
		return Component{Name: "DDS Plugin", State: StatePartial, Version: version, Path: pluginPath, Detail: "файл есть, BetterDiscord не подтверждён как OK"}
	}
	return Component{Name: "DDS Plugin", State: StateOK, Version: version, Path: pluginPath, Detail: "DDS.plugin.js найден"}
}

func readCurrent(root string) (currentPointer, error) {
	var p currentPointer
	b, err := os.ReadFile(filepath.Join(root, "current.json"))
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, err
	}
	if strings.TrimSpace(p.Version) == "" {
		return p, errors.New("version missing")
	}
	return p, nil
}

func rootFromCompanionProcess(p CompanionProcess) string {
	exePath := filepath.Clean(strings.TrimSpace(p.ExecutablePath))
	if exePath == "" || !exists(exePath) {
		return ""
	}

	switch {
	case strings.EqualFold(filepath.Base(exePath), "DDS.exe"):
		root := filepath.Dir(exePath)
		if _, err := readCurrent(root); err == nil {
			return root
		}

	case strings.EqualFold(filepath.Base(exePath), "DDSApp.exe"):
		// The stable root launcher DDS.exe starts the versioned payload at
		// <root>\versions\<version>\DDSApp.exe and then the payload remains
		// as the long-running process. Walk back to the stable root and verify
		// that current.json actually points at this exact payload before accepting it.
		payloadDir := filepath.Dir(exePath)
		root := filepath.Dir(filepath.Dir(payloadDir))
		ptr, err := readCurrent(root)
		if err != nil || strings.TrimSpace(ptr.RelativePath) == "" {
			return ""
		}
		expected := filepath.Clean(filepath.Join(root, filepath.FromSlash(ptr.RelativePath), "DDSApp.exe"))
		if strings.EqualFold(expected, exePath) {
			return root
		}
	}

	return ""
}

func findCompanionRoot(ctx DetectContext) (string, CompanionProcess) {
	for _, p := range ctx.Processes {
		if root := rootFromCompanionProcess(p); root != "" {
			return root, p
		}
	}

	candidates := []string{
		ctx.InstallRoot,
		readOwnership(ctx.LocalAppData).Root,
		defaultProgramRoot(),
		ctx.InstallerDir,
		filepath.Join(ctx.LocalAppData, "DDS"),
		filepath.Join(ctx.LocalAppData, "Programs", "DDS"),
		filepath.Join(ctx.LocalAppData, "Programs", "DDS_Companion"),
	}
	seen := map[string]bool{}
	for _, root := range candidates {
		root = strings.TrimSpace(root)
		if root == "" || seen[strings.ToLower(root)] {
			continue
		}
		seen[strings.ToLower(root)] = true
		if exists(filepath.Join(root, "DDS.exe")) {
			if _, err := readCurrent(root); err == nil {
				return root, CompanionProcess{}
			}
		}
	}
	return "", CompanionProcess{}
}

func detectCompanion(ctx DetectContext) (Component, string) {
	root, proc := findCompanionRoot(ctx)
	if root == "" {
		return Component{Name: "DDS Companion", State: StateNotDetected, Detail: "не найден в известных расположениях и среди запущенных DDS.exe"}, "unknown"
	}
	ptr, err := readCurrent(root)
	if err != nil {
		return Component{Name: "DDS Companion", State: StatePartial, Path: root, Detail: "DDS.exe найден, current.json повреждён/не читается"}, "unknown"
	}
	exe := filepath.Join(root, "DDS.exe")
	if !exists(exe) {
		return Component{Name: "DDS Companion", State: StatePartial, Version: ptr.Version, Path: root, Detail: "current.json найден, DDS.exe отсутствует"}, "unknown"
	}

	profile := "installed"
	cmd := strings.ToLower(proc.CommandLine)
	if strings.Contains(cmd, "--portable") {
		profile = "portable"
	} else if dirExists(filepath.Join(root, "Data")) && !strings.EqualFold(root, filepath.Join(ctx.LocalAppData, "DDS")) && !strings.EqualFold(root, filepath.Join(ctx.LocalAppData, "Programs", "DDS")) {
		// When process command line is unavailable this is only a bounded hint.
		profile = "portable?"
	}
	return Component{Name: "DDS Companion", State: StateOK, Version: ptr.Version, Path: root, Detail: "DDS.exe + current.json"}, profile
}

func detectDataRoot(ctx DetectContext, companion Component, profile string) Component {
	installed := filepath.Join(ctx.LocalAppData, "DDS_Companion")
	if profile == "portable" && companion.Path != "" {
		portable := filepath.Join(companion.Path, "Data")
		if dirExists(portable) {
			return Component{Name: "DataRoot", State: StateOK, Path: portable, Detail: "portable · PRESERVED"}
		}
		return Component{Name: "DataRoot", State: StateMissing, Path: portable, Detail: "portable Data не найдена"}
	}
	if dirExists(installed) {
		return Component{Name: "DataRoot", State: StateOK, Path: installed, Detail: "installed · PRESERVED"}
	}
	if companion.Path != "" && dirExists(filepath.Join(companion.Path, "Data")) {
		portable := filepath.Join(companion.Path, "Data")
		return Component{Name: "DataRoot", State: StateOK, Path: portable, Detail: "portable candidate · PRESERVED"}
	}
	return Component{Name: "DataRoot", State: StateNotDetected, Detail: "не найден; Installer ничего не создавал"}
}

func buildPlan(inv *Inventory) {
	var plan []string
	switch inv.Discord.State {
	case StateMissing:
		plan = append(plan, "Установить Discord Stable")
	case StatePartial:
		plan = append(plan, "Восстановить Discord Stable")
	}

	if inv.Discord.State == StateOK || inv.Discord.State == StatePartial {
		switch inv.BetterDiscord.State {
		case StateMissing:
			plan = append(plan, "Установить BetterDiscord")
		case StatePartial:
			plan = append(plan, "Восстановить BetterDiscord")
		}
	} else if inv.BetterDiscord.State == StateBlocked {
		plan = append(plan, "После Discord установить BetterDiscord")
	}

	switch inv.Plugin.State {
	case StateMissing:
		plan = append(plan, "Установить DDS Plugin")
	case StateBlocked:
		plan = append(plan, "После BetterDiscord установить DDS Plugin")
	case StatePartial:
		plan = append(plan, "Проверить/восстановить DDS Plugin")
	}

	switch inv.Companion.State {
	case StateNotDetected, StateMissing:
		plan = append(plan, "Установить или указать существующий DDS Companion; DataRoot сохранить")
	case StatePartial:
		plan = append(plan, "Восстановить DDS Companion; DataRoot сохранить")
	}

	if len(plan) == 0 {
		plan = []string{"NONE — изменений не требуется"}
	} else {
		plan = append(plan, "Финальная валидация")
	}
	inv.Plan = plan
}

func Detect(ctx DetectContext) Inventory {
	d := detectDiscord(ctx)
	bd := detectBetterDiscord(ctx, d)
	p := detectPlugin(ctx, bd)
	c, profile := detectCompanion(ctx)
	dr := detectDataRoot(ctx, c, profile)
	inv := Inventory{Discord: d, BetterDiscord: bd, Plugin: p, Companion: c, DataRoot: dr, Profile: profile}
	buildPlan(&inv)
	return inv
}

func printInventory(inv Inventory) {
	fmt.Println("DDS Installer " + installerVersion + " — Обнаружение / Проверка / Подготовка")
	fmt.Println("Обнаружение безопасно. Операции 1/2 только готовят пакеты; 3 — Plugin Apply; 4 — Companion Preview; 5 — Companion Apply; 6 — Recovery; 7 — Companion Fresh; 8 — First Setup с отдельным подтверждением. Рабочие данные не удаляются.")
	fmt.Println(strings.Repeat("=", 72))
	fmt.Println(inv.Discord.summary())
	if inv.Discord.Path != "" {
		fmt.Println("  Путь:", inv.Discord.Path)
	}
	fmt.Println(inv.BetterDiscord.summary())
	if inv.BetterDiscord.Path != "" {
		fmt.Println("  Путь:", inv.BetterDiscord.Path)
	}
	fmt.Println(inv.Plugin.summary())
	if inv.Plugin.Path != "" {
		fmt.Println("  Путь:", inv.Plugin.Path)
	}
	fmt.Println(inv.Companion.summary())
	if inv.Companion.Path != "" {
		fmt.Println("  Путь:", inv.Companion.Path)
	}
	fmt.Println("Профиль Companion:", inv.Profile)
	fmt.Println(inv.DataRoot.summary())
	if inv.DataRoot.Path != "" {
		fmt.Println("  Путь:", inv.DataRoot.Path)
	}
	fmt.Println(strings.Repeat("-", 72))
	fmt.Println("Рекомендуемый план:")
	for i, step := range inv.Plan {
		if strings.HasPrefix(step, "NONE") {
			fmt.Println("  ", step)
		} else {
			fmt.Printf("  %d. %s\n", i+1, step)
		}
	}
	fmt.Println(strings.Repeat("=", 72))
	if inv.Companion.Path != "" {
		if pending, e := pendingCompanionTransactions(inv.Companion.Path); e != nil {
			fmt.Println("ВНИМАНИЕ: проверка незавершённой установки Companion не прошла:", e)
		} else if len(pending) > 0 {
			fmt.Println("RECOVERY_REQUIRED: обнаружена прерванная установка Companion; операция 6, без автоматических изменений.")
		}
	}
	fmt.Println("Обнаружение завершено. Рабочие компоненты и пользовательские данные не изменялись.")
}

func waitForEnter() {
	fmt.Println()
	fmt.Print("Нажмите Enter, чтобы закрыть DDS Setup...")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

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

type finishRuntimeHooks struct {
	DiscordRunning, CompanionRunning, Heartbeat, Archive func() bool
	Injection                                            func() error
}
type finishRuntimeReport struct {
	Discord, BetterDiscord, Plugin, Companion, Archive string
	Problems                                           []string
}

func (r finishRuntimeReport) Text() string {
	return strings.Join([]string{"Результат проверки после закрытия мастера:", "Discord: " + r.Discord, "BetterDiscord: " + r.BetterDiscord, "DDS Plugin: " + r.Plugin, "DDS Companion: " + r.Companion, "SQLite: " + r.Archive, "Отображение Plugin в статусе Companion проверяется в открытом приложении; полный LIVE PASS ещё не установлен."}, "\n")
}

// Processes, static injection, fresh plugin execution and archive presence are
// separate evidence. No daemon, no repair, no app launches inside this check.
func checkFinishRuntime(ctx context.Context, discord, companion bool, h finishRuntimeHooks) finishRuntimeReport {
	const skipped = "НЕ ПРОВЕРЯЛОСЬ — запуск не выбран"
	r := finishRuntimeReport{Discord: skipped, BetterDiscord: skipped, Plugin: skipped, Companion: skipped, Archive: skipped}
	if discord {
		r.Discord = "ЗАПУСК НЕ ПОДТВЕРЖДЁН"
		r.BetterDiscord = "РАБОТА НЕ ПОДТВЕРЖДЕНА"
		r.Plugin = "РАБОТА НЕ ПОДТВЕРЖДЕНА"
		if err := awaitSetupComponent(ctx, 40*time.Second, h.DiscordRunning); err != nil {
			r.Problems = append(r.Problems, "Discord: "+err.Error())
		} else {
			r.Discord = "ЗАПУЩЕНО"
			if err := h.Injection(); err != nil {
				r.Problems = append(r.Problems, err.Error())
			} else if err := awaitSetupComponent(ctx, 90*time.Second, h.Heartbeat); err != nil {
				r.BetterDiscord = "ВНЕДРЕНИЕ ПРОВЕРЕНО; загрузка не подтверждена"
				r.Problems = append(r.Problems, "Нет свежего heartbeat DDS после текущего запуска: "+err.Error())
			} else {
				// A newly executing DDS plugin is evidence of BD loading, not a process-only guess.
				r.BetterDiscord = "РАБОТАЕТ — подтверждено свежим heartbeat DDS"
				r.Plugin = "РАБОТАЕТ — свежий heartbeat текущего запуска"
			}
		}
	}
	if companion {
		r.Companion = "ЗАПУСК НЕ ПОДТВЕРЖДЁН"
		r.Archive = "НЕ ПОДТВЕРЖДЕНО"
		if err := awaitSetupComponent(ctx, 30*time.Second, h.CompanionRunning); err != nil {
			r.Problems = append(r.Problems, "Companion: "+err.Error())
		} else {
			r.Companion = "ЗАПУЩЕНО; обнаружение Plugin проверить в статусе приложения"
			if err := awaitSetupComponent(ctx, 20*time.Second, h.Archive); err != nil {
				r.Problems = append(r.Problems, fmt.Sprintf("SQLite: %v", err))
			} else {
				r.Archive = "ФАЙЛ АРХИВА ПОДТВЕРЖДЁН"
			}
		}
	}
	return r
}

// First-install integration: orchestrate independently validated floors and
// perform bounded startup verification. A failed floor stops its dependants; already healthy
// components are not rewritten. This is NOT the Repair/Uninstall workflow.

type setupStep struct {
	Component string
	Result    string
	Reason    string
}

type setupReport struct {
	Steps                       []setupStep
	Status                      string
	CompanionFirstStartVerified bool // not yet implemented by this floor
}

type firstSetupHooks struct {
	Detect               func() Inventory
	InstallDiscord       func(context.Context) error
	InstallBetterDiscord func(context.Context) error
	InstallPlugin        func(Inventory) error
	InstallCompanion     func(Inventory) error
}

var firstSetupOrder = []string{"discord", "betterdiscord", "plugin", "companion"}

func setupComponent(i Inventory, key string) Component {
	switch key {
	case "discord":
		return i.Discord
	case "betterdiscord":
		return i.BetterDiscord
	case "plugin":
		return i.Plugin
	case "companion":
		return i.Companion
	default:
		return Component{State: StatePartial}
	}
}

// Reject ambiguous, partially installed and Portable states rather than
// mistaking the first-install mode for permission to repair/migrate them.
func planFirstSetup(i Inventory) ([]string, error) {
	if strings.HasPrefix(i.Profile, "portable") {
		return nil, errors.New("Portable профиль: требуется отдельный профильный Setup/Repair; первичная Installed-установка запрещена")
	}
	for _, part := range []Component{i.Discord, i.BetterDiscord, i.Plugin, i.Companion} {
		if part.State == StatePartial {
			return nil, fmt.Errorf("%s: состояние PARTIAL; нужен отдельный Repair, не первичная установка", part.Name)
		}
	}
	if i.Discord.State != StateOK && i.Discord.State != StateMissing {
		return nil, errors.New("Discord: неоднозначное состояние")
	}
	if i.BetterDiscord.State != StateOK && i.BetterDiscord.State != StateMissing && i.BetterDiscord.State != StateBlocked {
		return nil, errors.New("BetterDiscord: неоднозначное состояние")
	}
	if i.Plugin.State != StateOK && i.Plugin.State != StateMissing && i.Plugin.State != StateBlocked {
		return nil, errors.New("Plugin: неоднозначное состояние")
	}
	if i.Companion.State != StateOK && i.Companion.State != StateMissing && i.Companion.State != StateNotDetected {
		return nil, errors.New("Companion: неоднозначное состояние")
	}
	if i.Discord.State == StateOK && i.BetterDiscord.State == StateBlocked {
		return nil, errors.New("BetterDiscord заблокирован при уже установленном Discord")
	}
	if i.BetterDiscord.State == StateOK && i.Plugin.State == StateBlocked {
		return nil, errors.New("Plugin заблокирован при уже установленном BetterDiscord")
	}
	need := make([]string, 0, 4)
	for _, key := range firstSetupOrder {
		if setupComponent(i, key).State != StateOK {
			need = append(need, key)
		}
	}
	return need, nil
}

func stageReady(need []string, got []stageOutcome) error {
	if len(got) != len(need) {
		return errors.New("подготовлены не все необходимые компоненты")
	}
	for n, key := range need {
		s := got[n]
		if s.Component != key || (s.Result != "VERIFIED_AND_STAGED" && s.Result != "VERIFIED_CACHED") {
			return fmt.Errorf("%s: нет подтверждённого проверенного staging: %s", key, s.Reason)
		}
	}
	return nil
}

func executeFirstSetup(ctx context.Context, initial Inventory, prepared []stageOutcome, h firstSetupHooks) setupReport {
	report := setupReport{Status: "BLOCKED"}
	need, err := planFirstSetup(initial)
	if err != nil {
		report.Steps = append(report.Steps, setupStep{"preflight", "BLOCKED", err.Error()})
		return report
	}
	if len(need) == 0 {
		report.Status = "ALREADY_HEALTHY_NO_CHANGE"
		return report
	}
	if err = stageReady(need, prepared); err != nil {
		report.Steps = append(report.Steps, setupStep{"stage", "BLOCKED", err.Error()})
		return report
	}
	if h.Detect == nil || h.InstallDiscord == nil || h.InstallBetterDiscord == nil || h.InstallPlugin == nil || h.InstallCompanion == nil {
		report.Steps = append(report.Steps, setupStep{"preflight", "BLOCKED", "неполный набор операций Setup"})
		return report
	}
	// No mutating hook can execute until all required packages passed staging.
	for _, key := range firstSetupOrder {
		if ctx.Err() != nil {
			report.Steps = append(report.Steps, setupStep{key, "BLOCKED", ctx.Err().Error()})
			return report
		}
		inv := h.Detect()
		state := setupComponent(inv, key).State
		if state == StateOK {
			report.Steps = append(report.Steps, setupStep{key, "ALREADY_PRESENT", "без изменений"})
			continue
		}
		if key == "betterdiscord" && inv.Discord.State != StateOK || key == "plugin" && (inv.Discord.State != StateOK || inv.BetterDiscord.State != StateOK) {
			report.Steps = append(report.Steps, setupStep{key, "BLOCKED", "зависимость не подтверждена после предыдущего шага"})
			return report
		}
		if key == "companion" && state != StateNotDetected && state != StateMissing {
			report.Steps = append(report.Steps, setupStep{key, "BLOCKED", "существующий/неоднозначный Companion; нужен Repair"})
			return report
		}
		if key == "discord" && state != StateMissing || key == "betterdiscord" && state != StateMissing && state != StateBlocked || key == "plugin" && state != StateMissing && state != StateBlocked {
			report.Steps = append(report.Steps, setupStep{key, "BLOCKED", "изменилось состояние; требуется новое планирование"})
			return report
		}
		var e error
		switch key {
		case "discord":
			e = h.InstallDiscord(ctx)
		case "betterdiscord":
			e = h.InstallBetterDiscord(ctx)
		case "plugin":
			e = h.InstallPlugin(inv)
		case "companion":
			e = h.InstallCompanion(inv)
		}
		if e != nil {
			report.Steps = append(report.Steps, setupStep{key, "FAILED", e.Error()})
			report.Status = "PARTIAL_OR_BLOCKED"
			return report
		}
		fresh := h.Detect()
		if setupComponent(fresh, key).State != StateOK {
			report.Steps = append(report.Steps, setupStep{key, "FAILED", "операция завершилась, но установленный компонент не подтверждён повторным обнаружением"})
			report.Status = "PARTIAL_OR_BLOCKED"
			return report
		}
		report.Steps = append(report.Steps, setupStep{key, "FILES_VALIDATED", "повторное обнаружение OK"})
	}
	report.Status = "FILES_INTEGRATED_FIRST_START_PENDING"
	// Neither a detected DDS.exe nor a copied file proves startup/GUI/DataRoot.
	return report
}

// Compare the exact approved receipt with the staged bytes once more before
// an external process or product Apply is allowed to touch installed files.
func verifyExactSetupStage(root string, expected stageOutcome) error {
	if err := requireNormalDir(root); err != nil {
		return err
	}
	var sha, version, path string
	switch expected.Component {
	case "discord":
		file, e := verifiedDiscordStageFile(root, verifyDiscordSignature)
		if e != nil {
			return e
		}
		h, _, e := shaFile(file)
		if e != nil {
			return e
		}
		sha = h
		version = "official Stable x64 (installer)"
		path = filepath.Dir(filepath.Dir(file))
	case "betterdiscord", "plugin":
		var rec preparedExternal
		var ok bool
		if expected.Component == "betterdiscord" {
			rec, path, ok = validExternalCache(root, "betterdiscord", false)
			if !ok {
				return errors.New("BetterDiscord CLI staging не прошёл повторную проверку")
			}
		} else {
			var payload string
			var e error
			rec, payload, e = verifiedPluginStage(root)
			if e != nil {
				return e
			}
			path = filepath.Dir(filepath.Dir(payload))
		}
		sha, version = rec.SHA256, rec.Version
	case "companion":
		rec, p, ok := validCompanionCache(root, false)
		if !ok {
			return errors.New("Companion staging не прошёл повторную проверку")
		}
		sha, version, path = rec.SHA256, rec.Version, p
	default:
		return errors.New("неизвестный компонент в пакете Setup")
	}
	if !strings.EqualFold(path, expected.Location) || !strings.EqualFold(sha, expected.SHA256) || version != expected.Version {
		return fmt.Errorf("%s: изменился проверенный кандидат с момента подтверждения", expected.Component)
	}
	return nil
}

func preparedStage(got []stageOutcome, key string) stageOutcome {
	for _, s := range got {
		if s.Component == key {
			return s
		}
	}
	return stageOutcome{}
}

func verifiedDiscordStageFile(root string, sign func(string) error) (string, error) {
	if sign == nil {
		return "", errors.New("отсутствует проверка цифровой подписи Discord")
	}
	for _, dir := range []string{root, filepath.Join(root, "discord"), filepath.Join(root, "discord", "official-stable-x64"), filepath.Join(root, "discord", "official-stable-x64", "package")} {
		if err := requireNormalDir(dir); err != nil {
			return "", err
		}
	}
	d := filepath.Join(root, "discord", "official-stable-x64")
	receiptPath := filepath.Join(d, "stage_receipt.json")
	if err := requireNormalFile(receiptPath); err != nil {
		return "", err
	}
	b, err := os.ReadFile(receiptPath)
	if err != nil {
		return "", err
	}
	var r preparedExternal
	if json.Unmarshal(b, &r) != nil || r.Schema != "dds-installer-external-stage-v1" || r.Component != "Discord" || r.Source != discordInstallerURL || r.Status != "VERIFIED_AND_STAGED" || r.Version != "official Stable x64 (installer)" || !hexDigestRE.MatchString(r.SHA256) || r.ZIPSize <= 0 || r.ZIPSize > externalMaxSize {
		return "", errors.New("неверный Discord staging receipt")
	}
	target := filepath.Join(d, "package", "DiscordSetup.exe")
	if err := requireNormalFile(target); err != nil {
		return "", err
	}
	actual, n, err := shaFile(target)
	if err != nil {
		return "", err
	}
	if actual != r.SHA256 || n != r.ZIPSize {
		return "", errors.New("Discord staging SHA-256/size mismatch")
	}
	if err := verifyWindowsPE(target); err != nil {
		return "", err
	}
	if err := sign(target); err != nil {
		return "", fmt.Errorf("Discord Authenticode: %w", err)
	}
	return target, nil
}

func verifiedBetterDiscordCLIFile(root string) (string, error) {
	_, path, ok := validExternalCache(root, "betterdiscord", false)
	if !ok {
		return "", errors.New("нет проверенного BetterDiscord CLI staging")
	}
	exe := filepath.Join(path, "payload", "bdcli.exe")
	if err := requireNormalFile(exe); err != nil {
		return "", err
	}
	return exe, nil
}

// After an external installer exits, wait for the actual installed state:
// many Windows installers spawn an asynchronous child and exit first.
func awaitSetupComponent(ctx context.Context, maxWait time.Duration, confirmed func() bool) error {
	timeout, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if confirmed() {
			return nil
		}
		select {
		case <-timeout.Done():
			return fmt.Errorf("компонент не обнаружен после установки: %w", timeout.Err())
		case <-ticker.C:
		}
	}
}

// Never buffer unlimited stdout from a downloaded external tool.
type boundedOutput struct{ last []byte }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	b.last = append(b.last, p...)
	if len(b.last) > 4096 {
		b.last = append([]byte(nil), b.last[len(b.last)-4096:]...)
	}
	return n, nil
}

func externalRun(ctx context.Context, exe string, args ...string) error {
	if runtime.GOOS != "windows" {
		return errors.New("Windows external installer only")
	}
	if err := requireNormalFile(exe); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, exe, args...)
	hideInstallerCommand(cmd)
	// Bounded output, no shell interpolation, and no ambient stdin prompts.
	output := &boundedOutput{}
	cmd.Stdout, cmd.Stderr = output, output
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("внешний установщик завершился с ошибкой: %v: %s", err, strings.TrimSpace(string(output.last)))
	}
	return nil
}

// Run the official installer, not an invented undocumented silent flag. A
// Squirrel-style updater may detach; a later Detect is authoritative.
func launchVerifiedDiscord(ctx context.Context, root string) error {
	p, err := verifiedDiscordStageFile(root, verifyDiscordSignature)
	if err != nil {
		return err
	}
	return externalRun(ctx, p)
}
func launchVerifiedBetterDiscord(ctx context.Context, root string) error {
	p, err := verifiedBetterDiscordCLIFile(root)
	if err != nil {
		return err
	}
	// Official BetterDiscord CLI docs, Stable only, no --dev or Canary.
	return externalRun(ctx, p, "--silent", "install", "--channel", "stable")
}

// Final status describes files only; live Discord/Plugin heartbeat and real
// Companion first start must be validated by later Windows acceptance gates.
func printFirstSetupReport(w io.Writer, r setupReport) {
	fmt.Fprintln(w, "\nDDS Setup — итоги по компонентам:")
	for _, s := range r.Steps {
		fmt.Fprintf(w, "  %s: %s (%s)\n", labelComponent(s.Component), s.Result, s.Reason)
	}
	fmt.Fprintln(w, "FILES STATUS:", r.Status)
	if r.Status == "FILES_INTEGRATED_FIRST_START_PENDING" {
		fmt.Fprintln(w, "Файлы интегрированы; запускаем Discord и Companion и ожидаем heartbeat.")
	}
}

// First Setup automatically handles Discord lifecycle after the user's
// one explicit approval for the complete installation.
func sharedBufferedReader(r io.Reader) *bufio.Reader {
	if b, ok := r.(*bufio.Reader); ok {
		return b
	}
	return bufio.NewReader(r)
}

func setupInventoryNow(localAppData, appData, installerDir string) Inventory {
	return Detect(DetectContext{LocalAppData: localAppData, AppData: appData, InstallerDir: installerDir, Processes: runningDDSProcesses()})
}

func runFirstSetupMenu(initial Inventory, input io.Reader, out io.Writer, localAppData, appData, installerDir string) {
	plan, err := planFirstSetup(initial)
	if err != nil {
		fmt.Fprintln(out, "SETUP BLOCKED:", err)
		return
	}
	if len(plan) == 0 {
		fmt.Fprintln(out, "ALREADY_HEALTHY_NO_CHANGE: переписывать ничего не требуется")
		return
	}
	if !filepath.IsAbs(localAppData) || !filepath.IsAbs(appData) || requireNormalDir(localAppData) != nil || requireNormalDir(appData) != nil {
		fmt.Fprintln(out, "SETUP BLOCKED: Windows LOCALAPPDATA/APPDATA не определены безопасно")
		return
	}
	fmt.Fprintln(out, "DDS Setup — ПЕРВИЧНАЯ УСТАНОВКА Installed (не Repair/Portable):")
	for _, n := range plan {
		fmt.Fprintln(out, "  →", labelComponent(n))
	}
	fmt.Fprintln(out, "Сначала будут скачаны и проверены ВСЕ нужные пакеты, затем потребуется отдельное подтверждение установки.")
	reader := sharedBufferedReader(input)
	if !confirmNumeric(reader, out, "Скачать и проверить необходимые пакеты?") {
		fmt.Fprintln(out, "Отменено без изменений")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	stageRoot := filepath.Join(os.TempDir(), "DDS_Installer_Stage")
	c := newTransferClient()
	c.Progress = func(msg string) { fmt.Fprintln(out, msg) }
	prepared := runCascade(ctx, c, stageRoot, plan, verifyDiscordSignature)
	if err = stageReady(plan, prepared); err != nil {
		fmt.Fprintln(out, "SETUP BLOCKED: staging не завершён:", err)
		return
	}
	// All four checked receipts must still match the exact candidates the user
	// just downloaded, not an unapproved newer cached package.
	for _, candidate := range prepared {
		if e := verifyExactSetupStage(stageRoot, candidate); e != nil {
			fmt.Fprintln(out, "SETUP BLOCKED: проверка конкретного пакета:", e)
			return
		}
	}
	// Re-detection prevents approving an obsolete plan after long downloads.
	fresh := setupInventoryNow(localAppData, appData, installerDir)
	currentPlan, e := planFirstSetup(fresh)
	if e != nil || strings.Join(currentPlan, ",") != strings.Join(plan, ",") {
		fmt.Fprintln(out, "SETUP BLOCKED: состояние изменилось во время подготовки; повтори запуск")
		return
	}
	fmt.Fprintln(out, "Пакеты проверены. Installer сам закроет Discord перед BetterDiscord/Plugin и затем запустит Discord и DDS Companion.")
	fmt.Fprintln(out, "Discord может закрыться принудительно, если не завершится штатно. Сохрани черновики и выйди из голосового чата до подтверждения.")
	fmt.Fprintln(out, "DDS Plugin будет скопирован с проверкой и резервной копией; Companion только в НОВУЮ папку %LOCALAPPDATA%\\DDS.")
	fmt.Fprintln(out, "Рабочий DDS, SQLite, DDS_Data и DataRoot нельзя переносить/перезаписывать этим режимом.")
	if !confirmNumeric(reader, out, "Установить перечисленные компоненты на этот компьютер?") {
		fmt.Fprintln(out, "Установка отменена; подготовленные пакеты остаются в TEMP")
		return
	}
	hooks := firstSetupHooks{
		Detect: func() Inventory { return setupInventoryNow(localAppData, appData, installerDir) },
		InstallDiscord: func(ctx context.Context) error {
			if err := verifyExactSetupStage(stageRoot, preparedStage(prepared, "discord")); err != nil {
				return err
			}
			if err := launchVerifiedDiscord(ctx, stageRoot); err != nil {
				return err
			}
			return awaitSetupComponent(ctx, 90*time.Second, func() bool {
				return setupInventoryNow(localAppData, appData, installerDir).Discord.State == StateOK
			})
		},
		InstallBetterDiscord: func(ctx context.Context) error {
			if err := ensureDiscordStopped(ctx, discordClientIsRunning, stopDiscordForSetup); err != nil {
				return err
			}
			if err := verifyExactSetupStage(stageRoot, preparedStage(prepared, "betterdiscord")); err != nil {
				return err
			}
			if err := launchVerifiedBetterDiscord(ctx, stageRoot); err != nil {
				return err
			}
			return awaitSetupComponent(ctx, 30*time.Second, func() bool {
				return setupInventoryNow(localAppData, appData, installerDir).BetterDiscord.State == StateOK
			})
		},
		InstallPlugin: func(inv Inventory) error {
			if err := verifyExactSetupStage(stageRoot, preparedStage(prepared, "plugin")); err != nil {
				return err
			}
			if err := ensureDiscordStopped(ctx, discordClientIsRunning, stopDiscordForSetup); err != nil {
				return err
			}
			result, err := applyPlugin(inv, stageRoot, appData)
			if err != nil {
				return err
			}
			if result.Status == "ALREADY_CURRENT" {
				return nil
			}
			return enableNewDDSPlugin(appData)
		},
		InstallCompanion: func(inv Inventory) error {
			if err := verifyExactSetupStage(stageRoot, preparedStage(prepared, "companion")); err != nil {
				return err
			}
			result, err := freshCompanionInstall(inv, stageRoot, localAppData, checkNoCompanionProcesses)
			if err != nil {
				return err
			}
			if result.Status != "FILES_INSTALLED_UNSTARTED" {
				return errors.New("Companion не подтвердил установку файлов")
			}
			return nil
		},
	}
	report := executeFirstSetup(ctx, fresh, prepared, hooks)
	printFirstSetupReport(out, report)
	if report.Status != "FILES_INTEGRATED_FIRST_START_PENDING" {
		return
	}
	firstStart := startupHooks{
		StartDiscord:     func() error { return startInstalledDiscord(localAppData, appData, installerDir) },
		StartCompanion:   func() error { return startInstalledCompanion(localAppData) },
		DiscordRunning:   discordClientIsRunning,
		CompanionRunning: companionClientIsRunning,
		PluginHeartbeat:  func() bool { return freshPluginHeartbeat(appData) },
		ArchiveReady:     func() bool { return sqliteHeaderPresent(localAppData) },
	}
	if err := runSetupFirstStart(ctx, out, firstStart); err != nil {
		fmt.Fprintln(out, "STATUS: FIRST_START_INCOMPLETE —", err)
		fmt.Fprintln(out, "Установленные файлы сохранены; не объявляем DDS работающим до успешного heartbeat.")
	} else {
		fmt.Fprintln(out, "STATUS: RUNTIME_READY — Discord, Companion, DDS Plugin heartbeat и SQLite подтверждены")
	}

}

// First-install only: keeps other BetterDiscord addon choices untouched.
// BetterDiscord's AddonManager reads plugins.json into state keyed by @name.
func ddsPluginEnabled(appData string) (bool, error) {
	path := filepath.Join(appData, "BetterDiscord", "data", "stable", "plugins.json")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return false, nil
	}
	if err := requireNormalFile(path); err != nil {
		return false, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var state map[string]json.RawMessage
	if json.Unmarshal(b, &state) != nil || state == nil {
		return false, errors.New("Не удалось прочитать состояние плагинов BetterDiscord")
	}
	raw, exists := state["DDS"]
	if !exists {
		return false, nil
	}
	var active bool
	if json.Unmarshal(raw, &active) != nil {
		return false, errors.New("Неверное состояние DDS в BetterDiscord")
	}
	return active, nil
}

func enableNewDDSPlugin(appData string) error {
	if !filepath.IsAbs(appData) || requireNormalDir(appData) != nil {
		return errors.New("APPDATA недоступна")
	}
	bd := filepath.Join(appData, "BetterDiscord")
	if requireNormalDir(bd) != nil || requireNormalFile(filepath.Join(bd, "plugins", "DDS.plugin.js")) != nil {
		return errors.New("DDS Plugin не найден в BetterDiscord")
	}
	stable := filepath.Join(bd, "data", "stable")
	if err := requireNormalDir(filepath.Join(bd, "data")); err != nil {
		return fmt.Errorf("BetterDiscord data: %w", err)
	}
	if err := os.MkdirAll(stable, 0700); err != nil {
		return err
	}
	if err := requireNormalDir(stable); err != nil {
		return err
	}
	path := filepath.Join(stable, "plugins.json")
	state := map[string]json.RawMessage{}
	if _, err := os.Lstat(path); err == nil {
		if err := requireNormalFile(path); err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &state); err != nil {
			return fmt.Errorf("неверный plugins.json: %w", err)
		}
		if state == nil {
			return errors.New("plugins.json должен содержать JSON-объект")
		}
		if v, ok := state["DDS"]; ok {
			var enabled bool
			if err := json.Unmarshal(v, &enabled); err != nil {
				return fmt.Errorf("DDS настройка не является bool: %w", err)
			}
			if enabled {
				return nil
			}
			// In first Setup the user approved enabling DDS; all other plugin choices remain unchanged.
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	state["DDS"] = json.RawMessage("true")
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	tmp, err := os.CreateTemp(stable, ".dds-plugins-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(content)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// A safe first-install path must never replace a symlink or user data file.
	if err := atomicReplaceFile(tmpPath, path); err != nil {
		return err
	}
	return nil
}

func ensureDiscordStopped(ctx context.Context, running func() bool, stop func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !running() {
		return nil
	}
	if err := stop(ctx); err != nil {
		return fmt.Errorf("не удалось закрыть Discord: %w", err)
	}
	return awaitSetupComponent(ctx, 20*time.Second, func() bool { return !running() })
}

type startupHooks struct {
	StartDiscord     func() error
	StartCompanion   func() error
	DiscordRunning   func() bool
	CompanionRunning func() bool
	PluginHeartbeat  func() bool
	ArchiveReady     func() bool
}

func runSetupFirstStart(ctx context.Context, out io.Writer, h startupHooks) error {
	if h.StartDiscord == nil || h.StartCompanion == nil || h.DiscordRunning == nil || h.CompanionRunning == nil || h.PluginHeartbeat == nil || h.ArchiveReady == nil {
		return errors.New("неполная конфигурация запуска")
	}
	fmt.Fprintln(out, "Первый запуск: Discord → DDS Companion → проверка DDS Plugin")
	if !h.DiscordRunning() {
		if err := h.StartDiscord(); err != nil {
			return fmt.Errorf("Discord не запущен: %w", err)
		}
	}
	if err := awaitSetupComponent(ctx, 40*time.Second, h.DiscordRunning); err != nil {
		return fmt.Errorf("Discord: %w", err)
	}
	if !h.CompanionRunning() {
		if err := h.StartCompanion(); err != nil {
			return fmt.Errorf("DDS Companion не запущен: %w", err)
		}
	}
	if err := awaitSetupComponent(ctx, 30*time.Second, h.CompanionRunning); err != nil {
		return fmt.Errorf("DDS Companion: %w", err)
	}
	if err := awaitSetupComponent(ctx, 90*time.Second, h.PluginHeartbeat); err != nil {
		return fmt.Errorf("DDS Plugin heartbeat не появился: %w", err)
	}
	if err := awaitSetupComponent(ctx, 20*time.Second, h.ArchiveReady); err != nil {
		return fmt.Errorf("SQLite архив не подтверждён: %w", err)
	}
	return nil
}

func freshPluginHeartbeat(appData string) bool {
	return freshPluginHeartbeatSince(appData, "0.5.3", time.Time{}, time.Now())
}

func freshPluginHeartbeatSince(appData, version string, since, now time.Time) bool {
	path := filepath.Join(appData, "BetterDiscord", "DDS_Data", "plugin_heartbeat.json")
	if requireNormalFile(path) != nil {
		return false
	}
	st, err := os.Stat(path)
	if err != nil || st.Size() > 64<<10 {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var h struct {
		State         string `json:"state"`
		UpdatedAt     string `json:"updatedAt"`
		PluginVersion string `json:"pluginVersion"`
	}
	if json.Unmarshal(b, &h) != nil || h.State != "RUNNING" || version == "" || h.PluginVersion != version {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, h.UpdatedAt)
	if err != nil || at.Before(since) {
		return false
	}
	age := now.Sub(at)
	return age >= 0 && age <= 75*time.Second
}

func sqliteHeaderPresent(localAppData string) bool {
	path := filepath.Join(localAppData, "DDS_Companion", "database", "dds.sqlite3")
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 16)
	_, err = io.ReadFull(f, b)
	return err == nil && strings.HasPrefix(string(b), "SQLite format 3\x00")
}
