//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

type knownFolderGUID struct {
	A    uint32
	B, C uint16
	D    [8]byte
}

func hideInstallerCommand(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }

func knownFolderPath(id knownFolderGUID) string {
	var ptr *uint16
	r, _, _ := syscall.NewLazyDLL("shell32.dll").NewProc("SHGetKnownFolderPath").Call(uintptr(unsafe.Pointer(&id)), 0, 0, uintptr(unsafe.Pointer(&ptr)))
	if int32(r) != 0 || ptr == nil {
		return ""
	}
	defer syscall.NewLazyDLL("ole32.dll").NewProc("CoTaskMemFree").Call(uintptr(unsafe.Pointer(ptr)))
	return syscall.UTF16ToString(unsafe.Slice(ptr, 32768))
}

func defaultProgramRoot() string {
	base := knownFolderPath(knownFolderGUID{0x905e63b6, 0xc1bf, 0x494e, [8]byte{0xb2, 0x9c, 0x65, 0xb7, 0x32, 0xd3, 0xd2, 0x1a}})
	if base == "" {
		base = os.Getenv("ProgramW6432")
	}
	if base == "" {
		base = os.Getenv("ProgramFiles")
	}
	return filepath.Join(base, "DDS")
}

func desktopDirectory() string {
	return knownFolderPath(knownFolderGUID{0xb4bfcc3a, 0xdb2c, 0x424c, [8]byte{0xb0, 0x29, 0x7f, 0xe9, 0x9a, 0x87, 0xc6, 0x41}})
}

func powershellLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func hiddenPowerShell(ctx context.Context, script string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; [Console]::OutputEncoding=[Text.UTF8Encoding]::new(); "+script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.CombinedOutput()
}

