//go:build windows

package main

import (
	"context"
	_ "embed"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const installerVersion = "0.9.0-gui-r7"
const (
	guiW          = 900
	guiH          = 610
	wmDestroy     = 2
	wmClose       = 0x10
	wmPaint       = 0x0f
	wmLButtonUp   = 0x0202
	wmKeyDown     = 0x0100
	wmAppUpdate   = 0x8001
	wsCaption     = 0x00c00000
	wsSysMenu     = 0x00080000
	wsMinimizeBox = 0x00020000
	swShow        = 5
	dtSingleLine  = 0x20
	dtVCenter     = 4
	dtCenter      = 1
	dtEndEllipsis = 0x8000
	srccopy       = 0x00cc0020
)

//go:embed gui_assets/background.bgra
var backgroundPixels []byte

var (
	user32            = syscall.NewLazyDLL("user32.dll")
	gdi32             = syscall.NewLazyDLL("gdi32.dll")
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	pRegisterClass    = user32.NewProc("RegisterClassExW")
	pCreateWindow     = user32.NewProc("CreateWindowExW")
	pDefWindowProc    = user32.NewProc("DefWindowProcW")
	pShowWindow       = user32.NewProc("ShowWindow")
	pUpdateWindow     = user32.NewProc("UpdateWindow")
	pDestroyWindow    = user32.NewProc("DestroyWindow")
	pPostQuit         = user32.NewProc("PostQuitMessage")
	pGetMessage       = user32.NewProc("GetMessageW")
	pTranslateMessage = user32.NewProc("TranslateMessage")
	pDispatchMessage  = user32.NewProc("DispatchMessageW")
	pPostMessage      = user32.NewProc("PostMessageW")
	pInvalidateRect   = user32.NewProc("InvalidateRect")
	pBeginPaint       = user32.NewProc("BeginPaint")
	pEndPaint         = user32.NewProc("EndPaint")
	pFillRect         = user32.NewProc("FillRect")
	pDrawText         = user32.NewProc("DrawTextW")
	pDrawIconEx       = user32.NewProc("DrawIconEx")
	pLoadIcon         = user32.NewProc("LoadIconW")
	pLoadCursor       = user32.NewProc("LoadCursorW")
	pAdjustWindowRect = user32.NewProc("AdjustWindowRect")
	pMessageBox       = user32.NewProc("MessageBoxW")
	pSetBkMode        = gdi32.NewProc("SetBkMode")
	pSetTextColor     = gdi32.NewProc("SetTextColor")
	pCreateBrush      = gdi32.NewProc("CreateSolidBrush")
	pDeleteObj        = gdi32.NewProc("DeleteObject")
	pCreateFont       = gdi32.NewProc("CreateFontW")
	pSelectObj        = gdi32.NewProc("SelectObject")
	pStretchDIBits    = gdi32.NewProc("StretchDIBits")
	pGetModule        = kernel32.NewProc("GetModuleHandleW")
	pSetDPIAware      = user32.NewProc("SetProcessDPIAware")
	pGetWindowText    = user32.NewProc("GetWindowTextW")
	pSetWindowText    = user32.NewProc("SetWindowTextW")
	pEnableWindow     = user32.NewProc("EnableWindow")
)

type guiRect struct{ Left, Top, Right, Bottom int32 }
type guiPoint struct{ X, Y int32 }
type windowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	IconSmall                          uintptr
}
type guiMessage struct {
	Hwnd           uintptr
	Message        uint32
	_              uint32
	Wparam, Lparam uintptr
	Time           uint32
	Pt             guiPoint
	Private        uint32
}
type guiPaint struct {
	Hdc                uintptr
	Erase              int32
	Rc                 guiRect
	Restore, IncUpdate int32
	Reserved           [32]byte
}
type bitmapHeader struct {
	Size                   uint32
	Width, Height          int32
	Planes, BitCount       uint16
	Compression, SizeImage uint32
	XPels, YPels           int32
	Used, Important        uint32
}
type bitmapInfo struct {
	Header bitmapHeader
	Color  uint32
}

