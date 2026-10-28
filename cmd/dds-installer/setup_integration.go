package main

// First-install integration: orchestrate independently validated floors and
// perform bounded startup verification. A failed floor stops its dependants; already healthy
// components are not rewritten. This is NOT the Repair/Uninstall workflow.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type setupStep struct {
	Component string
	Result    string
	Reason    string
}

type setupReport struct {
	Steps                       []setupStep
	Status                      string
	CompanionFirstStartVerified bool // not yet implemented by this floor
}

type firstSetupHooks struct {
	Detect               func() Inventory
	InstallDiscord       func(context.Context) error
	InstallBetterDiscord func(context.Context) error
	InstallPlugin        func(Inventory) error
	InstallCompanion     func(Inventory) error
}

var firstSetupOrder = []string{"discord", "betterdiscord", "plugin", "companion"}

func setupComponent(i Inventory, key string) Component {
	switch key {
	case "discord":
		return i.Discord
	case "betterdiscord":
		return i.BetterDiscord
	case "plugin":
		return i.Plugin
	case "companion":
		return i.Companion
	default:
		return Component{State: StatePartial}
	}
}

// Reject ambiguous, partially installed and Portable states rather than
// mistaking the first-install mode for permission to repair/migrate them.
func planFirstSetup(i Inventory) ([]string, error) {
	if strings.HasPrefix(i.Profile, "portable") {
		return nil, errors.New("Portable профиль: требуется отдельный профильный Setup/Repair; первичная Installed-установка запрещена")
	}
	for _, part := range []Component{i.Discord, i.BetterDiscord, i.Plugin, i.Companion} {
		if part.State == StatePartial {
			return nil, fmt.Errorf("%s: состояние PARTIAL; нужен отдельный Repair, не первичная установка", part.Name)
		}
	}
	if i.Discord.State != StateOK && i.Discord.State != StateMissing {
		return nil, errors.New("Discord: неоднозначное состояние")
	}
	if i.BetterDiscord.State != StateOK && i.BetterDiscord.State != StateMissing && i.BetterDiscord.State != StateBlocked {
		return nil, errors.New("BetterDiscord: неоднозначное состояние")
	}
	if i.Plugin.State != StateOK && i.Plugin.State != StateMissing && i.Plugin.State != StateBlocked {
		return nil, errors.New("Plugin: неоднозначное состояние")
	}
	if i.Companion.State != StateOK && i.Companion.State != StateMissing && i.Companion.State != StateNotDetected {
		return nil, errors.New("Companion: неоднозначное состояние")
	}
	if i.Discord.State == StateOK && i.BetterDiscord.State == StateBlocked {
		return nil, errors.New("BetterDiscord заблокирован при уже установленном Discord")
	}
	if i.BetterDiscord.State == StateOK && i.Plugin.State == StateBlocked {
		return nil, errors.New("Plugin заблокирован при уже установленном BetterDiscord")
	}
	need := make([]string, 0, 4)
	for _, key := range firstSetupOrder {
		if setupComponent(i, key).State != StateOK {
			need = append(need, key)
		}
	}
	return need, nil
}

func stageReady(need []string, got []stageOutcome) error {
	if len(got) != len(need) {
		return errors.New("подготовлены не все необходимые компоненты")
	}
	for n, key := range need {
		s := got[n]
		if s.Component != key || (s.Result != "VERIFIED_AND_STAGED" && s.Result != "VERIFIED_CACHED") {
			return fmt.Errorf("%s: нет подтверждённого проверенного staging: %s", key, s.Reason)
		}
	}
	return nil
}

