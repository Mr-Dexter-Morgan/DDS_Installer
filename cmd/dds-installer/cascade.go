package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type stageOutcome struct {
	Component string
	Version   string
	Result    string
	Location  string
	Reason    string
	SHA256    string
}

// Staging is independent from installation dependency resolution: a missing
// Discord installation does not prevent verifying the Plugin payload. No
// stage may mutate the live component's installation or user archive.
func planStage(inv Inventory, all bool) []string {
	result := []string{}
	for _, x := range []struct {
		key   string
		state State
	}{
		{"discord", inv.Discord.State}, {"betterdiscord", inv.BetterDiscord.State}, {"plugin", inv.Plugin.State}, {"companion", inv.Companion.State},
	} {
		if all || x.state != StateOK {
			result = append(result, x.key)
		}
	}
	return result
}

func runCascade(ctx context.Context, c *transferClient, stageRoot string, desired []string, sign func(string) error) []stageOutcome {
	outcomes := make([]stageOutcome, 0, len(desired))
	for _, key := range desired {
		o := stageOutcome{Component: key, Result: "FAILED"}
		c.UsedCachedStage = false
		fmt.Println("\n→ Этаж:", labelComponent(key), "— Acquire → Verify → Stage")
		c.DownloadProgress = newConsoleDownloadDisplay(os.Stdout).update
		var err error
		switch key {
		case "discord":
			var a preparedExternal
			a, o.Location, err = c.stageDiscord(ctx, stageRoot, sign)
			o.Version = a.Version
			o.SHA256 = a.SHA256
		case "betterdiscord", "plugin":
			var a preparedExternal
			a, o.Location, err = c.stageGitHubExternal(ctx, stageRoot, key)
			o.Version = a.Version
			o.SHA256 = a.SHA256
		case "companion":
			var a stageReceipt
			a, o.Location, err = c.stageCompanion(ctx, stageRoot)
			o.Version = a.Version
			o.SHA256 = a.SHA256
		default:
			err = fmt.Errorf("неизвестный этаж %q", key)
		}
		if err != nil {
			o.Reason = err.Error()
			fmt.Println("НЕ УДАЛОСЬ:", o.Reason)
			fmt.Println("Рабочие компоненты не затронуты; остальные этажи проверяются независимо.")
		} else {
			o.Result = "VERIFIED_AND_STAGED"
			if c.UsedCachedStage {
				o.Result = "VERIFIED_CACHED"
			}
			fmt.Printf("ГОТОВО: %s %s [%s] | SHA-256 %s\nStaging: %s\n", labelComponent(key), o.Version, o.Result, o.SHA256, o.Location)
		}
		outcomes = append(outcomes, o)
	}
	return outcomes
}

func labelComponent(k string) string {
	switch k {
	case "discord":
		return "Discord"
	case "betterdiscord":
		return "BetterDiscord CLI"
	case "plugin":
		return "DDS Plugin"
	case "companion":
		return "DDS Companion"
	}
	return k
}

func printCascadeSummary(outcomes []stageOutcome) {
	fmt.Println("\n", strings.Repeat("=", 72), "\nРЕЗУЛЬТАТ ПОДГОТОВКИ (УСТАНОВКА НЕ ВЫПОЛНЯЛАСЬ):")
	ok := 0
	for _, o := range outcomes {
		if o.Result == "VERIFIED_AND_STAGED" || o.Result == "VERIFIED_CACHED" {
			ok++
			fmt.Printf("  %s: %s %s\n", labelComponent(o.Component), o.Result, o.Version)
		} else {
			fmt.Printf("  %s: FAILED — %s\n", labelComponent(o.Component), o.Reason)
		}
	}
	fmt.Printf("Итого: %d/%d подготовлено. Рабочие файлы и DataRoot не изменены.\n", ok, len(outcomes))
	for _, o := range outcomes {
		if o.Result == "VERIFIED_CACHED" {
			fmt.Println("Примечание: VERIFIED_CACHED означает, что локальный staging повторно проверен и его версия+SHA-256 совпали с текущим Stable на GitHub; повторная загрузка пакета не выполнялась.")
			break
		}
	}
}