type installGUI struct {
	hwnd                                       uintptr
	mu                                         sync.Mutex
	page                                       int // 0 setup, 1 install, 2 finish
	running, finished, successful              bool
	progress                                   int
	activity                                   string
	lines                                      []string
	fault                                      string
	selectedDiscord, selectedCompanion         bool
	quitWithFinish                             bool
	selectedFloors                             [4]bool
	installRoot                                string
	createShortcut                             bool
	pathEdit, browseButton                     uintptr
	download                                   downloadProgress
	detail                                     string
	state                                      Inventory
	healthy                                    bool
	fontNormal, fontBold, fontSmall, fontTitle uintptr
}

var ui = &installGUI{selectedDiscord: true, selectedCompanion: true, createShortcut: true, selectedFloors: [4]bool{true, true, true, true}}

func utf(s string) *uint16     { return syscall.StringToUTF16Ptr(s) }
func rgb(r, g, b byte) uintptr { return uintptr(uint32(r) | uint32(g)<<8 | uint32(b)<<16) }
func makeFont(sz, weight int, face string) uintptr {
	r, _, _ := pCreateFont.Call(uintptr(int32(-sz)), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(utf(face))))
	return r
}
func fill(hdc uintptr, x, y, w, h int32, r, g, b byte) {
	if w <= 0 || h <= 0 {
		return
	}
	br, _, _ := pCreateBrush.Call(rgb(r, g, b))
	rect := guiRect{x, y, x + w, y + h}
	pFillRect.Call(hdc, uintptr(unsafe.Pointer(&rect)), br)
	pDeleteObj.Call(br)
}
func textAt(hdc uintptr, s string, x, y, w, h int32, font uintptr, color uintptr, flags uint32) {
	pSelectObj.Call(hdc, font)
	pSetBkMode.Call(hdc, 1)
	pSetTextColor.Call(hdc, color)
	rect := guiRect{x, y, x + w, y + h}
	pDrawText.Call(hdc, uintptr(unsafe.Pointer(utf(s))), ^uintptr(0), uintptr(unsafe.Pointer(&rect)), uintptr(flags|dtSingleLine|dtVCenter|dtEndEllipsis))
}
func edge(hdc uintptr, x, y, w, h int32, r, g, b byte) {
	fill(hdc, x, y, w, 1, r, g, b)
	fill(hdc, x, y+h-1, w, 1, r, g, b)
	fill(hdc, x, y, 1, h, r, g, b)
	fill(hdc, x+w-1, y, 1, h, r, g, b)
}
func writeLine(hdc uintptr, s string, x, y, w int32, font uintptr, fg uintptr) {
	textAt(hdc, s, x, y, w, 33, font, fg, 0)
}
func drawCheckbox(hdc uintptr, x, y int32, checked bool, label string, enabled bool) {
	edge(hdc, x, y, 23, 23, 30, 157, 247)
	if checked {
		fill(hdc, x+4, y+4, 15, 15, 17, 114, 224)
		textAt(hdc, "✓", x+2, y-1, 21, 25, ui.fontBold, rgb(255, 255, 255), dtCenter)
	}
	c := rgb(240, 246, 255)
	if !enabled {
		c = rgb(180, 198, 219)
	}
	textAt(hdc, label, x+40, y-5, 665, 36, ui.fontNormal, c, 0)
}
func (g *installGUI) snapshot() (int, bool, bool, bool, int, string, []string, string, bool, bool, Inventory, [4]bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.page, g.running, g.finished, g.successful, g.progress, g.activity, append([]string{}, g.lines...), g.fault, g.selectedDiscord, g.selectedCompanion, g.state, g.selectedFloors
}
func (g *installGUI) repaint() {
	if g.hwnd != 0 {
		pPostMessage.Call(g.hwnd, wmAppUpdate, 0, 0)
	}
}
func (g *installGUI) note(message string, percentage int) {
	g.mu.Lock()
	if percentage >= 0 {
		g.activity = message
		g.progress = percentage
	} else {
		g.detail = message
	}
	if len(message) > 0 {
		if len(g.lines) >= 120 {
			g.lines = g.lines[1:]
		}
		g.lines = append(g.lines, message)
	}
	g.mu.Unlock()
	g.repaint()
}