func executeFirstSetup(ctx context.Context, initial Inventory, prepared []stageOutcome, h firstSetupHooks) setupReport {
	report := setupReport{Status: "BLOCKED"}
	need, err := planFirstSetup(initial)
	if err != nil {
		report.Steps = append(report.Steps, setupStep{"preflight", "BLOCKED", err.Error()})
		return report
	}
	if len(need) == 0 {
		report.Status = "ALREADY_HEALTHY_NO_CHANGE"
		return report
	}
	if err = stageReady(need, prepared); err != nil {
		report.Steps = append(report.Steps, setupStep{"stage", "BLOCKED", err.Error()})
		return report
	}
	if h.Detect == nil || h.InstallDiscord == nil || h.InstallBetterDiscord == nil || h.InstallPlugin == nil || h.InstallCompanion == nil {
		report.Steps = append(report.Steps, setupStep{"preflight", "BLOCKED", "неполный набор операций Setup"})
		return report
	}
	// No mutating hook can execute until all required packages passed staging.
	for _, key := range firstSetupOrder {
		if ctx.Err() != nil {
			report.Steps = append(report.Steps, setupStep{key, "BLOCKED", ctx.Err().Error()})
			return report
		}
		inv := h.Detect()
		state := setupComponent(inv, key).State
		if state == StateOK {
			report.Steps = append(report.Steps, setupStep{key, "ALREADY_PRESENT", "без изменений"})
			continue
		}
		if key == "betterdiscord" && inv.Discord.State != StateOK || key == "plugin" && (inv.Discord.State != StateOK || inv.BetterDiscord.State != StateOK) {
			report.Steps = append(report.Steps, setupStep{key, "BLOCKED", "зависимость не подтверждена после предыдущего шага"})
			return report
		}
		if key == "companion" && state != StateNotDetected && state != StateMissing {
			report.Steps = append(report.Steps, setupStep{key, "BLOCKED", "существующий/неоднозначный Companion; нужен Repair"})
			return report
		}
		if key == "discord" && state != StateMissing || key == "betterdiscord" && state != StateMissing && state != StateBlocked || key == "plugin" && state != StateMissing && state != StateBlocked {
			report.Steps = append(report.Steps, setupStep{key, "BLOCKED", "изменилось состояние; требуется новое планирование"})
			return report
		}
		var e error
		switch key {
		case "discord":
			e = h.InstallDiscord(ctx)
		case "betterdiscord":
			e = h.InstallBetterDiscord(ctx)
		case "plugin":
			e = h.InstallPlugin(inv)
		case "companion":
			e = h.InstallCompanion(inv)
		}
		if e != nil {
			report.Steps = append(report.Steps, setupStep{key, "FAILED", e.Error()})
			report.Status = "PARTIAL_OR_BLOCKED"
			return report
		}
		fresh := h.Detect()
		if setupComponent(fresh, key).State != StateOK {
			report.Steps = append(report.Steps, setupStep{key, "FAILED", "операция завершилась, но установленный компонент не подтверждён повторным обнаружением"})
			report.Status = "PARTIAL_OR_BLOCKED"
			return report
		}
		report.Steps = append(report.Steps, setupStep{key, "FILES_VALIDATED", "повторное обнаружение OK"})
	}
	report.Status = "FILES_INTEGRATED_FIRST_START_PENDING"
	// Neither a detected DDS.exe nor a copied file proves startup/GUI/DataRoot.
	return report
}

// Compare the exact approved receipt with the staged bytes once more before
// an external process or product Apply is allowed to touch installed files.
func verifyExactSetupStage(root string, expected stageOutcome) error {
	if err := requireNormalDir(root); err != nil {
		return err
	}
	var sha, version, path string
	switch expected.Component {
	case "discord":
		file, e := verifiedDiscordStageFile(root, verifyDiscordSignature)
		if e != nil {
			return e
		}
		h, _, e := shaFile(file)
		if e != nil {
			return e
		}
		sha = h
		version = "official Stable x64 (installer)"
		path = filepath.Dir(filepath.Dir(file))
	case "betterdiscord", "plugin":
		var rec preparedExternal
		var ok bool
		if expected.Component == "betterdiscord" {
			rec, path, ok = validExternalCache(root, "betterdiscord", false)
			if !ok {
				return errors.New("BetterDiscord CLI staging не прошёл повторную проверку")
			}
		} else {
			var payload string
			var e error
			rec, payload, e = verifiedPluginStage(root)
			if e != nil {
				return e
			}
			path = filepath.Dir(filepath.Dir(payload))
		}
		sha, version = rec.SHA256, rec.Version
	case "companion":
		rec, p, ok := validCompanionCache(root, false)
		if !ok {
			return errors.New("Companion staging не прошёл повторную проверку")
		}
		sha, version, path = rec.SHA256, rec.Version, p
	default:
		return errors.New("неизвестный компонент в пакете Setup")
	}
	if !strings.EqualFold(path, expected.Location) || !strings.EqualFold(sha, expected.SHA256) || version != expected.Version {
		return fmt.Errorf("%s: изменился проверенный кандидат с момента подтверждения", expected.Component)
	}
	return nil
}

