//go:build windows

package main

// In-process Win32 rendering only. No production install, UI input, process
// control, registry changes or user archive access is performed by this test.
import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"unsafe"
)

func TestNativeWizardRender(t *testing.T) {
	destination := os.Getenv("DDS_RENDER_OUTPUT")
	if destination == "" {
		t.Skip("render requested separately")
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		t.Fatal(err)
	}
	ui.fontSmall = makeFont(18, 400, "Segoe UI")
	ui.fontNormal = makeFont(22, 400, "Segoe UI")
	ui.fontBold = makeFont(22, 600, "Segoe UI")
	ui.fontTitle = makeFont(26, 600, "Segoe UI")
	defer func() {
		for _, f := range []uintptr{ui.fontSmall, ui.fontNormal, ui.fontBold, ui.fontTitle} {
			pDeleteObj.Call(f)
		}
	}()
	ui.state = guiInventory(StateOK, StateOK, StateOK, StateOK)
	ui.selectedFloors = [4]bool{true, true, true, true}
	ui.createShortcut = true
	ui.progress = 65
	ui.activity = "Устанавливаем BetterDiscord"
	ui.detail = "Подлинность и целостность пакетов проверены"
	dc, _, _ := gdi32.NewProc("CreateCompatibleDC").Call(0)
	if dc == 0 {
		t.Fatal("CreateCompatibleDC")
	}
	defer gdi32.NewProc("DeleteDC").Call(dc)
	info := bitmapInfo{Header: bitmapHeader{Size: 40, Width: guiW, Height: -guiH, Planes: 1, BitCount: 32}}
	var bits unsafe.Pointer
	bitmap, _, _ := gdi32.NewProc("CreateDIBSection").Call(dc, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 {
		t.Fatal("CreateDIBSection")
	}
	defer pDeleteObj.Call(bitmap)
	old, _, _ := pSelectObj.Call(dc, bitmap)
	defer pSelectObj.Call(dc, old)
	for page := 0; page < 3; page++ {
		ui.page = page
		ui.successful = page == 2
		paintPage(dc)
		if page == 0 {
			fill(dc, 64, 268, 660, 32, 1, 12, 30)
			edge(dc, 64, 268, 660, 32, 101, 149, 194)
			textAt(dc, defaultProgramRoot(), 74, 268, 640, 32, ui.fontSmall, rgb(244, 249, 255), 0)
			textAt(dc, "Обзор…", 734, 268, 100, 32, ui.fontSmall, rgb(94, 193, 255), dtCenter)
		}
		gdi32.NewProc("GdiFlush").Call()
		raw := unsafe.Slice((*byte)(bits), guiW*guiH*4)
		img := image.NewRGBA(image.Rect(0, 0, guiW, guiH))
		for y := 0; y < guiH; y++ {
			for x := 0; x < guiW; x++ {
				i := (y*guiW + x) * 4
				img.SetRGBA(x, y, color.RGBA{raw[i+2], raw[i+1], raw[i], 255})
			}
		}
		f, err := os.Create(filepath.Join(destination, []string{"setup.png", "install.png", "finish.png"}[page]))
		if err != nil {
			t.Fatal(err)
		}
		if err = png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}