func paintPage(hdc uintptr) {
	g := ui
	page, running, finished, successful, progress, activity, lines, fault, selDiscord, selCompanion, inv, choices := g.snapshot()
	// Firm header and native DDS icon embedded in the EXE.
	fill(hdc, 0, 0, guiW, 88, 249, 251, 255)
	mod, _, _ := pGetModule.Call(0)
	ic, _, _ := pLoadIcon.Call(mod, uintptr(1))
	if ic != 0 {
		pDrawIconEx.Call(hdc, 24, 8, ic, 72, 72, 0, 0, 3)
	}
	textAt(hdc, "DDS - Discord Data Snatcher", 110, 14, 730, 35, g.fontTitle, rgb(5, 43, 87), 0)
	textAt(hdc, "Мастер установки", 112, 51, 400, 22, g.fontSmall, rgb(80, 113, 153), 0)
	textAt(hdc, installerVersion, 635, 51, 230, 22, g.fontSmall, rgb(102, 126, 156), dtCenter)
	// Tab strip like accepted DDS visual reference.
	fill(hdc, 0, 88, guiW, 58, 4, 31, 61)
	steps := []string{"Настройка", "Установка", "Завершение"}
	for j, n := range steps {
		x := int32(j * 300)
		if j == page {
			fill(hdc, x, 88, 300, 58, 13, 81, 155)
			fill(hdc, x, 142, 300, 4, 27, 151, 252)
		}
		textAt(hdc, n, x+8, 94, 284, 44, g.fontNormal, rgb(236, 245, 255), dtCenter)
	}
	fill(hdc, 0, 146, guiW, 369, 4, 18, 37)
	if len(backgroundPixels) > 0 {
		bi := bitmapInfo{Header: bitmapHeader{Size: 40, Width: 900, Height: -390, Planes: 1, BitCount: 32}}
		pStretchDIBits.Call(hdc, 0, 146, guiW, 369, 0, 0, 900, 390, uintptr(unsafe.Pointer(&backgroundPixels[0])), uintptr(unsafe.Pointer(&bi)), 0, srccopy)
	}
	blue := rgb(94, 193, 255)
	white := rgb(244, 249, 255)
	muted := rgb(162, 188, 221)
	switch page {
	case 0:
		writeLine(hdc, "Подготовка установки", 64, 164, 750, g.fontBold, blue)
		writeLine(hdc, "DDS установит недостающие компоненты и восстановит выбранный BetterDiscord.", 64, 197, 775, g.fontSmall, white)
		writeLine(hdc, "Папка DDS Companion", 64, 235, 650, g.fontSmall, blue)
		states := []Component{inv.Discord, inv.BetterDiscord, inv.Plugin, inv.Companion}
		labels := []string{"Discord", "BetterDiscord", "DDS Plugin", "DDS Companion"}
		for i := range labels {
			y := int32(309 + i*34)
			// These ticks indicate mandatory floors, not optional destructive controls.
			drawCheckbox(hdc, 77, y, choices[i], labels[i], true)
			stateText := "Будет установлен"
			if !choices[i] {
				stateText = "Пропустить"
			}
			if choices[i] && states[i].State == StateOK {
				stateText = "Уже установлен — сохраняем"
				if i == 1 && choices[i] {
					stateText = "Будет восстановлено внедрение"
				}
				if i == 2 && choices[i] {
					stateText = "Проверим файл и включим DDS"
				}
			} else if states[i].State == StatePartial {
				stateText = "Требует внимания; нужен Repair"
				if i == 1 && choices[i] {
					stateText = "Будет восстановлено внедрение"
				}
			}
			textAt(hdc, stateText, 490, y-3, 345, 33, g.fontSmall, muted, 0)
		}
		g.mu.Lock()
		shortcut := g.createShortcut
		g.mu.Unlock()
		drawCheckbox(hdc, 77, 451, shortcut, "Создать ярлык на рабочем столе", choices[3])
		if e := planError(inv, choices); e != "" {
			writeLine(hdc, "Внимание: "+e, 65, 482, 810, g.fontSmall, rgb(255, 183, 91))
		} else {
			writeLine(hdc, "При установке Discord может закрыться. Сохрани черновики и выйди из звонка.", 65, 482, 810, g.fontSmall, muted)
		}
	case 1:
		writeLine(hdc, "Установка DDS", 64, 170, 680, g.fontBold, blue)
		writeLine(hdc, "Не закрывай установщик во время установки компонентов.", 64, 202, 775, g.fontSmall, white)
		fill(hdc, 65, 262, 769, 23, 3, 39, 67)
		fill(hdc, 68, 265, int32(float64(763)*float64(progress)/100), 17, 20, 138, 249)
		writeLine(hdc, activity, 65, 306, 780, g.fontNormal, white)
		g.mu.Lock()
		transfer, detail := g.download, g.detail
		g.mu.Unlock()
		if transfer.Bytes > 0 || transfer.Total > 0 {
			line := formatDownloadLine(transfer.Bytes, transfer.Total, 0, 0, transfer.Finished)
			writeLine(hdc, "Загрузка: "+strings.TrimSpace(line), 65, 355, 780, g.fontSmall, muted)
		}
		writeLine(hdc, detail, 65, 392, 780, g.fontSmall, muted)
		textAt(hdc, "Подробности…", 65, 434, 220, 32, g.fontSmall, blue, 0)
		if running {
			writeLine(hdc, "Подготовка и установка выполняются автоматически…", 65, 475, 775, g.fontSmall, blue)
		}
	case 2:
		states := []Component{inv.Discord, inv.BetterDiscord, inv.Plugin, inv.Companion}
		for i, c := range states {
			label := "Не подтверждён"
			if c.State == StateOK {
				label = "Файлы установлены"
			} else if c.State == StateMissing || c.State == StateNotDetected {
				label = "Не установлен"
			}
			writeLine(hdc, c.Name+": "+label, 65, int32(222+i*25), 770, g.fontSmall, muted)
		}
		if successful {
			writeLine(hdc, "Выбранные компоненты установлены", 64, 170, 760, g.fontBold, rgb(109, 230, 179))
			drawCheckbox(hdc, 84, 343, selDiscord, "Запустить Discord", inv.Discord.State == StateOK)
			drawCheckbox(hdc, 84, 389, selCompanion, "Запустить DDS Companion", inv.Companion.State == StateOK)
			writeLine(hdc, "Запуск — только после «Завершить». Работа плагина и архива ещё не проверена.", 65, 440, 790, g.fontSmall, muted)
			textAt(hdc, "Подробности…", 65, 477, 220, 30, g.fontSmall, blue, 0)
		} else {
			writeLine(hdc, "Установка не завершена", 64, 172, 760, g.fontBold, rgb(255, 175, 108))
			if len([]rune(fault)) > 90 {
				fault = string([]rune(fault)[:90]) + "…"
			}
			writeLine(hdc, fault, 64, 350, 790, g.fontSmall, rgb(255, 193, 132))
			textAt(hdc, "Подробности…", 65, 434, 220, 32, g.fontSmall, blue, 0)
		}
		_ = finished
	}
	_ = lines
	// Dark footer and clickable navigation actions.
	fill(hdc, 0, 515, guiW, 95, 2, 13, 30)
	fill(hdc, 0, 515, guiW, 2, 13, 147, 241)
	fill(hdc, 450, 530, 1, 80, 21, 55, 86)
	switch page {
	case 0:
		textAt(hdc, "Закрыть", 0, 530, 450, 63, g.fontBold, white, dtCenter)
		textAt(hdc, "Установить  ❯", 450, 530, 450, 63, g.fontBold, blue, dtCenter)
	case 1:
		textAt(hdc, "Выполняется установка…", 0, 530, 900, 63, g.fontBold, muted, dtCenter)
	case 2:
		textAt(hdc, "Закрыть", 0, 530, 450, 63, g.fontBold, white, dtCenter)
		textAt(hdc, "Завершить  ❯", 450, 530, 450, 63, g.fontBold, blue, dtCenter)
	}
}
func planError(inv Inventory, choices [4]bool) string {
	_, e := selectedSetupPlan(inv, choices)
	if e != nil {
		return e.Error()
	}
	return ""
}
func main() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if len(os.Args) > 1 && os.Args[1] == "--dds-admin-install" {
		runElevatedInstall(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--dds-postlaunch" {
		runPostLaunch(os.Args[2:])
		return
	}
	if userProcessElevationBlocked() {
		message("Запусти DDSSetup обычным двойным щелчком. Разрешение администратора будет запрошено только при размещении файлов DDS; Discord и архив должны работать от твоего пользователя.")
		return
	}
	pSetDPIAware.Call()
	ui.state = setupInventoryNow(os.Getenv("LOCALAPPDATA"), os.Getenv("APPDATA"), exeDir())
	ui.installRoot = preferredInstallRoot(ui.state)
	ui.fontSmall = makeFont(18, 400, "Segoe UI")
	ui.fontNormal = makeFont(22, 400, "Segoe UI")
	ui.fontBold = makeFont(22, 600, "Segoe UI")
	ui.fontTitle = makeFont(26, 600, "Segoe UI")
	inst, _, _ := pGetModule.Call(0)
	name := utf("DDSInstallerGUI")
	icon, _, _ := pLoadIcon.Call(inst, uintptr(1))
	cursor, _, _ := pLoadCursor.Call(0, 32512)
	class := windowClass{Size: uint32(unsafe.Sizeof(windowClass{})), Proc: syscall.NewCallback(windowProc), Instance: inst, Icon: icon, IconSmall: icon, Cursor: cursor, ClassName: name}
	if r, _, e := pRegisterClass.Call(uintptr(unsafe.Pointer(&class))); r == 0 {
		message("Невозможно открыть GUI: " + e.Error())
		return
	}
	style := uintptr(wsCaption | wsSysMenu | wsMinimizeBox)
	rc := guiRect{0, 0, guiW, guiH}
	pAdjustWindowRect.Call(uintptr(unsafe.Pointer(&rc)), style, 0)
	hwnd, _, err := pCreateWindow.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(utf("DDS - Discord Data Snatcher"))), style, 120, 90, uintptr(rc.Right-rc.Left), uintptr(rc.Bottom-rc.Top), 0, 0, inst, 0)
	if hwnd == 0 {
		message("Невозможно создать окно: " + err.Error())
		return
	}
	ui.hwnd = hwnd
	createLocationControls(hwnd, inst)
	pShowWindow.Call(hwnd, swShow)
	pUpdateWindow.Call(hwnd)
	var m guiMessage
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	if ui.quitWithFinish && ui.successful && (ui.selectedDiscord || ui.selectedCompanion) {
		if err := launchPostLaunchWorker(ui.selectedDiscord, ui.selectedCompanion, ui.installRoot); err != nil {
			message("Не удалось запустить выбранные приложения: " + err.Error())
		}
	}
	for _, f := range []uintptr{ui.fontSmall, ui.fontNormal, ui.fontBold, ui.fontTitle} {
		if f != 0 {
			pDeleteObj.Call(f)
		}
	}
}
func exeDir() string { exe, _ := os.Executable(); return filepath.Dir(exe) }
func message(s string) {
	pMessageBox.Call(0, uintptr(unsafe.Pointer(utf(s))), uintptr(unsafe.Pointer(utf("DDS Setup"))), 0x10)
}
func windowProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	switch msg {
	case 0x111: // WM_COMMAND, native path picker
		if wparam&0xffff == 1002 {
			chooseInstallFolder()
			return 0
		}
	case wmPaint:
		var ps guiPaint
		hdc, _, _ := pBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		paintPage(hdc)
		pEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		return 0
	case wmAppUpdate:
		pInvalidateRect.Call(hwnd, 0, 0)
		return 0
	case wmClose:
		ui.mu.Lock()
		busy := ui.running
		ui.mu.Unlock()
		if busy {
			return 0
		}
		pDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		pPostQuit.Call(0)
		return 0
	case wmKeyDown:
		if wparam == 0x1b {
			pPostMessage.Call(hwnd, wmClose, 0, 0)
			return 0
		}
		if wparam == 0x0d {
			handleClick(700, 560)
			return 0
		}
	case wmLButtonUp:
		x := int32(uint16(lparam & 0xffff))
		y := int32(uint16((lparam >> 16) & 0xffff))
		handleClick(x, y)
		return 0
	}
	res, _, _ := pDefWindowProc.Call(hwnd, uintptr(msg), wparam, lparam)
	return res
}
func handleClick(x, y int32) {
	ui.mu.Lock()
	page := ui.page
	busy := ui.running
	ok := ui.successful
	ui.mu.Unlock()
	if (page == 1 || page == 2) && x >= 60 && x <= 300 && ((y >= 430 && y <= 468 && (page == 1 || !ok)) || page == 2 && ok && y >= 475 && y <= 509) {
		ui.mu.Lock()
		details := strings.Join(ui.lines, "\r\n")
		if ui.fault != "" {
			details += "\r\n\r\n" + ui.fault
		}
		ui.mu.Unlock()
		pMessageBox.Call(ui.hwnd, uintptr(unsafe.Pointer(utf(details))), uintptr(unsafe.Pointer(utf("DDS — подробности установки"))), 0x40)
		return
	}
	if busy {
		return
	}
	if page == 0 && x >= 70 && x <= 650 {
		for i := 0; i < 4; i++ {
			y0 := int32(305 + i*34)
			if y >= y0 && y < y0+34 {
				ui.mu.Lock()
				ui.selectedFloors[i] = !ui.selectedFloors[i]
				ui.mu.Unlock()
				ui.repaint()
				return
			}
		}
		if y >= 447 && y <= 478 {
			ui.mu.Lock()
			if ui.selectedFloors[3] {
				ui.createShortcut = !ui.createShortcut
			}
			ui.mu.Unlock()
			ui.repaint()
			return
		}
	}
	if page == 2 && ok && x >= 77 && x <= 700 {
		if y >= 337 && y < 376 {
			ui.mu.Lock()
			if ui.state.Discord.State == StateOK {
				ui.selectedDiscord = !ui.selectedDiscord
			}
			ui.mu.Unlock()
			ui.repaint()
			return
		}
		if y >= 383 && y < 423 {
			ui.mu.Lock()
			if ui.state.Companion.State == StateOK {
				ui.selectedCompanion = !ui.selectedCompanion
			}
			ui.mu.Unlock()
			ui.repaint()
			return
		}
	}
	if y < 515 || y > 610 {
		return
	}
	if x < 450 {
		pPostMessage.Call(ui.hwnd, wmClose, 0, 0)
		return
	}
	switch page {
	case 0:
		buf := make([]uint16, 32768)
		pGetWindowText.Call(ui.pathEdit, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		target := strings.TrimSpace(syscall.UTF16ToString(buf))
		if ui.state.Companion.Path != "" && !strings.EqualFold(filepath.Clean(target), filepath.Clean(ui.state.Companion.Path)) {
			message("Перенос существующей установки требует отдельной миграции. Текущая папка сохранена.")
			return
		}
		if ui.selectedFloors[3] && ui.state.Companion.State != StateOK {
			if e := validateProgramTarget(target, filepath.Join(os.Getenv("LOCALAPPDATA"), "DDS_Companion")); e != nil {
				message(e.Error())
				return
			}
		}
		if e := planError(ui.state, ui.selectedFloors); e != "" {
			message("Установка заблокирована: " + e)
			return
		}
		ui.mu.Lock()
		ui.installRoot = target
		ui.page = 1
		ui.running = true
		ui.progress = 2
		ui.activity = "Проверка установленных компонентов"
		ui.lines = nil
		ui.mu.Unlock()
		pShowWindow.Call(ui.pathEdit, 0)
		pShowWindow.Call(ui.browseButton, 0)
		ui.repaint()
		go performGUIInstall()
	case 2:
		ui.quitWithFinish = true
		pDestroyWindow.Call(ui.hwnd)
	}
}
func performGUIInstall() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	ui.mu.Lock()
	target, shortcut, choices := ui.installRoot, ui.createShortcut, ui.selectedFloors
	ui.mu.Unlock()
	result, err := guiFirstSetup(ctx, os.Getenv("LOCALAPPDATA"), os.Getenv("APPDATA"), exeDir(), target, shortcut, ui.note, func(p downloadProgress) { ui.mu.Lock(); ui.download = p; ui.mu.Unlock(); ui.repaint() }, choices)
	fresh := setupInventoryAt(os.Getenv("LOCALAPPDATA"), os.Getenv("APPDATA"), exeDir(), target)
	ui.mu.Lock()
	ui.running = false
	ui.finished = true
	ui.successful = (err == nil)
	ui.state = fresh
	if err == nil {
		if ui.state.Discord.State != StateOK {
			ui.selectedDiscord = false
		}
		if ui.state.Companion.State != StateOK {
			ui.selectedCompanion = false
		}
	}
	ui.page = 2
	ui.progress = 100
	if err != nil {
		ui.fault = err.Error()
	} else {
		ui.activity = result.Status
	}
	ui.mu.Unlock()
	ui.repaint()
}

