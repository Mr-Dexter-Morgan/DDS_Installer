package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Companion upgrade is intentionally a separate, explicit operation.
// One immutable versioned payload is added; an existing version is never
// overwritten. current.json is switched last and only after verified copies.
// Previous bootstrap files and the prior version are preserved for recovery.

type companionApplyResult struct{ Status, Version, Root, Backup string }

var bootstrapRootNames = []string{"DDS.exe", "DDSUpdater.exe", "current.json"}

func ensureNoLinksInTree(root string) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("ссылка запрещена: %s", p)
		}
		if d.IsDir() {
			return requireNormalDir(p)
		}
		return requireNormalFile(p)
	})
}

func copyCheckedFile(src, dst, expected string) error {
	if !hexDigestRE.MatchString(expected) {
		return errors.New("отсутствует проверенный SHA-256")
	}
	if err := requireNormalFile(src); err != nil {
		return err
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	o, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, cpErr := io.Copy(io.MultiWriter(o, h), io.LimitReader(f, maxUnpacked+1))
	syncErr := o.Sync()
	closeErr := o.Close()
	if cpErr != nil {
		return cpErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n > maxUnpacked || hex.EncodeToString(h.Sum(nil)) != expected {
		return fmt.Errorf("копия не совпала с SHA-256: %s", filepath.Base(src))
	}
	return nil
}

func copyVersionFiles(source, dest string) error {
	return filepath.WalkDir(source, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, p)
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("ссылки запрещены в версии")
		}
		dst := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0700)
		}
		if err = requireNormalFile(p); err != nil {
			return err
		}
		sha, _, e := shaFile(p)
		if e != nil {
			return e
		}
		return copyCheckedFile(p, dst, sha)
	})
}

func makeAtomicReplacement(src, target string) error {
	// src has been verified; temporary resides on the target's filesystem.
	// Go os.Rename performs replace-existing for normal files on Windows.
	if err := requireNormalFile(src); err != nil {
		return err
	}
	if err := requireNormalFile(target); err != nil {
		return err
	}
	return os.Rename(src, target)
}