func preparedStage(got []stageOutcome, key string) stageOutcome {
	for _, s := range got {
		if s.Component == key {
			return s
		}
	}
	return stageOutcome{}
}

func verifiedDiscordStageFile(root string, sign func(string) error) (string, error) {
	if sign == nil {
		return "", errors.New("отсутствует проверка цифровой подписи Discord")
	}
	for _, dir := range []string{root, filepath.Join(root, "discord"), filepath.Join(root, "discord", "official-stable-x64"), filepath.Join(root, "discord", "official-stable-x64", "package")} {
		if err := requireNormalDir(dir); err != nil {
			return "", err
		}
	}
	d := filepath.Join(root, "discord", "official-stable-x64")
	receiptPath := filepath.Join(d, "stage_receipt.json")
	if err := requireNormalFile(receiptPath); err != nil {
		return "", err
	}
	b, err := os.ReadFile(receiptPath)
	if err != nil {
		return "", err
	}
	var r preparedExternal
	if json.Unmarshal(b, &r) != nil || r.Schema != "dds-installer-external-stage-v1" || r.Component != "Discord" || r.Source != discordInstallerURL || r.Status != "VERIFIED_AND_STAGED" || r.Version != "official Stable x64 (installer)" || !hexDigestRE.MatchString(r.SHA256) || r.ZIPSize <= 0 || r.ZIPSize > externalMaxSize {
		return "", errors.New("неверный Discord staging receipt")
	}
	target := filepath.Join(d, "package", "DiscordSetup.exe")
	if err := requireNormalFile(target); err != nil {
		return "", err
	}
	actual, n, err := shaFile(target)
	if err != nil {
		return "", err
	}
	if actual != r.SHA256 || n != r.ZIPSize {
		return "", errors.New("Discord staging SHA-256/size mismatch")
	}
	if err := verifyWindowsPE(target); err != nil {
		return "", err
	}
	if err := sign(target); err != nil {
		return "", fmt.Errorf("Discord Authenticode: %w", err)
	}
	return target, nil
}

func verifiedBetterDiscordCLIFile(root string) (string, error) {
	_, path, ok := validExternalCache(root, "betterdiscord", false)
	if !ok {
		return "", errors.New("нет проверенного BetterDiscord CLI staging")
	}
	exe := filepath.Join(path, "payload", "bdcli.exe")
	if err := requireNormalFile(exe); err != nil {
		return "", err
	}
	return exe, nil
}

// After an external installer exits, wait for the actual installed state:
// many Windows installers spawn an asynchronous child and exit first.
func awaitSetupComponent(ctx context.Context, maxWait time.Duration, confirmed func() bool) error {
	timeout, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if confirmed() {
			return nil
		}
		select {
		case <-timeout.Done():
			return fmt.Errorf("компонент не обнаружен после установки: %w", timeout.Err())
		case <-ticker.C:
		}
	}
}

// Never buffer unlimited stdout from a downloaded external tool.
type boundedOutput struct{ last []byte }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	b.last = append(b.last, p...)
	if len(b.last) > 4096 {
		b.last = append([]byte(nil), b.last[len(b.last)-4096:]...)
	}
	return n, nil
}

func externalRun(ctx context.Context, exe string, args ...string) error {
	if runtime.GOOS != "windows" {
		return errors.New("Windows external installer only")
	}
	if err := requireNormalFile(exe); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, exe, args...)
	hideInstallerCommand(cmd)
	// Bounded output, no shell interpolation, and no ambient stdin prompts.
	output := &boundedOutput{}
	cmd.Stdout, cmd.Stderr = output, output
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("внешний установщик завершился с ошибкой: %v: %s", err, strings.TrimSpace(string(output.last)))
	}
	return nil
}

