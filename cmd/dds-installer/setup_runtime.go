package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// First-install only: keeps other BetterDiscord addon choices untouched.
// BetterDiscord's AddonManager reads plugins.json into state keyed by @name.
func ddsPluginEnabled(appData string) (bool, error) {
	path := filepath.Join(appData, "BetterDiscord", "data", "stable", "plugins.json")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return false, nil
	}
	if err := requireNormalFile(path); err != nil {
		return false, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var state map[string]json.RawMessage
	if json.Unmarshal(b, &state) != nil || state == nil {
		return false, errors.New("Не удалось прочитать состояние плагинов BetterDiscord")
	}
	raw, exists := state["DDS"]
	if !exists {
		return false, nil
	}
	var active bool
	if json.Unmarshal(raw, &active) != nil {
		return false, errors.New("Неверное состояние DDS в BetterDiscord")
	}
	return active, nil
}

func enableNewDDSPlugin(appData string) error {
	if !filepath.IsAbs(appData) || requireNormalDir(appData) != nil {
		return errors.New("APPDATA недоступна")
	}
	bd := filepath.Join(appData, "BetterDiscord")
	if requireNormalDir(bd) != nil || requireNormalFile(filepath.Join(bd, "plugins", "DDS.plugin.js")) != nil {
		return errors.New("DDS Plugin не найден в BetterDiscord")
	}
	stable := filepath.Join(bd, "data", "stable")
	if err := requireNormalDir(filepath.Join(bd, "data")); err != nil {
		return fmt.Errorf("BetterDiscord data: %w", err)
	}
	if err := os.MkdirAll(stable, 0700); err != nil {
		return err
	}
	if err := requireNormalDir(stable); err != nil {
		return err
	}
	path := filepath.Join(stable, "plugins.json")
	state := map[string]json.RawMessage{}
	if _, err := os.Lstat(path); err == nil {
		if err := requireNormalFile(path); err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &state); err != nil {
			return fmt.Errorf("неверный plugins.json: %w", err)
		}
		if state == nil {
			return errors.New("plugins.json должен содержать JSON-объект")
		}
		if v, ok := state["DDS"]; ok {
			var enabled bool
			if err := json.Unmarshal(v, &enabled); err != nil {
				return fmt.Errorf("DDS настройка не является bool: %w", err)
			}
			if enabled {
				return nil
			}
			// In first Setup the user approved enabling DDS; all other plugin choices remain unchanged.
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	state["DDS"] = json.RawMessage("true")
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	tmp, err := os.CreateTemp(stable, ".dds-plugins-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(content)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// A safe first-install path must never replace a symlink or user data file.
	if err := atomicReplaceFile(tmpPath, path); err != nil {
		return err
	}
	return nil
}

func ensureDiscordStopped(ctx context.Context, running func() bool, stop func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !running() {
		return nil
	}
	if err := stop(ctx); err != nil {
		return fmt.Errorf("не удалось закрыть Discord: %w", err)
	}
	return awaitSetupComponent(ctx, 20*time.Second, func() bool { return !running() })
}

type startupHooks struct {
	StartDiscord     func() error
	StartCompanion   func() error
	DiscordRunning   func() bool
	CompanionRunning func() bool
	PluginHeartbeat  func() bool
	ArchiveReady     func() bool
}

func runSetupFirstStart(ctx context.Context, out io.Writer, h startupHooks) error {
	if h.StartDiscord == nil || h.StartCompanion == nil || h.DiscordRunning == nil || h.CompanionRunning == nil || h.PluginHeartbeat == nil || h.ArchiveReady == nil {
		return errors.New("неполная конфигурация запуска")
	}
	fmt.Fprintln(out, "Первый запуск: Discord → DDS Companion → проверка DDS Plugin")
	if !h.DiscordRunning() {
		if err := h.StartDiscord(); err != nil {
			return fmt.Errorf("Discord не запущен: %w", err)
		}
	}
	if err := awaitSetupComponent(ctx, 40*time.Second, h.DiscordRunning); err != nil {
		return fmt.Errorf("Discord: %w", err)
	}
	if !h.CompanionRunning() {
		if err := h.StartCompanion(); err != nil {
			return fmt.Errorf("DDS Companion не запущен: %w", err)
		}
	}
	if err := awaitSetupComponent(ctx, 30*time.Second, h.CompanionRunning); err != nil {
		return fmt.Errorf("DDS Companion: %w", err)
	}
	if err := awaitSetupComponent(ctx, 90*time.Second, h.PluginHeartbeat); err != nil {
		return fmt.Errorf("DDS Plugin heartbeat не появился: %w", err)
	}
	if err := awaitSetupComponent(ctx, 20*time.Second, h.ArchiveReady); err != nil {
		return fmt.Errorf("SQLite архив не подтверждён: %w", err)
	}
	return nil
}

func freshPluginHeartbeat(appData string) bool {
	return freshPluginHeartbeatSince(appData, "0.5.3", time.Time{}, time.Now())
}

func freshPluginHeartbeatSince(appData, version string, since, now time.Time) bool {
	path := filepath.Join(appData, "BetterDiscord", "DDS_Data", "plugin_heartbeat.json")
	if requireNormalFile(path) != nil {
		return false
	}
	st, err := os.Stat(path)
	if err != nil || st.Size() > 64<<10 {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var h struct {
		State         string `json:"state"`
		UpdatedAt     string `json:"updatedAt"`
		PluginVersion string `json:"pluginVersion"`
	}
	if json.Unmarshal(b, &h) != nil || h.State != "RUNNING" || version == "" || h.PluginVersion != version {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, h.UpdatedAt)
	if err != nil || at.Before(since) {
		return false
	}
	age := now.Sub(at)
	return age >= 0 && age <= 75*time.Second
}

func sqliteHeaderPresent(localAppData string) bool {
	path := filepath.Join(localAppData, "DDS_Companion", "database", "dds.sqlite3")
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 16)
	_, err = io.ReadFull(f, b)
	return err == nil && strings.HasPrefix(string(b), "SQLite format 3\x00")
}
