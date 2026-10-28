package main

import (
	"fmt"
	"io"
)

func runPluginApplyMenu(inv Inventory, input io.Reader, out io.Writer, stageRoot, appData string) {
	rec, _, target, needed, e := preparePluginApply(inv, stageRoot, appData)
	if e != nil {
		fmt.Fprintln(out, "НЕВОЗМОЖНО:", e)
		return
	}
	if !needed {
		fmt.Fprintf(out, "DDS Plugin %s уже соответствует проверенному staging. Файлы не изменялись.\n", rec.Version)
		return
	}
	if discordClientIsRunning() {
		fmt.Fprintln(out, "ОТМЕНА: Discord запущен. Закрой Discord перед изменением DDS Plugin.")
		return
	}
	fmt.Fprintf(out, "Только DDS Plugin %s -> %s\n", rec.Version, target)
	fmt.Fprintln(out, "Будет создана резервная копия прежнего DDS.plugin.js (если существует).")
	fmt.Fprintln(out, "Discord / BetterDiscord / Companion / DDS_Data / SQLite / DataRoot не изменяются.")
	if !confirmNumeric(input, out, "Установить проверенный DDS Plugin?") {
		fmt.Fprintln(out, "Отменено без изменений.")
		return
	}
	if discordClientIsRunning() {
		fmt.Fprintln(out, "ОТМЕНА: Discord запущен.")
		return
	}
	result, e := applyPlugin(inv, stageRoot, appData)
	if e != nil {
		fmt.Fprintln(out, "НЕ УДАЛОСЬ:", e)
		return
	}
	fmt.Fprintf(out, "%s: DDS Plugin %s\nПуть: %s\n", result.Status, result.Version, result.Target)
	if result.Backup != "" {
		fmt.Fprintln(out, "Резервная копия:", result.Backup)
	}
}
