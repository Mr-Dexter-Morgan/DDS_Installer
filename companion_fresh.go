package main

// A first-install floor for DDS Companion. It only publishes a previously
// verified full Stable payload under a NEW per-user application directory.
// It does not launch DDS, claim readiness, create a DataRoot, or touch registry.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type companionFreshResult struct {
	Status  string
	Root    string
	Version string
}

func freshCompanionInstall(inv Inventory, stageRoot, localAppData string, beforeCommit func() error) (companionFreshResult, error) {
	return freshCompanionInstallAt(inv, stageRoot, filepath.Join(localAppData, "DDS"), beforeCommit, nil)
}

// Preserve the verified staging/publish transaction for both legacy and GUI paths.
// preparePermissions applies only to the newly copied private scratch directory.
func freshCompanionInstallAt(inv Inventory, stageRoot, target string, beforeCommit func() error, preparePermissions func(string) error) (companionFreshResult, error) {
	result := companionFreshResult{}
	// An uncertain or existing DDS environment is not a clean-install permit.
	if inv.Companion.State != StateMissing && inv.Companion.State != StateNotDetected {
		return result, errors.New("DDS Companion уже обнаружен или его состояние неоднозначно; первичная установка запрещена")
	}
	if inv.Profile == "portable" || inv.Profile == "installed" && inv.Companion.Path != "" {
		return result, errors.New("существующий профиль DDS: чистая установка запрещена")
	}
	if err := validateProgramTarget(target, inv.DataRoot.Path); err != nil {
		return result, err
	}
	target = filepath.Clean(target)
	if _, err := os.Lstat(target); err == nil {
		return result, errors.New("целевая папка уже существует; перезапись и объединение запрещены")
	} else if !os.IsNotExist(err) {
		return result, err
	}
	// The target must not overlap a previously detected DataRoot.
	if inv.DataRoot.Path != "" {
		dr := filepath.Clean(inv.DataRoot.Path)
		if strings.EqualFold(dr, target) || strings.HasPrefix(strings.ToLower(dr), strings.ToLower(target+string(os.PathSeparator))) {
			return result, errors.New("путь установки пересекается с DataRoot")
		}
	}
	rec, stage, ok := validCompanionCache(stageRoot, false)
	if !ok {
		return result, errors.New("нет проверенного Stable staging DDS Companion")
	}
	payload := filepath.Join(stage, "payload")
	if err := ensureNoLinksInTree(payload); err != nil {
		return result, fmt.Errorf("небезопасное содержимое пакета: %w", err)
	}
	if err := validCompanionStageContents(stage, rec); err != nil {
		return result, err
	}
	if beforeCommit == nil {
		return result, errors.New("не предоставлена обязательная проверка отсутствия запущенного DDS")
	}
	if err := beforeCommit(); err != nil {
		return result, err
	}
	scratch, err := os.MkdirTemp(filepath.Dir(target), ".dds-installer-fresh-")
	if err != nil {
		return result, err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(scratch)
		}
	}()
	if err = copyVersionFiles(payload, scratch); err != nil {
		return result, err
	}
	if preparePermissions != nil {
		if err = preparePermissions(scratch); err != nil {
			return result, fmt.Errorf("права на файлы DDS: %w", err)
		}
	}
	if err = compareVersionTree(payload, scratch); err != nil {
		return result, err
	}
	if err = validCompanionStageContents(stage, rec); err != nil {
		return result, errors.New("staging изменился во время копирования: " + err.Error())
	}
	if err = beforeCommit(); err != nil {
		return result, err
	}
	if _, err = os.Lstat(target); err == nil {
		return result, errors.New("целевая папка появилась во время установки; операция отменена")
	} else if !os.IsNotExist(err) {
		return result, err
	}
	if err = os.Rename(scratch, target); err != nil {
		return result, fmt.Errorf("не удалось опубликовать проверенный каталог: %w", err)
	}
	published = true
	// A full runnable start and profile initialization are a later Windows gate.
	return companionFreshResult{Status: "FILES_INSTALLED_UNSTARTED", Root: target, Version: rec.Version}, nil
}

func runCompanionFreshMenu(inv Inventory, input io.Reader, out io.Writer, stageRoot, localAppData string, closed func() error) {
	fmt.Fprintln(out, "DDS Companion — первичная установка в новую папку (без запуска)")
	fmt.Fprintln(out, "Цель: ", filepath.Join(localAppData, "DDS"))
	fmt.Fprintln(out, "Установленный DDS, DataRoot, SQLite, Discord, BetterDiscord и реестр не изменяются.")
	if !confirmNumeric(input, out, "Установить DDS Companion в новую папку?") {
		fmt.Fprintln(out, "ОТМЕНЕНО: файловые изменения не выполнялись")
		return
	}
	got, err := freshCompanionInstall(inv, stageRoot, localAppData, closed)
	if err != nil {
		fmt.Fprintln(out, "BLOCKED:", err)
		return
	}
	fmt.Fprintf(out, "%s — Companion %s. Файлы размещены: %s\n", got.Status, got.Version, got.Root)
	fmt.Fprintln(out, "Запуск, DataRoot, интеграция и итоговая проверка работы приложения ещё не выполнялись.")
}