// postcheck injection supports deterministic rollback tests, without
// executing any downloaded executable or modifying DataRoot.
func applyCompanionWithValidation(inv Inventory, stageRoot string, closed func() error, postcheck func(string, string) error) (res companionApplyResult, err error) {
	// A crashed earlier transaction must be resolved before ANY new Apply,
	// even if the installed pointer happens to match the staged version.
	if inv.Companion.Path != "" {
		if e := ensureNoPendingCompanionTransaction(inv.Companion.Path); e != nil {
			return res, e
		}
	}
	preview := prepareCompanionPreview(inv, stageRoot)
	if preview.Status == "VERSION_MATCH" {
		return companionApplyResult{Status: "VERSION_MATCH_NOOP", Version: preview.StagedVersion, Root: preview.InstallRoot}, nil
	}
	if preview.Status != "PREVIEW_ONLY" {
		return res, fmt.Errorf("применение запрещено: %s", preview.Message)
	}
	if closed == nil {
		return res, errors.New("отсутствует обязательная проверка закрытия Companion")
	}
	if err = closed(); err != nil {
		return res, err
	}
	root, version := preview.InstallRoot, preview.StagedVersion
	if filepath.IsAbs(stageRoot) == false || requireNormalDir(root) != nil {
		return res, errors.New("путь установки или staging небезопасен")
	}
	versions := filepath.Join(root, "versions")
	if err = requireNormalDir(versions); err != nil {
		return res, fmt.Errorf("versions: %w", err)
	}
	currentVersion := filepath.Join(versions, preview.InstalledVersion)
	if err = requireNormalDir(currentVersion); err != nil {
		return res, fmt.Errorf("предыдущая версия отсутствует: %w", err)
	}
	targetVersion := filepath.Join(versions, version)
	if _, e := os.Lstat(targetVersion); e == nil {
		return res, errors.New("целевая версия уже существует; перезапись запрещена")
	} else if !os.IsNotExist(e) {
		return res, e
	}
	r, stage, ok := validCompanionCache(stageRoot, false)
	if !ok || r.Version != version || stage != preview.StagingPath {
		return res, errors.New("staging изменился после предпросмотра")
	}
	payload := filepath.Join(stage, "payload")
	if err = ensureNoLinksInTree(payload); err != nil {
		return res, fmt.Errorf("небезопасный staging: %w", err)
	}
	if err = validCompanionStageContents(stage, r); err != nil {
		return res, err
	}
	for _, name := range bootstrapRootNames {
		if e := requireNormalFile(filepath.Join(root, name)); e != nil {
			return res, e
		}
	}
	// Only versioned application files are added to root; the SQLite/DataRoot
	// and all unrelated root files are never traversed or mutated.
	size := int64(0)
	e := filepath.WalkDir(filepath.Join(payload, "versions", version), func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		st, e := d.Info()
		if e != nil {
			return e
		}
		if st.Size() > maxUnpacked-size {
			return errors.New("версия превышает лимит размера")
		}
		size += st.Size()
		return nil
	})
	if e != nil {
		return res, e
	}
	if avail, e := freeDiskSpace(root); e != nil {
		return res, e
	} else if !enoughStageSpace(avail, uint64(size)) {
		return res, errors.New("недостаточно свободного места для новой версии")
	}
	if err = closed(); err != nil {
		return res, err
	}
	pending, err := os.MkdirTemp(versions, ".dds-installer-pending-")
	if err != nil {
		return res, err
	}
	pendingExists := true
	defer func() {
		if pendingExists {
			_ = os.RemoveAll(pending)
		}
	}()
	if err = copyVersionFiles(filepath.Join(payload, "versions", version), pending); err != nil {
		return res, err
	}
	// Validate copied payload with the exact stage manifest; do not trust mere sizes.
	if err = compareVersionTree(filepath.Join(payload, "versions", version), pending); err != nil {
		return res, err
	}
	// Temporary working directory and backup stay inside installed root.
	backup, err := os.MkdirTemp(root, ".dds-installer-backup-")
	if err != nil {
		return res, err
	}
	res.Backup = backup
	backupCommitted := false
	defer func() {
		if !backupCommitted {
			_ = os.RemoveAll(backup)
		}
	}()
	nextFiles := map[string]string{}
	oldFiles := map[string]string{}
	oldDigests := map[string]string{}
	for _, name := range bootstrapRootNames {
		src := filepath.Join(payload, name)
		dst := filepath.Join(backup, "next-"+name)
		if err = copyCheckedFile(src, dst, r.BootstrapSHA256[name]); err != nil {
			return res, err
		}
		old := filepath.Join(root, name)
		oldHash, _, hashErr := shaFile(old)
		if hashErr != nil {
			return res, hashErr
		}
		oldDst := filepath.Join(backup, "previous-"+name)
		if err = copyCheckedFile(old, oldDst, oldHash); err != nil {
			return res, err
		}
		nextFiles[name] = dst
		oldFiles[name] = oldDst
		oldDigests[name] = oldHash
	}
	// Verify source staging again immediately before first installed write.
	if err = validCompanionStageContents(stage, r); err != nil {
		return res, fmt.Errorf("staging изменился: %w", err)
	}
	if err = closed(); err != nil {
		return res, err
	}
	if _, err = os.Lstat(targetVersion); err == nil {
		return res, errors.New("целевая версия появилась во время подготовки")
	}
	if !os.IsNotExist(err) {
		return res, err
	}
	// A synced journal is mandatory before the first installed write.
	// If the process is terminated, the pending journal blocks new Apply.
	nextDigests := make(map[string]string, len(bootstrapRootNames))
	for _, name := range bootstrapRootNames {
		nextDigests[name] = r.BootstrapSHA256[name]
	}
	transaction := companionTransaction{Schema: companionTxnSchema,
		PreviousVersion: preview.InstalledVersion, NextVersion: version,
		PreviousSHA256: oldDigests, NextSHA256: nextDigests}
	if err = writeCompanionTransactionPending(backup, transaction); err != nil {
		return res, err
	}
	if err = os.Rename(pending, targetVersion); err != nil {
		return res, err
	}
	backupCommitted = true
	pendingExists = false
	changed := []string{}
	// Any failure restores original bootstrap files, including current.json.
	defer func() {
		if err == nil {
			return
		}
		var failures []string
		for i := len(changed) - 1; i >= 0; i-- {
			name := changed[i]
			restore := filepath.Join(backup, "rollback-"+name)
			oldHash, _, hashErr := shaFile(oldFiles[name])
			if hashErr != nil {
				failures = append(failures, name+": "+hashErr.Error())
				continue
			}
			if e := copyCheckedFile(oldFiles[name], restore, oldHash); e != nil {
				failures = append(failures, name+": "+e.Error())
				continue
			}
			if e := makeAtomicReplacement(restore, filepath.Join(root, name)); e != nil {
				failures = append(failures, name+": "+e.Error())
			}
		}
		// The original versions/preview pointer are always preserved.
		// A failed new version may remain unused so forensic evidence is not lost.
		if len(failures) == 0 {
			if e := finishCompanionTransaction(backup, companionTxnRecovered); e != nil {
				failures = append(failures, "запись итога: "+e.Error())
			}
		}
		if len(failures) > 0 {
			err = fmt.Errorf("%v; ОТКАТ НЕ ПОЛНЫЙ: %s; backup: %s", err, strings.Join(failures, "; "), backup)
		} else {
			err = fmt.Errorf("%w; предыдущие файлы восстановлены; backup: %s", err, backup)
		}
	}()
	for _, name := range bootstrapRootNames {
		if err = makeAtomicReplacement(nextFiles[name], filepath.Join(root, name)); err != nil {
			return res, fmt.Errorf("замена %s: %w", name, err)
		}
		changed = append(changed, name)
	}
	// Verify stage version and every installed target against accepted bytes.
	for _, name := range bootstrapRootNames {
		digest, _, e := shaFile(filepath.Join(root, name))
		if e != nil || digest != r.BootstrapSHA256[name] {
			err = fmt.Errorf("хэш установленного %s не совпал", name)
			return res, err
		}
	}
	if err = compareVersionTree(filepath.Join(payload, "versions", version), targetVersion); err != nil {
		return res, err
	}
	if postcheck != nil {
		if err = postcheck(root, version); err != nil {
			return res, err
		}
	}
	if err = finishCompanionTransaction(backup, companionTxnCompleted); err != nil {
		return res, fmt.Errorf("завершение журнала: %w", err)
	}
	res.Status, res.Version, res.Root = "APPLIED_AND_VALIDATED", version, root
	return res, nil
}

