package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSelectedSetupAlwaysRepairsPresentBDAndChecksPresentPlugin(t *testing.T) {
	inv := guiInventory(StateOK, StateOK, StateOK, StateOK)
	var calls []string
	hooks := firstSetupHooks{
		Detect:               func() Inventory { return inv },
		InstallDiscord:       func(context.Context) error { t.Fatal("healthy Discord reinstalled"); return nil },
		InstallBetterDiscord: func(context.Context) error { calls = append(calls, "BD repair"); return nil },
		InstallPlugin:        func(Inventory) error { calls = append(calls, "DDS enable/check"); return nil },
		InstallCompanion:     func(Inventory) error { t.Fatal("healthy Companion reinstalled"); return nil },
	}
	staged := []stageOutcome{{Component: "betterdiscord", Result: "VERIFIED_CACHED"}}
	r := executeSelectedFirstSetup(context.Background(), inv, staged, hooks, [4]bool{true, true, true, true})
	if r.Status != "SELECTED_FILES_INTEGRATED_FIRST_START_PENDING" || !reflect.DeepEqual(calls, []string{"BD repair", "DDS enable/check"}) {
		t.Fatalf("%+v calls=%v", r, calls)
	}
	hooks.InstallBetterDiscord = func(context.Context) error { return errors.New("official CLI failure") }
	calls = nil
	r = executeSelectedFirstSetup(context.Background(), inv, staged, hooks, [4]bool{true, true, true, true})
	if r.Status != "PARTIAL_OR_BLOCKED" || len(calls) != 0 {
		t.Fatalf("failure continued: %+v %v", r, calls)
	}
}
func TestSelectedDDSCannotBypassRequiredBDRepair(t *testing.T) {
	inv := guiInventory(StateOK, StateOK, StateOK, StateOK)
	for _, choices := range [][4]bool{{false, false, true, false}, {false, false, false, true}} {
		if _, err := selectedSetupPlan(inv, choices); err == nil {
			t.Fatal("DDS setup without required BD repair accepted")
		}
	}
	// Opening inventory and planning never calls a mutating hook.
	if inv.BetterDiscord.State != StateOK {
		t.Fatal("inventory mutated")
	}
}
func TestSelectedPartialBDIsRepairableButPortableIsNotMigrated(t *testing.T) {
	inv := guiInventory(StateOK, StatePartial, StatePartial, StateOK)
	inv.Plugin.Version = "0.5.3"
	p, e := selectedSetupPlan(inv, [4]bool{true, true, true, true})
	if e != nil || !reflect.DeepEqual(p, []string{"betterdiscord", "plugin"}) {
		t.Fatalf("%v %v", p, e)
	}
	inv.Profile = "portable"
	if _, e = selectedSetupPlan(inv, [4]bool{true, true, true, true}); e == nil {
		t.Fatal("portable auto migration")
	}
}
func TestBDRepairStopsBeforeAndAfterCLIAndFailsClosed(t *testing.T) {
	for _, fail := range []string{"", "stop", "install", "verify"} {
		t.Run(fail, func(t *testing.T) {
			running := true
			calls := []string{}
			err := repairBetterDiscord(context.Background(), func() bool { return running }, func(context.Context) error {
				calls = append(calls, "stop")
				if fail == "stop" {
					return errors.New("blocked")
				}
				running = false
				return nil
			}, func() error {
				calls = append(calls, "install")
				if running {
					t.Fatal("install while running")
				}
				running = true
				if fail == "install" {
					return errors.New("CLI failed")
				}
				return nil
			}, func() error {
				calls = append(calls, "verify")
				if running {
					t.Fatal("verify before close")
				}
				if fail == "verify" {
					return errors.New("loader missing")
				}
				return nil
			})
			if (err != nil) != (fail != "") {
				t.Fatal(err)
			}
			expected := []string{"stop", "install", "stop", "verify"}
			if fail == "stop" {
				expected = []string{"stop"}
			}
			if fail == "install" {
				expected = expected[:3]
			}
			if !reflect.DeepEqual(calls, expected) {
				t.Fatalf("%v", calls)
			}
		})
	}
}
func TestBDInjectionFilesAreSeparateFromCoreAndRuntime(t *testing.T) {
	ctx := mkctx(t)
	installDiscord(t, ctx, "1.0.9999")
	installBD(t, ctx)
	discord := detectDiscord(ctx)
	if verifyBetterDiscordInjection(discord, ctx.AppData) == nil {
		t.Fatal("core file mistaken for injection")
	}
	resources := filepath.Join(filepath.Dir(discord.Path), "resources")
	touch(t, filepath.Join(resources, "betterdiscord.app.asar"), "original")
	touch(t, filepath.Join(resources, "app", "package.json"), `{"main":"./index.js"}`)
	touch(t, filepath.Join(resources, "app", "index.js"), `require("BetterDiscord/data/betterdiscord.asar");module.exports=require("../betterdiscord.app.asar");`)
	if err := verifyBetterDiscordInjection(discord, ctx.AppData); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(resources, "app.asar"), "new Discord shadows loader")
	if verifyBetterDiscordInjection(discord, ctx.AppData) == nil {
		t.Fatal("shadowed injection accepted")
	}
}
func TestExistingPluginIdentityAndOtherSettingsPreserved(t *testing.T) {
	ctx := mkctx(t)
	installBD(t, ctx)
	installPlugin(t, ctx, "0.5.3")
	if v, e := installedDDSPluginVersion(ctx.AppData); e != nil || v != "0.5.3" {
		t.Fatal(v, e)
	}
	p := filepath.Join(ctx.AppData, "BetterDiscord", "data", "stable", "plugins.json")
	touch(t, p, `{"DDS":false,"Other":true,"Disabled":false}`)
	theme := filepath.Join(ctx.AppData, "BetterDiscord", "themes", "my.theme.css")
	touch(t, theme, "theme")
	settings := filepath.Join(ctx.AppData, "BetterDiscord", "data", "stable", "settings.json")
	touch(t, settings, "settings")
	if err := enableNewDDSPlugin(ctx.AppData); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"Other": true`) || !strings.Contains(string(b), `"Disabled": false`) {
		t.Fatal(string(b))
	}
	for p, want := range map[string]string{theme: "theme", settings: "settings"} {
		b, _ := os.ReadFile(p)
		if string(b) != want {
			t.Fatal("other data changed")
		}
	}
	touch(t, filepath.Join(ctx.AppData, "BetterDiscord", "plugins", "DDS.plugin.js"), "/**\n * @name Other\n * @version 0.5.3\n */")
	if _, e := installedDDSPluginVersion(ctx.AppData); e == nil {
		t.Fatal("wrong plugin accepted")
	}
}
