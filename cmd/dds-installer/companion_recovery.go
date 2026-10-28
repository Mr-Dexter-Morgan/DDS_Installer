package main

// Durable Companion transaction evidence for process-kill / interruption.
// A pending journal is created and synced BEFORE the first live bootstrap
// replacement. Recovery requires explicit user approval; no auto-repair.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

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