func compareVersionTree(a, b string) error {
	return filepath.WalkDir(a, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(a, p)
		if e != nil {
			return e
		}
		other := filepath.Join(b, rel)
		if d.IsDir() {
			return requireNormalDir(other)
		}
		if e = requireNormalFile(other); e != nil {
			return e
		}
		ah, as, e := shaFile(p)
		if e != nil {
			return e
		}
		bh, bs, e := shaFile(other)
		if e != nil {
			return e
		}
		if ah != bh || as != bs {
			return fmt.Errorf("версия: содержимое %s изменено", rel)
		}
		return nil
	})
}

func runCompanionApplyMenu(inv Inventory, input io.Reader, out io.Writer, stageRoot string, closed func() error) {
	if inv.Companion.Path != "" {
		if e := ensureNoPendingCompanionTransaction(inv.Companion.Path); e != nil {
			fmt.Fprintln(out, "BLOCKED:", e)
			return
		}
	}
	pre := prepareCompanionPreview(inv, stageRoot)
	fmt.Fprintln(out, "DDS Companion — безопасная установка из проверенного staging")
	fmt.Fprintf(out, "Результат предварительной проверки: %s\n", pre.Status)
	fmt.Fprintln(out, pre.Message)
	if pre.Status == "VERSION_MATCH" {
		fmt.Fprintln(out, "Установленная версия уже совпадает. Изменений нет.")
		return
	}
	if pre.Status != "PREVIEW_ONLY" {
		return
	}
	if closed == nil || closed() != nil {
		fmt.Fprintln(out, "ОТМЕНА: DDS запущен или проверка процессов недоступна.")
		return
	}
	fmt.Fprintf(out, "Обновление: %s -> %s\nПапка: %s\n", pre.InstalledVersion, pre.StagedVersion, pre.InstallRoot)
	fmt.Fprintln(out, "Будет создана отдельная версия и резервная копия трёх корневых файлов; предыдущая версия сохранится.")
	fmt.Fprintln(out, "DataRoot, SQLite, DDS_Data, Plugin, Discord, реестр и автозапуск не изменяются.")
	if !confirmNumeric(input, out, "Установить проверенную версию DDS Companion?") {
		fmt.Fprintln(out, "ОТМЕНЕНО: изменений нет.")
		return
	}
	result, e := applyCompanionWithValidation(inv, stageRoot, closed, nil)
	if e != nil {
		fmt.Fprintln(out, "НЕ УДАЛОСЬ:", e)
		return
	}
	fmt.Fprintf(out, "%s: %s\nПапка: %s\nРезервная копия: %s\n", result.Status, result.Version, result.Root, result.Backup)
	fmt.Fprintln(out, "Исполняемые файлы не запускались. Запуск DDS и UI health — отдельная последующая проверка.")
}