// Launch only AFTER the parent executable has exited. This helper is invoked only
// by Finish (not X/Cancel), and it never installs or mutates archive data itself.
func launchPostLaunchWorker(discord, companion bool, root string) error {
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	cmd := exec.Command(exe, "--dds-postlaunch", strconv.Itoa(os.Getpid()), strconv.FormatBool(discord), strconv.FormatBool(companion), root)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err == nil {
		_ = cmd.Process.Release()
		return nil
	} else {
		return err
	}
}
func runPostLaunch(args []string) {
	if len(args) != 4 || userProcessElevationBlocked() {
		return
	}
	pid, e := strconv.Atoi(args[0])
	if e != nil || pid <= 0 {
		return
	}
	d, e1 := strconv.ParseBool(args[1])
	c, e2 := strconv.ParseBool(args[2])
	if e1 != nil || e2 != nil || (!d && !c) {
		return
	}
	if pid == os.Getpid() {
		return
	}
	local, app := os.Getenv("LOCALAPPDATA"), os.Getenv("APPDATA")
	var started time.Time
	errs := finishHandoff(d, c, finishHooks{
		WaitParent: func() bool {
			if !waitInstallerExit(pid) {
				return false
			}
			started = time.Now()
			return true
		},
		DiscordRunning: discordClientIsRunning, CompanionRunning: companionClientIsRunning,
		StartDiscord:   func() error { return startInstalledDiscord(local, app, exeDir()) },
		StartCompanion: func() error { return startCompanionAt(args[3]) },
	})
	status := "Запуск после закрытия Installer не подтверждён"
	if !started.IsZero() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		version, versionErr := installedDDSPluginVersion(app)
		r := checkFinishRuntime(ctx, d, c, finishRuntimeHooks{
			DiscordRunning:   discordClientIsRunning,
			CompanionRunning: func() bool { return companionAtRunning(args[3]) },
			Injection: func() error {
				return verifyBetterDiscordInjection(detectDiscord(DetectContext{LocalAppData: local, AppData: app}), app)
			},
			Heartbeat: func() bool { return versionErr == nil && freshPluginHeartbeatSince(app, version, started, time.Now()) },
			Archive:   func() bool { return sqliteHeaderPresent(local) },
		})
		status = r.Text()
		errs = append(errs, r.Problems...)
	}
	if len(errs) > 0 {
		status += "\nПричина: " + strings.Join(errs, "; ")
	}
	path := filepath.Join(os.TempDir(), "DDS_Installer_PostLaunch_Status.txt")
	if err := os.WriteFile(path, []byte(time.Now().Format(time.RFC3339)+"\n"+status+"\n"), 0600); err != nil {
		errs = append(errs, "не удалось сохранить диагностику: "+err.Error())
	}
	if len(errs) > 0 {
		message("Файлы установлены, но работа выбранных приложений подтверждена не полностью.\n" + status + "\nДиагностика: " + path)
	}
}

func waitInstallerExit(pid int) bool {
	// SYNCHRONIZE process object is signaled only when parent has exited.
	op := kernel32.NewProc("OpenProcess")
	wait := kernel32.NewProc("WaitForSingleObject")
	closeH := kernel32.NewProc("CloseHandle")
	h, _, openErr := op.Call(0x00100000, 0, uintptr(pid))
	if h != 0 {
		r, _, _ := wait.Call(h, 60000)
		closeH.Call(h)
		if r != 0 {
			return false
		}
	} else if openErr != syscall.Errno(87) {
		// Access denied or unknown state: never launch before confirmed parent exit.
		return false
	}
	return true
}
