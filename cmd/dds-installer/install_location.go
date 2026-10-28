package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

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