// A first-install floor for DDS Companion. It only publishes a previously
// verified full Stable payload under a NEW per-user application directory.
// It does not launch DDS, claim readiness, create a DataRoot, or touch registry.

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

// Non-mutating Companion apply preflight. This does not implement or authorize
// installation. The verified staging receipt alone is never an Apply permit.

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

// Durable Companion transaction evidence for process-kill / interruption.
// A pending journal is created and synced BEFORE the first live bootstrap
// replacement. Recovery requires explicit user approval; no auto-repair.

const (
	companionTxnSchema    = "dds-installer-companion-transaction-v1"
	companionTxnPending   = "transaction.pending.json"
	companionTxnCompleted = "transaction.completed.json"
	companionTxnRecovered = "transaction.recovered.json"
)

type companionTransaction struct {
	Schema          string            `json:"schema"`
	PreviousVersion string            `json:"previous_version"`
	NextVersion     string            `json:"next_version"`
	PreviousSHA256  map[string]string `json:"previous_sha256"`
	NextSHA256      map[string]string `json:"next_sha256"`
}

func validateCompanionTransaction(tx companionTransaction) error {
	if tx.Schema != companionTxnSchema || !finalVersionRE.MatchString(tx.PreviousVersion) || !finalVersionRE.MatchString(tx.NextVersion) || tx.PreviousVersion == tx.NextVersion {
		return errors.New("неверный контракт журнала транзакции")
	}
	if len(tx.PreviousSHA256) != len(bootstrapRootNames) || len(tx.NextSHA256) != len(bootstrapRootNames) {
		return errors.New("неполные контрольные суммы журнала")
	}
	for _, n := range bootstrapRootNames {
		if !hexDigestRE.MatchString(tx.PreviousSHA256[n]) || !hexDigestRE.MatchString(tx.NextSHA256[n]) {
			return fmt.Errorf("неверная контрольная сумма %s", n)
		}
	}
	return nil
}