func selectStage(input io.Reader, out io.Writer) string {
	fmt.Fprintln(out, "\nДоступные операции (УСТАНОВКА ТОЛЬКО С ОТДЕЛЬНЫМ ПОДТВЕРЖДЕНИЕМ):")
	fmt.Fprintln(out, "  1 — ТОЛЬКО скачать и проверить 4 пакета (БЕЗ УСТАНОВКИ)")
	fmt.Fprintln(out, "  2 — Подготовить только отсутствующие/неисправные компоненты по плану")
	fmt.Fprintln(out, "  3 — DDS Plugin: установить проверенный staging (отдельное подтверждение)")
	fmt.Fprintln(out, "  4 — DDS Companion: проверить готовность к установке (ТОЛЬКО ПРОСМОТР)\n  5 — DDS Companion: установить проверенную новую версию (строгое подтверждение)")
	fmt.Fprintln(out, "  6 — DDS Companion: восстановить прерванную установку (строгое подтверждение)")
	fmt.Fprintln(out, "  7 — DDS Companion: первичная установка в новый каталог (строгое подтверждение)")
	fmt.Fprintln(out, "  8 — УСТАНОВИТЬ DDS: автоматически пройти все необходимые этапы")
	fmt.Fprintln(out, "  Enter — Выход без изменений")
	fmt.Fprint(out, "Выбор: ")
	text, _ := sharedBufferedReader(input).ReadString('\n')
	return strings.TrimSpace(text)
}

func runStageMenu(inv Inventory) {
	reader := bufio.NewReader(os.Stdin)
	mode := selectStage(reader, os.Stdout)
	if mode == "8" {
		exe, _ := os.Executable()
		runFirstSetupMenu(inv, reader, os.Stdout, os.Getenv("LOCALAPPDATA"), os.Getenv("APPDATA"), filepath.Dir(exe))
		return
	}
	if mode == "3" {
		runPluginApplyMenu(inv, reader, os.Stdout, filepath.Join(os.TempDir(), "DDS_Installer_Stage"), os.Getenv("APPDATA"))
		return
	}
	if mode == "4" {
		runCompanionPreview(inv, os.Stdout, filepath.Join(os.TempDir(), "DDS_Installer_Stage"))
		return
	}
	if mode == "5" {
		runCompanionApplyMenu(inv, reader, os.Stdout, filepath.Join(os.TempDir(), "DDS_Installer_Stage"), checkNoCompanionProcesses)
		return
	}
	if mode == "6" {
		runCompanionRecoveryMenu(inv, reader, os.Stdout, checkNoCompanionProcesses)
		return
	}
	if mode == "7" {
		runCompanionFreshMenu(inv, reader, os.Stdout, filepath.Join(os.TempDir(), "DDS_Installer_Stage"), os.Getenv("LOCALAPPDATA"), checkNoCompanionProcesses)
		return
	}
	if mode != "1" && mode != "2" {
		return
	}
	requested := planStage(inv, mode == "1")
	if len(requested) == 0 {
		fmt.Println("Все компоненты обнаружены: изменений и загрузок по плану не требуется.")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	c := newTransferClient()
	c.Progress = func(s string) { fmt.Println(s) }
	folder := filepath.Join(os.TempDir(), "DDS_Installer_Stage")
	fmt.Println("Источник: только официальные Stable; временный staging:", folder)
	fmt.Println("ЗАПРЕЩЕНО: запуск установщиков, замена компонентов, запись в DataRoot/SQLite/реестр.")
	outputs := runCascade(ctx, c, folder, requested, verifyDiscordSignature)
	printCascadeSummary(outputs)
}
