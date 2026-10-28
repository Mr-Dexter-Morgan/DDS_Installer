//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// guiFirstSetup is the already-proven r3 first-install sequence without console
// confirmations or the r3 auto-run. User approval is the GUI "Установить" click.
// Nothing is launched after file installation until the GUI Finish action.
func guiFirstSetup(ctx context.Context, localAppData, appData, installerDir, target string, shortcut bool, log func(string, int), download func(downloadProgress), choices [4]bool) (setupReport, error) {
	report := setupReport{Status: "BLOCKED"}
	if !filepath.IsAbs(localAppData) || !filepath.IsAbs(appData) || requireNormalDir(localAppData) != nil || requireNormalDir(appData) != nil {
		return report, errors.New("Недоступны каталоги Windows LOCALAPPDATA / APPDATA")
	}
	detect := func() Inventory { return setupInventoryAt(localAppData, appData, installerDir, target) }
	initial := detect()
	if initial.Companion.Path != "" && !strings.EqualFold(filepath.Clean(target), filepath.Clean(initial.Companion.Path)) {
		return report, errors.New("Существующая установка сохранена. Перенос в другую папку требует отдельной миграции")
	}
	if choices[3] && initial.Companion.State != StateOK {
		if e := validateProgramTarget(target, filepath.Join(localAppData, "DDS_Companion")); e != nil {
			return report, e
		}
	}
	plan, err := selectedSetupPlan(initial, choices)
	if err != nil {
		return report, err
	}
	if len(plan) == 0 {
		if choices[2] {
			if e := ensureSelectedDDSPlugin(ctx, appData); e != nil {
				return report, e
			}
		}
		if choices[3] {
			if e := registerWindowsInstall(localAppData, initial.Companion.Path, shortcut); e != nil {
				return report, e
			}
		}
		log("Все компоненты уже установлены — файлы не изменены", 100)
		return setupReport{Status: "ALREADY_HEALTHY_NO_CHANGE"}, nil
	}
	log("Скачиваем и проверяем необходимые компоненты", 7)
	stageRoot := filepath.Join(os.TempDir(), "DDS_Installer_Stage")
	c := newTransferClient()
	c.DownloadProgress = download
	c.Progress = func(s string) {
		if strings.TrimSpace(s) != "" {
			log(s, -1)
		}
	}
	prepared := runCascade(ctx, c, stageRoot, plan, verifyDiscordSignature)
	if err = stageReady(plan, prepared); err != nil {
		return report, fmt.Errorf("Подготовка пакетов не завершена: %w", err)
	}
	log("Все пакеты скачаны; повторная проверка целостности", 42)
	for _, candidate := range prepared {
		if e := verifyExactSetupStage(stageRoot, candidate); e != nil {
			return report, fmt.Errorf("Изменился проверенный пакет %s: %w", candidate.Component, e)
		}
	}
	fresh := detect()
	currentPlan, e := selectedSetupPlan(fresh, choices)
	if e != nil || strings.Join(currentPlan, ",") != strings.Join(plan, ",") {
		return report, errors.New("Состояние Windows изменилось во время подготовки; запуск установки остановлен")
	}
	// Report non-atomic, real progress for each floor, not invented download percentages.
	hooks := firstSetupHooks{
		Detect: detect,
		InstallDiscord: func(ctx context.Context) error {
			log("Устанавливаем Discord", 52)
			if e := verifyExactSetupStage(stageRoot, preparedStage(prepared, "discord")); e != nil {
				return e
			}
			if e := launchVerifiedDiscord(ctx, stageRoot); e != nil {
				return e
			}
			return awaitSetupComponent(ctx, 90*time.Second, func() bool { return detect().Discord.State == StateOK })
		},
		InstallBetterDiscord: func(ctx context.Context) error {
			log("Закрываем Discord и восстанавливаем внедрение BetterDiscord", 65)
			if e := verifyExactSetupStage(stageRoot, preparedStage(prepared, "betterdiscord")); e != nil {
				return e
			}
			return repairBetterDiscord(ctx, discordClientIsRunning, stopDiscordForSetup,
				func() error { return launchVerifiedBetterDiscord(ctx, stageRoot) },
				func() error { return verifyBetterDiscordInjection(detect().Discord, appData) })
		},
		InstallPlugin: func(inv Inventory) error {
			log("Проверяем и включаем DDS Plugin", 77)
			if inv.Plugin.State == StateOK {
				return ensureSelectedDDSPlugin(ctx, appData)
			}
			if e := ensureDiscordStopped(ctx, discordClientIsRunning, stopDiscordForSetup); e != nil {
				return e
			}
			if e := verifyExactSetupStage(stageRoot, preparedStage(prepared, "plugin")); e != nil {
				return e
			}
			result, e := applyPlugin(inv, stageRoot, appData)
			if e != nil {
				return e
			}
			_ = result
			return ensureSelectedDDSPlugin(ctx, appData)
		},
		InstallCompanion: func(inv Inventory) error {
			log("Устанавливаем DDS Companion", 89)
			if e := verifyExactSetupStage(stageRoot, preparedStage(prepared, "companion")); e != nil {
				return e
			}
			log("Размещаем DDS Companion; Windows может запросить разрешение", 89)
			result, e := guiInstallCompanion(ctx, inv, stageRoot, target, localAppData, appData, installerDir)
			if e != nil {
				return e
			}
			if result.Status != "FILES_INSTALLED_UNSTARTED" {
				return errors.New("Companion не подтвердил установку файлов")
			}
			return nil
		},
	}
	report = executeSelectedFirstSetup(ctx, fresh, prepared, hooks, choices)
	if report.Status != "SELECTED_FILES_INTEGRATED_FIRST_START_PENDING" {
		var details []string
		for _, s := range report.Steps {
			if s.Result == "FAILED" || s.Result == "BLOCKED" {
				details = append(details, s.Component+": "+s.Reason)
			}
		}
		return report, fmt.Errorf("Установка не завершена (%s): %s", report.Status, strings.Join(details, "; "))
	}
	if choices[3] && detect().Companion.State == StateOK {
		log("Настраиваем ярлык и регистрацию DDS", 97)
		if e := registerWindowsInstall(localAppData, target, shortcut); e != nil {
			return report, e
		}
	}
	if choices[2] && detect().Plugin.State == StateOK {
		if e := ensureSelectedDDSPlugin(ctx, appData); e != nil {
			return report, e
		}
	}
	log("Выбранные файлы установлены; запуск — после завершения мастера", 100)
	return report, nil
}

func ensureSelectedDDSPlugin(ctx context.Context, appData string) error {
	if err := ensureDiscordStopped(ctx, discordClientIsRunning, stopDiscordForSetup); err != nil {
		return err
	}
	if _, err := installedDDSPluginVersion(appData); err != nil {
		return err
	}
	active, err := ddsPluginEnabled(appData)
	if err != nil {
		return err
	}
	if active {
		return nil
	}
	if err := ensureDiscordStopped(ctx, discordClientIsRunning, stopDiscordForSetup); err != nil {
		return err
	}
	if err := enableNewDDSPlugin(appData); err != nil {
		return err
	}
	active, err = ddsPluginEnabled(appData)
	if err != nil {
		return err
	}
	if !active {
		return errors.New("DDS Plugin не включён после записи plugins.json")
	}
	return nil
}