func writeCompanionTransactionPending(backup string, tx companionTransaction) error {
	if e := validateCompanionTransaction(tx); e != nil {
		return e
	}
	path := filepath.Join(backup, companionTxnPending)
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	b, e := json.MarshalIndent(tx, "", "  ")
	if e == nil {
		_, e = f.Write(append(b, '\n'))
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	return closeErr
}

func finishCompanionTransaction(backup, target string) error {
	if target != companionTxnCompleted && target != companionTxnRecovered {
		return errors.New("неверный итог транзакции")
	}
	return os.Rename(filepath.Join(backup, companionTxnPending), filepath.Join(backup, target))
}

type pendingCompanionTransaction struct {
	Backup  string
	Journal companionTransaction
}

// Treat malformed or tampered journal as a blocker, never silently skip it.
func pendingCompanionTransactions(root string) ([]pendingCompanionTransaction, error) {
	if e := requireNormalDir(root); e != nil {
		return nil, e
	}
	entries, e := os.ReadDir(root)
	if e != nil {
		return nil, e
	}
	result := make([]pendingCompanionTransaction, 0)
	for _, ent := range entries {
		if !strings.HasPrefix(ent.Name(), ".dds-installer-backup-") {
			continue
		}
		dir := filepath.Join(root, ent.Name())
		if e = requireNormalDir(dir); e != nil {
			return nil, fmt.Errorf("небезопасная папка резервирования: %w", e)
		}
		jp := filepath.Join(dir, companionTxnPending)
		if _, e = os.Lstat(jp); os.IsNotExist(e) {
			continue
		} else if e != nil {
			return nil, e
		}
		if e = requireNormalFile(jp); e != nil {
			return nil, e
		}
		b, e := os.ReadFile(jp)
		if e != nil {
			return nil, e
		}
		if len(b) > 64*1024 {
			return nil, errors.New("журнал транзакции слишком велик")
		}
		var j companionTransaction
		if e = json.Unmarshal(b, &j); e != nil {
			return nil, fmt.Errorf("журнал повреждён: %w", e)
		}
		if e = validateCompanionTransaction(j); e != nil {
			return nil, e
		}
		result = append(result, pendingCompanionTransaction{Backup: dir, Journal: j})
	}
	return result, nil
}

func ensureNoPendingCompanionTransaction(root string) error {
	pend, e := pendingCompanionTransactions(root)
	if e != nil {
		return e
	}
	if len(pend) > 0 {
		return fmt.Errorf("найдена незавершённая установка (%d): сначала отдельная операция восстановления", len(pend))
	}
	return nil
}

func copyToRandomRecoveryFile(from, backup, expected string) (string, error) {
	if e := requireNormalFile(from); e != nil {
		return "", e
	}
	f, e := os.CreateTemp(backup, ".dds-recovery-")
	if e != nil {
		return "", e
	}
	name := f.Name()
	if e = f.Close(); e != nil {
		os.Remove(name)
		return "", e
	}
	// copyCheckedFile requires an exclusive destination.
	if e = os.Remove(name); e != nil {
		return "", e
	}
	if e = copyCheckedFile(from, name, expected); e != nil {
		os.Remove(name)
		return "", e
	}
	return name, nil
}

// All backup/root file identities must be validated before ANY restoration.
// Root file must match either old or new transaction SHA, or be absent.
func preflightCompanionRecovery(root string, tx pendingCompanionTransaction) error {
	if e := validateCompanionTransaction(tx.Journal); e != nil {
		return e
	}
	if e := requireNormalDir(filepath.Join(root, "versions", tx.Journal.PreviousVersion)); e != nil {
		return fmt.Errorf("предыдущая версия недоступна: %w", e)
	}
	if e := requireNormalDir(tx.Backup); e != nil {
		return e
	}
	for _, n := range bootstrapRootNames {
		prev := filepath.Join(tx.Backup, "previous-"+n)
		if e := requireNormalFile(prev); e != nil {
			return e
		}
		h, _, e := shaFile(prev)
		if e != nil {
			return e
		}
		if h != tx.Journal.PreviousSHA256[n] {
			return fmt.Errorf("резервная копия %s повреждена", n)
		}
		current := filepath.Join(root, n)
		if _, e = os.Lstat(current); os.IsNotExist(e) {
			continue
		} else if e != nil {
			return e
		}
		if e = requireNormalFile(current); e != nil {
			return e
		}
		h, _, e = shaFile(current)
		if e != nil {
			return e
		}
		if h != tx.Journal.PreviousSHA256[n] && h != tx.Journal.NextSHA256[n] {
			return fmt.Errorf("%s изменён независимо: восстановление заблокировано", n)
		}
	}
	return nil
}

func recoverCompanionInterrupted(root string, closed func() error) (string, error) {
	if !filepath.IsAbs(root) || requireNormalDir(root) != nil {
		return "", errors.New("небезопасный путь DDS")
	}
	if closed == nil {
		return "", errors.New("нет обязательной проверки закрытого DDS")
	}
	transactions, e := pendingCompanionTransactions(root)
	if e != nil {
		return "", e
	}
	if len(transactions) != 1 {
		return "", fmt.Errorf("для восстановления требуется ровно одна незавершённая транзакция, обнаружено %d", len(transactions))
	}
	tx := transactions[0]
	if e = preflightCompanionRecovery(root, tx); e != nil {
		return tx.Backup, e
	}
	if e = closed(); e != nil {
		return tx.Backup, e
	}
	// Stage three verified restore files before touching the live root.
	staged := map[string]string{}
	defer func() {
		for _, p := range staged {
			os.Remove(p)
		}
	}()
	for _, n := range bootstrapRootNames {
		p, e := copyToRandomRecoveryFile(filepath.Join(tx.Backup, "previous-"+n), tx.Backup, tx.Journal.PreviousSHA256[n])
		if e != nil {
			return tx.Backup, e
		}
		staged[n] = p
	}
	if e = preflightCompanionRecovery(root, tx); e != nil {
		return tx.Backup, e
	}
	if e = closed(); e != nil {
		return tx.Backup, e
	}
	// Restore current.json first to ensure a valid previous pointer as early
	// as possible. An interruption during recovery leaves journal pending.
	for _, n := range []string{"current.json", "DDSUpdater.exe", "DDS.exe"} {
		if e = closed(); e != nil {
			return tx.Backup, e
		}
		current := filepath.Join(root, n)
		if _, e = os.Lstat(current); e == nil {
			if e = requireNormalFile(current); e != nil {
				return tx.Backup, e
			}
			h, _, ee := shaFile(current)
			if ee != nil {
				return tx.Backup, ee
			}
			if h != tx.Journal.PreviousSHA256[n] && h != tx.Journal.NextSHA256[n] {
				return tx.Backup, fmt.Errorf("%s изменён независимо; остановка", n)
			}
			if h == tx.Journal.PreviousSHA256[n] {
				continue
			}
			if e = makeAtomicReplacement(staged[n], current); e != nil {
				return tx.Backup, e
			}
			delete(staged, n)
		} else if os.IsNotExist(e) {
			if e = os.Rename(staged[n], current); e != nil {
				return tx.Backup, e
			}
			delete(staged, n)
		} else {
			return tx.Backup, e
		}
	}
	for _, n := range bootstrapRootNames {
		h, _, err := shaFile(filepath.Join(root, n))
		if err != nil || h != tx.Journal.PreviousSHA256[n] {
			return tx.Backup, fmt.Errorf("итоговая проверка %s не прошла: %v", n, err)
		}
	}
	if e = finishCompanionTransaction(tx.Backup, companionTxnRecovered); e != nil {
		return tx.Backup, e
	}
	return tx.Backup, nil
}

func runCompanionRecoveryMenu(inv Inventory, input io.Reader, out io.Writer, closed func() error) {
	reader := bufio.NewReader(input)
	fmt.Fprintln(out, "DDS Companion — восстановление прерванной установки (ручная операция)")
	root := ""
	if inv.Profile == "installed" && inv.Companion.Path != "" {
		root = filepath.Clean(inv.Companion.Path)
	} else {
		fmt.Fprintln(out, "Автоматически определить установленный DDS не удалось. Укажи абсолютный путь к папке DDS (или Enter для отмены):")
		line, _ := reader.ReadString('\n')
		root = filepath.Clean(strings.TrimSpace(line))
		// Explicit manual path is allowed ONLY for interrupted-transaction
		// recovery, never for a new installation or portable application.
		if !filepath.IsAbs(root) || requireNormalDir(root) != nil {
			fmt.Fprintln(out, "BLOCKED: путь не является существующей абсолютной папкой")
			return
		}
		if _, e := os.Lstat(filepath.Join(root, "Data")); e == nil || !os.IsNotExist(e) {
			fmt.Fprintln(out, "BLOCKED: возможный Portable DataRoot внутри корня")
			return
		}
	}
	tx, e := pendingCompanionTransactions(root)
	if e != nil {
		fmt.Fprintln(out, "BLOCKED:", e)
		return
	}
	if len(tx) != 1 {
		fmt.Fprintf(out, "BLOCKED: незавершённых транзакций %d (ожидается 1)\n", len(tx))
		return
	}
	if e = preflightCompanionRecovery(root, tx[0]); e != nil {
		fmt.Fprintln(out, "BLOCKED:", e)
		return
	}
	if closed == nil || closed() != nil {
		fmt.Fprintln(out, "BLOCKED: DDS запущен или проверка процессов недоступна")
		return
	}
	fmt.Fprintf(out, "Найдена прерванная установка %s → %s\nРезервная копия: %s\n", tx[0].Journal.PreviousVersion, tx[0].Journal.NextVersion, tx[0].Backup)
	fmt.Fprintln(out, "Будут восстановлены только три прежних корневых файла DDS. SQLite, DataRoot и старая версия не затрагиваются.")
	if !confirmNumeric(reader, out, "Восстановить прежние файлы DDS Companion из резервной копии?") {
		fmt.Fprintln(out, "ОТМЕНЕНО: файловые изменения не выполнялись")
		return
	}
	backup, e := recoverCompanionInterrupted(root, closed)
	if e != nil {
		fmt.Fprintln(out, "BLOCKED / ОШИБКА:", e, "; backup:", backup)
		return
	}
	fmt.Fprintln(out, "RECOVERED_AND_VERIFIED. Исходные bootstrap файлы восстановлены; backup:", backup)
}
