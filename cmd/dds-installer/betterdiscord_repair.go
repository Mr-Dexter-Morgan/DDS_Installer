package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A non-destructive re-install of the official injection. Do not call upstream
// RepairBD: that separate operation disables addons and removes the injection.
func repairBetterDiscord(ctx context.Context, running func() bool, stop func(context.Context) error, install func() error, verify func() error) error {
	if err := ensureDiscordStopped(ctx, running, stop); err != nil {
		return err
	}
	installErr := install()
	// A CLI failure can still have started Discord; close it before returning.
	stopErr := ensureDiscordStopped(ctx, running, stop)
	if err := errors.Join(installErr, stopErr); err != nil {
		return err
	}
	return verify()
}

// Static verification of the official CLI v1 injection in this exact Discord
// Stable installation. This proves loader placement, never that BD executed.
// Source: BetterDiscord/cli v1.0.0 internal/discord/{injection.go,assets/app_*}.
func verifyBetterDiscordInjection(discord Component, appData string) error {
	if discord.State != StateOK || !strings.EqualFold(filepath.Base(discord.Path), "Discord.exe") {
		return errors.New("Discord Stable не подтверждён для проверки внедрения")
	}
	resources := filepath.Join(filepath.Dir(discord.Path), "resources")
	for _, p := range []string{resources, filepath.Join(resources, "app")} {
		if err := requireNormalDir(p); err != nil {
			return fmt.Errorf("BetterDiscord loader: %w", err)
		}
	}
	// Electron prefers a live app.asar over app/: an old BD core alone is insufficient.
	if _, err := os.Lstat(filepath.Join(resources, "app.asar")); !os.IsNotExist(err) {
		return errors.New("Внедрение BetterDiscord не подтверждено: app.asar затеняет loader или недоступен")
	}
	for _, p := range []string{filepath.Join(resources, "betterdiscord.app.asar"), filepath.Join(appData, "BetterDiscord", "data", "betterdiscord.asar")} {
		if err := requireNormalFile(p); err != nil {
			return fmt.Errorf("BetterDiscord: %w", err)
		}
		st, err := os.Stat(p)
		if err != nil {
			return err
		}
		if st.Size() == 0 {
			return errors.New("BetterDiscord: пустой ASAR")
		}
	}
	read := func(p string) ([]byte, error) {
		if err := requireNormalFile(p); err != nil {
			return nil, err
		}
		st, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if st.Size() > 64<<10 {
			return nil, errors.New("неожиданный размер loader BetterDiscord")
		}
		return os.ReadFile(p)
	}
	b, err := read(filepath.Join(resources, "app", "package.json"))
	if err != nil {
		return err
	}
	var pkg struct {
		Main string `json:"main"`
	}
	if json.Unmarshal(b, &pkg) != nil || (pkg.Main != "./index.js" && pkg.Main != "index.js") {
		return errors.New("BetterDiscord: неверный entrypoint loader")
	}
	b, err = read(filepath.Join(resources, "app", "index.js"))
	if err != nil {
		return err
	}
	for _, part := range []string{"require(", "BetterDiscord", "betterdiscord.asar", "../betterdiscord.app.asar"} {
		if !strings.Contains(string(b), part) {
			return errors.New("BetterDiscord: loader не соответствует официальному механизму CLI v1")
		}
	}
	return nil
}

var pluginNameRE = regexp.MustCompile(`(?m)^\s*\*\s*@name\s+DDS\s*$`)

func installedDDSPluginVersion(appData string) (string, error) {
	root := filepath.Join(appData, "BetterDiscord")
	for _, p := range []string{appData, root, filepath.Join(root, "plugins")} {
		if err := requireNormalDir(p); err != nil {
			return "", err
		}
	}
	p := filepath.Join(root, "plugins", "DDS.plugin.js")
	if err := requireNormalFile(p); err != nil {
		return "", err
	}
	st, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if st.Size() > externalMaxSize {
		return "", errors.New("DDS Plugin превышает предел размера")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	v := pluginHeaderVersionRE.FindSubmatch(b)
	if !pluginNameRE.Match(b) || len(v) != 2 || !finalVersionRE.MatchString(string(v[1])) {
		return "", errors.New("DDS.plugin.js: не подтверждены имя DDS и версия")
	}
	return string(v[1]), nil
}
