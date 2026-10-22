package main

// Non-mutating Companion apply preflight. This does not implement or authorize
// installation. The verified staging receipt alone is never an Apply permit.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type companionPreview struct {
	Status           string
	StagedVersion    string
	InstalledVersion string
	InstallRoot      string
	StagingPath      string
	Message          string
}

func prepareCompanionPreview(inv Inventory, stageRoot string) companionPreview {
	result := companionPreview{Status: "BLOCKED"}
	rec, path, ok := validCompanionCache(stageRoot, false)
	if !ok {
		result.Message = "Проверенный staging Companion не найден или повреждён. Сначала требуется операция 1."
		return result
	}
	result.StagedVersion, result.StagingPath = rec.Version, path
	if inv.Companion.State != StateOK || inv.Companion.Path == "" {
		result.Message = "Корень установленного DDS не определён однозначно. Автоматическая установка запрещена."
		return result
	}
	if inv.Profile != "installed" {
		result.Message = "Профиль DDS не подтверждён как installed; Portable/неизвестный профиль требует отдельного контракта."
		return result
	}
	root := filepath.Clean(inv.Companion.Path)
	if !filepath.IsAbs(root) || requireNormalDir(root) != nil {
		result.Message = "Небезопасный или недоступный путь установленного DDS."
		return result
	}
	for _, name := range []string{"DDS.exe", "current.json"} {
		if requireNormalFile(filepath.Join(root, name)) != nil {
			result.Message = "Отсутствует обязательный файл установленного DDS: " + name
			return result
		}
	}
	b, e := os.ReadFile(filepath.Join(root, "current.json"))
	if e != nil {
		result.Message = "Не удалось прочитать current.json."
		return result
	}
	var ptr currentPointer
	if e = json.Unmarshal(b, &ptr); e != nil || !finalVersionRE.MatchString(ptr.Version) || ptr.Schema != "dds-version-pointer-v1" || ptr.RelativePath != "versions/"+ptr.Version {
		result.Message = "Установленный current.json не прошёл проверку контракта."
		return result
	}
	if !finalVersionRE.MatchString(inv.Companion.Version) || inv.Companion.Version != ptr.Version {
		result.Message = "Версия DDS в обнаружении не совпала с current.json."
		return result
	}
	result.InstallRoot, result.InstalledVersion = root, ptr.Version
	if compareStableVersions(ptr.Version, rec.Version) > 0 {
		result.Message = "Установленная версия новее проверенного staging. Понижение версии запрещено."
		return result
	}
	if ptr.Version == rec.Version {
		result.Status = "VERSION_MATCH"
		result.Message = "Версии совпадают; идентичность всех установленных файлов не проверена. Изменений нет."
		return result
	}
	result.Status = "PREVIEW_ONLY"
	result.Message = "Пакет проверен. Предварительно обнаружено обновление. Никакая установка или замена не реализована в операции 4."
	return result
}

func runCompanionPreview(inv Inventory, out io.Writer, stageRoot string) {
	p := prepareCompanionPreview(inv, stageRoot)
	fmt.Fprintln(out, "DDS Companion — ПРЕДВАРИТЕЛЬНАЯ ПРОВЕРКА, БЕЗ УСТАНОВКИ")
	fmt.Fprintf(out, "Результат: %s\n", p.Status)
	if p.StagedVersion != "" {
		fmt.Fprintf(out, "Проверенный пакет: %s (локальный staging; свежесть GitHub не проверялась)\n", p.StagedVersion)
	}
	if p.InstalledVersion != "" {
		fmt.Fprintf(out, "Установлено: %s\n", p.InstalledVersion)
	}
	if p.InstallRoot != "" {
		fmt.Fprintf(out, "Папка установленного DDS: %s\n", p.InstallRoot)
	}
	fmt.Fprintln(out, strings.TrimSpace(p.Message))
	fmt.Fprintln(out, "DDS.exe, current.json, версии приложения, DataRoot, SQLite, реестр и настройки не изменялись.")
}
