package main

// First narrowly scoped Apply/Validate floor: DDS Plugin only. The remaining
// three stages remain acquisition-only. Never write to DDS_Data or DataRoot.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type pluginApplyResult struct{ Status, Version, Target, Backup string }

var pluginStableRE = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

func compareStableVersions(a, b string) int {
	aa := strings.Split(a, ".")
	bb := strings.Split(b, ".")
	if len(aa) != 3 || len(bb) != 3 {
		return 0
	}
	for i := 0; i < 3; i++ {
		x, e := strconv.Atoi(aa[i])
		y, f := strconv.Atoi(bb[i])
		if e != nil || f != nil {
			return 0
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

func requireNormalFile(p string) error {
	st, e := os.Lstat(p)
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("небезопасный файл: %s", p)
	}
	return nil
}
func requireNormalDir(p string) error {
	st, e := os.Lstat(p)
	if e != nil {
		return e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("небезопасная папка: %s", p)
	}
	return nil
}
func verifiedPluginStage(stageRoot string) (preparedExternal, string, error) {
	var rec preparedExternal
	parent := filepath.Join(stageRoot, "plugin")
	if e := requireNormalDir(parent); e != nil {
		return rec, "", fmt.Errorf("нет проверенного staging DDS Plugin: %w", e)
	}
	dirs, e := os.ReadDir(parent)
	if e != nil {
		return rec, "", e
	}
	var versions []string
	for _, d := range dirs {
		if !d.IsDir() || !pluginStableRE.MatchString(d.Name()) {
			continue
		}
		versions = append(versions, d.Name())
	}
	sort.Slice(versions, func(i, j int) bool {
		return compareStableVersions(strings.TrimPrefix(versions[i], "v"), strings.TrimPrefix(versions[j], "v")) > 0
	})
	for _, v := range versions {
		d := filepath.Join(parent, v)
		if requireNormalDir(d) != nil {
			continue
		}
		receiptPath := filepath.Join(d, "stage_receipt.json")
		if requireNormalFile(receiptPath) != nil {
			continue
		}
		content, e := os.ReadFile(receiptPath)
		if e != nil {
			continue
		}
		var r preparedExternal
		if json.Unmarshal(content, &r) != nil || r.Schema != "dds-installer-external-stage-v1" || r.Component != "DDS Plugin" || r.Status != "VERIFIED_AND_STAGED" || r.Version != strings.TrimPrefix(v, "v") || r.PayloadSHA256 != r.SHA256 || !hexDigestRE.MatchString(r.SHA256) || r.ZIPSize <= 0 || r.ZIPSize > externalMaxSize {
			continue
		}
		if r.Source != pluginPrefix+v+"/DDS.plugin.js" {
			continue
		}
		if requireNormalDir(filepath.Join(d, "package")) != nil || requireNormalDir(filepath.Join(d, "payload")) != nil {
			continue
		}
		packageFile := filepath.Join(d, "package", "DDS.plugin.js")
		payloadFile := filepath.Join(d, "payload", "DDS.plugin.js")
		if requireNormalFile(packageFile) != nil || requireNormalFile(payloadFile) != nil {
			continue
		}
		packSHA, packSize, e := shaFile(packageFile)
		if e != nil || packSize != r.ZIPSize || packSHA != r.SHA256 {
			continue
		}
		actual, n, e := shaFile(payloadFile)
		if e != nil || actual != r.PayloadSHA256 || n != r.ZIPSize {
			continue
		}
		data, e := os.ReadFile(payloadFile)
		if e != nil {
			continue
		}
		match := pluginHeaderVersionRE.FindSubmatch(data)
		if len(match) != 2 || string(match[1]) != r.Version {
			continue
		}
		return r, payloadFile, nil
	}
	return rec, "", errors.New("нет годного проверенного staging DDS Plugin; сначала запусти 1")
}

// preparePluginApply makes no edits. Callers must obtain explicit approval
// before invoking applyPlugin. No silent downgrade of installed versions.
func preparePluginApply(inv Inventory, stageRoot, appData string) (preparedExternal, string, string, bool, error) {
	var empty preparedExternal
	if appData == "" {
		return empty, "", "", false, errors.New("APPDATA не определён")
	}
	if inv.Discord.State != StateOK || inv.BetterDiscord.State != StateOK {
		return empty, "", "", false, errors.New("сначала требуется исправный Discord и BetterDiscord")
	}
	if e := requireNormalDir(appData); e != nil {
		return empty, "", "", false, fmt.Errorf("небезопасная APPDATA: %w", e)
	}
	bd := filepath.Join(appData, "BetterDiscord")
	if e := requireNormalDir(bd); e != nil {
		return empty, "", "", false, e
	}
	if e := requireNormalDir(filepath.Join(bd, "data")); e != nil {
		return empty, "", "", false, e
	}
	pluginDir := filepath.Join(bd, "plugins")
	if st, e := os.Lstat(pluginDir); e == nil {
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return empty, "", "", false, errors.New("небезопасная папка плагинов")
		}
	} else if !os.IsNotExist(e) {
		return empty, "", "", false, e
	}
	rec, payload, e := verifiedPluginStage(stageRoot)
	if e != nil {
		return empty, "", "", false, e
	}
	target := filepath.Join(pluginDir, "DDS.plugin.js")
	if st, e := os.Lstat(target); e == nil {
		if !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 {
			return empty, "", "", false, errors.New("существующий плагин имеет небезопасный тип")
		}
		digest, _, e := shaFile(target)
		if e != nil {
			return empty, "", "", false, e
		}
		if digest == rec.PayloadSHA256 {
			return rec, payload, target, false, nil
		}
		installed, readErr := os.ReadFile(target)
		if readErr != nil {
			return empty, "", "", false, readErr
		}
		ver := pluginHeaderVersionRE.FindSubmatch(installed)
		if len(ver) == 2 && finalVersionRE.MatchString(string(ver[1])) && compareStableVersions(string(ver[1]), rec.Version) > 0 {
			return empty, "", "", false, errors.New("установленная версия новее staging; автоматический откат запрещён")
		}
	} else if !os.IsNotExist(e) {
		return empty, "", "", false, e
	}
	return rec, payload, target, true, nil
}

func copyPluginChecked(src, dst, expected string) error {
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	hash := sha256.New()
	n, cp := io.Copy(io.MultiWriter(out, hash), io.LimitReader(in, externalMaxSize+1))
	syncErr := out.Sync()
	closeErr := out.Close()
	if cp != nil {
		return cp
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n <= 0 || n > externalMaxSize || hex.EncodeToString(hash.Sum(nil)) != expected {
		return errors.New("контрольная сумма перед записью изменилась")
	}
	return nil
}

// validate is injectable only to prove rollback under deterministic test failure.
func applyPluginWithValidation(inv Inventory, stageRoot, appData string, validate func(string, string) error) (pluginApplyResult, error) {
	var result pluginApplyResult
	rec, payload, target, needed, e := preparePluginApply(inv, stageRoot, appData)
	if e != nil {
		return result, e
	}
	result.Version, result.Target = rec.Version, target
	if !needed {
		result.Status = "ALREADY_CURRENT"
		return result, nil
	}
	pluginDir := filepath.Dir(target)
	if e = os.MkdirAll(pluginDir, 0700); e != nil {
		return result, e
	}
	tmpf, e := os.CreateTemp(pluginDir, ".dds-plugin-apply-*.tmp")
	if e != nil {
		return result, e
	}
	temp := tmpf.Name()
	_ = tmpf.Close()
	_ = os.Remove(temp)
	defer os.Remove(temp)
	if e = copyPluginChecked(payload, temp, rec.PayloadSHA256); e != nil {
		return result, e
	}
	// Verify the staged original did not mutate after the preflight.
	if digest, _, err := shaFile(payload); err != nil || digest != rec.PayloadSHA256 {
		return result, errors.New("staging изменился до установки")
	}
	hadPrevious := false
	if _, e = os.Lstat(target); e == nil {
		hadPrevious = true
		backupFile, e2 := os.CreateTemp(pluginDir, ".dds-plugin-previous-*.bak")
		if e2 != nil {
			return result, e2
		}
		backup := backupFile.Name()
		_ = backupFile.Close()
		_ = os.Remove(backup)
		if e = os.Rename(target, backup); e != nil {
			return result, fmt.Errorf("резервное копирование: %w", e)
		}
		result.Backup = backup
	} else if !os.IsNotExist(e) {
		return result, e
	}
	if e = os.Rename(temp, target); e != nil {
		if hadPrevious {
			if rb := os.Rename(result.Backup, target); rb != nil {
				return result, fmt.Errorf("перенос: %v; откат: %v", e, rb)
			}
		}
		return result, e
	}
	check := func() error {
		if err := requireNormalFile(target); err != nil {
			return err
		}
		digest, _, err := shaFile(target)
		if err != nil {
			return err
		}
		if digest != rec.PayloadSHA256 {
			return errors.New("SHA-256 установленного DDS Plugin не совпал")
		}
		b, err := os.ReadFile(target)
		if err != nil {
			return err
		}
		m := pluginHeaderVersionRE.FindSubmatch(b)
		if len(m) != 2 || string(m[1]) != rec.Version {
			return errors.New("версия установленного DDS Plugin не совпала")
		}
		if validate != nil {
			return validate(target, rec.Version)
		}
		return nil
	}
	if e = check(); e != nil {
		// The historical fixed ".failed-apply" pathname could already exist.
		// That prevented moving the candidate out of the way on Windows,
		// leaving the old plugin unrestorable. Quarantine exclusively in a
		// newly created private directory; never overwrite foreign files.
		failureDir, mkErr := os.MkdirTemp(pluginDir, ".dds-plugin-failed-*")
		if mkErr != nil {
			return result, fmt.Errorf("проверка: %v; нельзя подготовить откат: %w (резервная копия: %s)", e, mkErr, result.Backup)
		}
		quarantined := filepath.Join(failureDir, "DDS.plugin.js")
		if moveErr := os.Rename(target, quarantined); moveErr != nil {
			_ = os.Remove(failureDir) // empty directory only
			return result, fmt.Errorf("проверка: %v; не удалось изолировать новую версию: %w (резервная копия: %s)", e, moveErr, result.Backup)
		}
		if hadPrevious {
			if restoreErr := os.Rename(result.Backup, target); restoreErr != nil {
				// Keep both versions for manual recovery; do not misreport
				// a successful rollback or delete the only old copy.
				return result, fmt.Errorf("проверка: %v; откат не выполнен: %w; резервная копия: %s; отклонённый файл: %s", e, restoreErr, result.Backup, quarantined)
			}
			result.Backup = ""
		}
		if cleanupErr := os.RemoveAll(failureDir); cleanupErr != nil {
			return result, fmt.Errorf("проверка: %v; прежнее состояние восстановлено, но временный файл %s не удалён: %w", e, quarantined, cleanupErr)
		}
		if hadPrevious {
			return result, fmt.Errorf("проверка после установки: %w; прежний DDS Plugin восстановлен", e)
		}
		return result, fmt.Errorf("проверка после установки: %w; новая установка отменена, DDS Plugin не установлен", e)
	}
	result.Status = "APPLIED_AND_VALIDATED"
	return result, nil
}
func applyPlugin(inv Inventory, stageRoot, appData string) (pluginApplyResult, error) {
	return applyPluginWithValidation(inv, stageRoot, appData, nil)
}
