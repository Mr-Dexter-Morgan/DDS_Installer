package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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
