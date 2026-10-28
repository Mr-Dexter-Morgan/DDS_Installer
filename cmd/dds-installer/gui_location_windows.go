//go:build windows

package main

import (
	"path/filepath"
	"syscall"
	"unsafe"
)

func createLocationControls(hwnd, instance uintptr) {
	style := uintptr(0x40000000 | 0x10000000 | 0x00010000 | 0x00800000 | 0x80) // CHILD,VISIBLE,TABSTOP,BORDER,AUTOHSCROLL
	if ui.state.Companion.Path != "" {
		style |= 0x800
	} // read-only existing install; no implicit migration
	ui.pathEdit, _, _ = pCreateWindow.Call(0, uintptr(unsafe.Pointer(utf("EDIT"))), uintptr(unsafe.Pointer(utf(ui.installRoot))), style, 64, 268, 660, 32, hwnd, 1001, instance, 0)
	ui.browseButton, _, _ = pCreateWindow.Call(0, uintptr(unsafe.Pointer(utf("BUTTON"))), uintptr(unsafe.Pointer(utf("Обзор…"))), 0x40000000|0x10000000|0x00010000, 734, 268, 100, 32, hwnd, 1002, instance, 0)
	send := user32.NewProc("SendMessageW")
	send.Call(ui.pathEdit, 0x30, ui.fontSmall, 1)
	send.Call(ui.browseButton, 0x30, ui.fontSmall, 1)
	if ui.state.Companion.Path != "" {
		pEnableWindow.Call(ui.browseButton, 0)
	}
}

type browseInfo struct {
	Owner, Root     uintptr
	Display         *uint16
	Title           *uint16
	Flags           uint32
	Callback, Param uintptr
	Image           int32
}

func chooseInstallFolder() {
	if ui.page != 0 || ui.running || ui.state.Companion.Path != "" {
		return
	}
	ole := syscall.NewLazyDLL("ole32.dll")
	hr, _, _ := ole.NewProc("CoInitializeEx").Call(0, 2)
	if int32(hr) >= 0 {
		defer ole.NewProc("CoUninitialize").Call()
	}
	name := make([]uint16, 260)
	info := browseInfo{Owner: ui.hwnd, Display: &name[0], Title: utf("Выбери родительскую папку; DDS будет установлен в её подпапку DDS"), Flags: 0x1 | 0x40}
	shell := syscall.NewLazyDLL("shell32.dll")
	pidl, _, _ := shell.NewProc("SHBrowseForFolderW").Call(uintptr(unsafe.Pointer(&info)))
	if pidl == 0 {
		return
	}
	defer ole.NewProc("CoTaskMemFree").Call(pidl)
	path := make([]uint16, 260)
	if r, _, _ := shell.NewProc("SHGetPathFromIDListW").Call(pidl, uintptr(unsafe.Pointer(&path[0]))); r == 0 {
		return
	}
	root := syscall.UTF16ToString(path)
	if filepath.Base(root) != "DDS" {
		root = filepath.Join(root, "DDS")
	}
	pSetWindowText.Call(ui.pathEdit, uintptr(unsafe.Pointer(utf(root))))
}