// Run the official installer, not an invented undocumented silent flag. A
// Squirrel-style updater may detach; a later Detect is authoritative.
func launchVerifiedDiscord(ctx context.Context, root string) error {
	p, err := verifiedDiscordStageFile(root, verifyDiscordSignature)
	if err != nil {
		return err
	}
	return externalRun(ctx, p)
}
func launchVerifiedBetterDiscord(ctx context.Context, root string) error {
	p, err := verifiedBetterDiscordCLIFile(root)
	if err != nil {
		return err
	}
	// Official BetterDiscord CLI docs, Stable only, no --dev or Canary.
	return externalRun(ctx, p, "--silent", "install", "--channel", "stable")
}

// Final status describes files only; live Discord/Plugin heartbeat and real
// Companion first start must be validated by later Windows acceptance gates.
func printFirstSetupReport(w io.Writer, r setupReport) {
	fmt.Fprintln(w, "\nDDS Setup — итоги по компонентам:")
	for _, s := range r.Steps {
		fmt.Fprintf(w, "  %s: %s (%s)\n", labelComponent(s.Component), s.Result, s.Reason)
	}
	fmt.Fprintln(w, "FILES STATUS:", r.Status)
	if r.Status == "FILES_INTEGRATED_FIRST_START_PENDING" {
		fmt.Fprintln(w, "Файлы интегрированы; запускаем Discord и Companion и ожидаем heartbeat.")
	}
}

// First Setup automatically handles Discord lifecycle after the user's
// one explicit approval for the complete installation.
func sharedBufferedReader(r io.Reader) *bufio.Reader {
	if b, ok := r.(*bufio.Reader); ok {
		return b
	}
	return bufio.NewReader(r)
}

func setupInventoryNow(localAppData, appData, installerDir string) Inventory {
	return Detect(DetectContext{LocalAppData: localAppData, AppData: appData, InstallerDir: installerDir, Processes: runningDDSProcesses()})
}

