package main

// Companion upgrade is intentionally a separate, explicit operation.
// One immutable versioned payload is added; an existing version is never
// overwritten. current.json is switched last and only after verified copies.
// Previous bootstrap files and the prior version are preserved for recovery.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

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