func registerWindowsInstall(local, root string, shortcut bool) error {
	if requireNormalFile(filepath.Join(root, "DDS.exe")) != nil {
		return errors.New("стабильный DDS.exe не найден")
	}
	previous := readOwnership(local)
	record := installerOwnership{Schema: "dds-installer-ownership-v1", Root: root, Version: installerVersion}
	if strings.EqualFold(previous.Root, root) {
		record.Shortcut = previous.Shortcut
		record.PermissionPolicy = previous.PermissionPolicy
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if shortcut {
		link, err := writeDesktopShortcut(root, desktopDirectory(), true, previous)
		if err != nil {
			return err
		}
		record.Shortcut = link
	}
	// HKCU registration and ownership are for this user; no fake Uninstall entry.
	script := `$key=[Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Software\DDS\Installer'); try { $key.SetValue('InstallRoot', ` + powershellLiteral(root) + ",[Microsoft.Win32.RegistryValueKind]::String); $key.SetValue('DesktopShortcut'," + powershellLiteral(record.Shortcut) + ",[Microsoft.Win32.RegistryValueKind]::String) } finally { $key.Dispose() }"
	if out, err := hiddenPowerShell(ctx, script); err != nil {
		return fmt.Errorf("регистрация DDS: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return saveOwnership(local, record)
}

// Explicit desktop injection keeps native .lnk tests away from the real desktop.
func writeDesktopShortcut(root, desktop string, enabled bool, previous installerOwnership) (string, error) {
	if !enabled {
		return "", nil
	}
	if desktop == "" || requireNormalDir(desktop) != nil || requireNormalFile(filepath.Join(root, "DDS.exe")) != nil {
		return "", errors.New("недоступна папка ярлыка или DDS.exe")
	}
	link := filepath.Join(desktop, "DDS.lnk")
	if _, err := os.Lstat(link); err == nil {
		if err = requireNormalFile(link); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	previousTarget := ""
	if previous.Schema == "dds-installer-ownership-v1" && previous.Root != "" && previous.Shortcut != "" && strings.EqualFold(filepath.Clean(previous.Shortcut), filepath.Clean(link)) {
		previousTarget = filepath.Join(previous.Root, "DDS.exe")
	}
	script := "$shell=New-Object -ComObject WScript.Shell; $path=" + powershellLiteral(link) + "; $target=" + powershellLiteral(filepath.Join(root, "DDS.exe")) + "; $previousTarget=" + powershellLiteral(previousTarget) + "; $s=$shell.CreateShortcut($path); $present=Test-Path -LiteralPath $path; if($present -and $s.TargetPath -ne $target -and ([string]::IsNullOrEmpty($previousTarget) -or $s.TargetPath -ne $previousTarget)){throw 'DDS.lnk points to another installation'}; if($present -and $s.IconLocation -eq ($target+',0') -and $s.WorkingDirectory -eq " + powershellLiteral(root) + "){exit 0}; $s.TargetPath=$target; $s.WorkingDirectory=" + powershellLiteral(root) + "; $s.IconLocation=$target+',0'; $s.Description='DDS - Discord Data Snatcher'; $s.Save(); if(-not(Test-Path -LiteralPath $path)){throw 'Shortcut not created'}"
	if out, err := hiddenPowerShell(ctx, script); err != nil {
		return "", fmt.Errorf("ярлык DDS: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return link, nil
}

func processElevated() bool {
	var token syscall.Token
	if syscall.OpenProcessToken(syscall.Handle(^uintptr(0)), syscall.TOKEN_QUERY, &token) != nil {
		return false
	}
	defer token.Close()
	var elevation, size uint32
	r, _, _ := syscall.NewLazyDLL("advapi32.dll").NewProc("GetTokenInformation").Call(uintptr(token), 20, uintptr(unsafe.Pointer(&elevation)), 4, uintptr(unsafe.Pointer(&size)))
	return r != 0 && elevation != 0
}

// With UAC disabled, an administrator's ordinary desktop token is elevated too.
// Permit that existing Windows configuration; never change it ourselves. With
// UAC enabled (or an unreadable policy), keep rejecting elevated user processes.
func userProcessElevationBlocked() bool {
	return elevationBlockedByPolicy(processElevated(), uacExplicitlyDisabled())
}

func elevationBlockedByPolicy(elevated, uacDisabled bool) bool {
	return elevated && !uacDisabled
}

func uacExplicitlyDisabled() bool {
	var key syscall.Handle
	path, err := syscall.UTF16PtrFromString(`SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`)
	if err != nil || syscall.RegOpenKeyEx(syscall.HKEY_LOCAL_MACHINE, path, 0, syscall.KEY_READ|syscall.KEY_WOW64_64KEY, &key) != nil {
		return false
	}
	defer syscall.RegCloseKey(key)
	name, _ := syscall.UTF16PtrFromString("EnableLUA")
	var kind, value uint32
	size := uint32(4)
	if syscall.RegQueryValueEx(key, name, nil, &kind, (*byte)(unsafe.Pointer(&value)), &size) != nil {
		return false
	}
	return kind == syscall.REG_DWORD && size == 4 && value == 0
}

func ownerModifyPermission(root, sid string) error {
	if !regexp.MustCompile(`^S-1-5-21-\d+-\d+-\d+-\d+$`).MatchString(sid) {
		return errors.New("неверный SID владельца DDS")
	}
	if err := ensureNoLinksInTree(root); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(os.Getenv("SystemRoot"), "System32", "icacls.exe"), root, "/grant", "*"+sid+":(OI)(CI)M", "/T", "/Q")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("icacls: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

type shellExecuteInfo struct {
	Size, Mask                        uint32
	Hwnd                              uintptr
	Verb, File, Parameters, Directory *uint16
	Show                              int32
	Instance, IDList                  uintptr
	Class                             *uint16
	ClassKey                          uintptr
	HotKey                            uint32
	Icon, Process                     uintptr
}

type elevatedInstallReply struct {
	Result companionFreshResult
	Error  string
}

func guiInstallCompanion(ctx context.Context, inv Inventory, stage, target, local, app, installerDir string) (companionFreshResult, error) {
	if err := validateProgramTarget(target, inv.DataRoot.Path); err != nil {
		return companionFreshResult{}, err
	}
	owner, err := user.Current()
	if err != nil {
		return companionFreshResult{}, err
	}
	// Always request elevation through Windows for the bounded file-placement step.
	// The ordinary GUI, Discord/BD operations and final launching keep the user token.
	work, err := os.MkdirTemp("", "DDS-Installer-Apply-")
	if err != nil {
		return companionFreshResult{}, err
	}
	defer os.RemoveAll(work)
	resultPath := filepath.Join(work, "result.json")
	exe, err := os.Executable()
	if err != nil {
		return companionFreshResult{}, err
	}
	args := []string{"--dds-admin-install", stage, target, local, app, installerDir, owner.Uid, resultPath}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = syscall.EscapeArg(a)
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ole := syscall.NewLazyDLL("ole32.dll")
	hr, _, _ := ole.NewProc("CoInitializeEx").Call(0, 6)
	if int32(hr) >= 0 {
		defer ole.NewProc("CoUninitialize").Call()
	}
	info := shellExecuteInfo{Mask: 0x40 | 0x100, Hwnd: ui.hwnd, Verb: utf("runas"), File: utf(exe), Parameters: utf(strings.Join(quoted, " ")), Directory: utf(filepath.Dir(exe)), Show: 0}
	info.Size = uint32(unsafe.Sizeof(info))
	r, _, callErr := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteExW").Call(uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return companionFreshResult{}, fmt.Errorf("Windows не разрешила размещение файлов (UAC): %w", callErr)
	}
	if info.Process == 0 {
		return companionFreshResult{}, errors.New("Windows не вернула процесс установки")
	}
	defer syscall.CloseHandle(syscall.Handle(info.Process))
	// Never abandon a mutating elevated process on GUI timeout: wait for its outcome.
	for {
		wait, _, _ := kernel32.NewProc("WaitForSingleObject").Call(info.Process, 1000)
		if wait == 0 {
			break
		}
		if wait != 258 {
			return companionFreshResult{}, errors.New("не удалось дождаться процесса установки")
		}
	}
	b, err := os.ReadFile(resultPath)
	if err != nil {
		return companionFreshResult{}, errors.New("результат размещения файлов не получен")
	}
	var reply elevatedInstallReply
	if json.Unmarshal(b, &reply) != nil {
		return companionFreshResult{}, errors.New("повреждён ответ установки")
	}
	if reply.Error != "" {
		return reply.Result, errors.New(reply.Error)
	}
	if reply.Result.Status != "FILES_INSTALLED_UNSTARTED" || !strings.EqualFold(reply.Result.Root, target) {
		return reply.Result, errors.New("неверный результат размещения DDS")
	}
	_, cached, ok := validCompanionCache(stage, false)
	if !ok {
		return reply.Result, errors.New("исходный пакет изменился")
	}
	if err := compareVersionTree(filepath.Join(cached, "payload"), target); err != nil {
		return reply.Result, err
	}
	return reply.Result, nil
}

func runElevatedInstall(args []string) {
	if len(args) != 7 || !processElevated() {
		return
	}
	stage, target, local, app, installerDir, sid, resultFile := args[0], args[1], args[2], args[3], args[4], args[5], args[6]
	reply := elevatedInstallReply{}
	inv := setupInventoryAt(local, app, installerDir, target)
	if err := validateProgramTarget(target, filepath.Join(local, "DDS_Companion")); err != nil {
		reply.Error = err.Error()
	} else {
		result, err := freshCompanionInstallAt(inv, stage, target, checkNoCompanionProcesses, func(root string) error { return ownerModifyPermission(root, sid) })
		reply.Result = result
		if err != nil {
			reply.Error = err.Error()
		}
	}
	if filepath.IsAbs(resultFile) && strings.HasPrefix(filepath.Base(filepath.Dir(resultFile)), "DDS-Installer-Apply-") {
		b, _ := json.Marshal(reply)
		_ = os.WriteFile(resultFile, b, 0600)
	}
}

func setupInventoryAt(local, app, installerDir, root string) Inventory {
	return Detect(DetectContext{LocalAppData: local, AppData: app, InstallerDir: installerDir, InstallRoot: root, Processes: runningDDSProcesses()})
}

func startCompanionAt(root string) error {
	if err := requireNormalDir(root); err != nil {
		return err
	}
	ptr, err := readCurrent(root)
	if err != nil {
		return err
	}
	if ptr.Schema != "dds-version-pointer-v1" || !finalVersionRE.MatchString(ptr.Version) || ptr.RelativePath != "versions/"+ptr.Version {
		return errors.New("неверный указатель версии DDS")
	}
	if err := requireNormalFile(filepath.Join(root, filepath.FromSlash(ptr.RelativePath), "DDSApp.exe")); err != nil {
		return err
	}
	return spawnVerifiedClient(filepath.Join(root, "DDS.exe"))
}