func runFirstSetupMenu(initial Inventory, input io.Reader, out io.Writer, localAppData, appData, installerDir string) {
	plan, err := planFirstSetup(initial)
	if err != nil {
		fmt.Fprintln(out, "SETUP BLOCKED:", err)
		return
	}
	if len(plan) == 0 {
		fmt.Fprintln(out, "ALREADY_HEALTHY_NO_CHANGE: переписывать ничего не требуется")
		return
	}
	if !filepath.IsAbs(localAppData) || !filepath.IsAbs(appData) || requireNormalDir(localAppData) != nil || requireNormalDir(appData) != nil {
		fmt.Fprintln(out, "SETUP BLOCKED: Windows LOCALAPPDATA/APPDATA не определены безопасно")
		return
	}
	fmt.Fprintln(out, "DDS Setup — ПЕРВИЧНАЯ УСТАНОВКА Installed (не Repair/Portable):")
	for _, n := range plan {
		fmt.Fprintln(out, "  →", labelComponent(n))
	}
	fmt.Fprintln(out, "Сначала будут скачаны и проверены ВСЕ нужные пакеты, затем потребуется отдельное подтверждение установки.")
	reader := sharedBufferedReader(input)
	if !confirmNumeric(reader, out, "Скачать и проверить необходимые пакеты?") {
		fmt.Fprintln(out, "Отменено без изменений")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	stageRoot := filepath.Join(os.TempDir(), "DDS_Installer_Stage")
	c := newTransferClient()
	c.Progress = func(msg string) { fmt.Fprintln(out, msg) }
	prepared := runCascade(ctx, c, stageRoot, plan, verifyDiscordSignature)
	if err = stageReady(plan, prepared); err != nil {
		fmt.Fprintln(out, "SETUP BLOCKED: staging не завершён:", err)
		return
	}
	// All four checked receipts must still match the exact candidates the user
	// just downloaded, not an unapproved newer cached package.
	for _, candidate := range prepared {
		if e := verifyExactSetupStage(stageRoot, candidate); e != nil {
			fmt.Fprintln(out, "SETUP BLOCKED: проверка конкретного пакета:", e)
			return
		}
	}
	// Re-detection prevents approving an obsolete plan after long downloads.
	fresh := setupInventoryNow(localAppData, appData, installerDir)
	currentPlan, e := planFirstSetup(fresh)
	if e != nil || strings.Join(currentPlan, ",") != strings.Join(plan, ",") {
		fmt.Fprintln(out, "SETUP BLOCKED: состояние изменилось во время подготовки; повтори запуск")
		return
	}
	fmt.Fprintln(out, "Пакеты проверены. Installer сам закроет Discord перед BetterDiscord/Plugin и затем запустит Discord и DDS Companion.")
	fmt.Fprintln(out, "Discord может закрыться принудительно, если не завершится штатно. Сохрани черновики и выйди из голосового чата до подтверждения.")
	fmt.Fprintln(out, "DDS Plugin будет скопирован с проверкой и резервной копией; Companion только в НОВУЮ папку %LOCALAPPDATA%\\DDS.")
	fmt.Fprintln(out, "Рабочий DDS, SQLite, DDS_Data и DataRoot нельзя переносить/перезаписывать этим режимом.")
	if !confirmNumeric(reader, out, "Установить перечисленные компоненты на этот компьютер?") {
		fmt.Fprintln(out, "Установка отменена; подготовленные пакеты остаются в TEMP")
		return
	}
	hooks := firstSetupHooks{
		Detect: func() Inventory { return setupInventoryNow(localAppData, appData, installerDir) },
		InstallDiscord: func(ctx context.Context) error {
			if err := verifyExactSetupStage(stageRoot, preparedStage(prepared, "discord")); err != nil {
				return err
			}
			if err := launchVerifiedDiscord(ctx, stageRoot); err != nil {
				return err
			}
			return awaitSetupComponent(ctx, 90*time.Second, func() bool {
				return setupInventoryNow(localAppData, appData, installerDir).Discord.State == StateOK
			})
		},
		InstallBetterDiscord: func(ctx context.Context) error {
			if err := ensureDiscordStopped(ctx, discordClientIsRunning, stopDiscordForSetup); err != nil {
				return err
			}
			if err := verifyExactSetupStage(stageRoot, preparedStage(prepared, "betterdiscord")); err != nil {
				return err
			}
			if err := launchVerifiedBetterDiscord(ctx, stageRoot); err != nil {
				return err
			}
			return awaitSetupComponent(ctx, 30*time.Second, func() bool {
				return setupInventoryNow(localAppData, appData, installerDir).BetterDiscord.State == StateOK
			})
		},
		InstallPlugin: func(inv Inventory) error {
			if err := verifyExactSetupStage(stageRoot, preparedStage(prepared, "plugin")); err != nil {
				return err
			}
			if err := ensureDiscordStopped(ctx, discordClientIsRunning, stopDiscordForSetup); err != nil {
				return err
			}
			result, err := applyPlugin(inv, stageRoot, appData)
			if err != nil {
				return err
			}
			if result.Status == "ALREADY_CURRENT" {
				return nil
			}
			return enableNewDDSPlugin(appData)
		},
		InstallCompanion: func(inv Inventory) error {
			if err := verifyExactSetupStage(stageRoot, preparedStage(prepared, "companion")); err != nil {
				return err
			}
			result, err := freshCompanionInstall(inv, stageRoot, localAppData, checkNoCompanionProcesses)
			if err != nil {
				return err
			}
			if result.Status != "FILES_INSTALLED_UNSTARTED" {
				return errors.New("Companion не подтвердил установку файлов")
			}
			return nil
		},
	}
	report := executeFirstSetup(ctx, fresh, prepared, hooks)
	printFirstSetupReport(out, report)
	if report.Status != "FILES_INTEGRATED_FIRST_START_PENDING" {
		return
	}
	firstStart := startupHooks{
		StartDiscord:     func() error { return startInstalledDiscord(localAppData, appData, installerDir) },
		StartCompanion:   func() error { return startInstalledCompanion(localAppData) },
		DiscordRunning:   discordClientIsRunning,
		CompanionRunning: companionClientIsRunning,
		PluginHeartbeat:  func() bool { return freshPluginHeartbeat(appData) },
		ArchiveReady:     func() bool { return sqliteHeaderPresent(localAppData) },
	}
	if err := runSetupFirstStart(ctx, out, firstStart); err != nil {
		fmt.Fprintln(out, "STATUS: FIRST_START_INCOMPLETE —", err)
		fmt.Fprintln(out, "Установленные файлы сохранены; не объявляем DDS работающим до успешного heartbeat.")
	} else {
		fmt.Fprintln(out, "STATUS: RUNTIME_READY — Discord, Companion, DDS Plugin heartbeat и SQLite подтверждены")
	}

}
