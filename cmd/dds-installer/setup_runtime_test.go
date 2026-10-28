package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mockNewBD(t *testing.T) (string, string) {
	t.Helper()
	app := filepath.Join(t.TempDir(), "Roaming")
	bd := filepath.Join(app, "BetterDiscord")
	for _, d := range []string{filepath.Join(bd, "data", "stable"), filepath.Join(bd, "plugins")} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bd, "plugins", "DDS.plugin.js"), []byte("DDS"), 0600); err != nil {
		t.Fatal(err)
	}
	return app, filepath.Join(bd, "data", "stable", "plugins.json")
}
func TestDDSPluginEnableKeepsOtherPlugins(t *testing.T) {
	app, file := mockNewBD(t)
	before := []byte(`{"OtherPlugin":false,"SecondPlugin":true,"nested":{"value":2}}`)
	if e := os.WriteFile(file, before, 0600); e != nil {
		t.Fatal(e)
	}
	if enabled,err:=ddsPluginEnabled(app);err!=nil||enabled{t.Fatalf("unexpected DDS state: %v %v",enabled,err)}
	if e := enableNewDDSPlugin(app); e != nil {
		t.Fatal(e)
	}
	if enabled,err:=ddsPluginEnabled(app);err!=nil||!enabled{t.Fatalf("DDS activation not detected: %v %v",enabled,err)}
	b, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	var obj map[string]interface{}
	if json.Unmarshal(b, &obj) != nil || obj["DDS"] != true || obj["OtherPlugin"] != false || obj["SecondPlugin"] != true {
		t.Fatalf("corrupt settings: %s", b)
	}
	if e = enableNewDDSPlugin(app); e != nil {
		t.Fatal(e)
	}
	b2, _ := os.ReadFile(file)
	if string(b) != string(b2) {
		t.Fatal("no-op rewrote settings")
	}
}
func TestDDSPluginEnableWithoutPreexistingSettings(t *testing.T) {
	app, file := mockNewBD(t)
	if e := enableNewDDSPlugin(app); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(file)
	if !strings.Contains(string(b), `"DDS": true`) {
		t.Fatalf("not enabled: %s", b)
	}
}
func TestDDSPluginEnableOnlyOverridesDDS(t *testing.T) {
	app, file := mockNewBD(t)
	old := []byte(`{"DDS":false,"OtherPlugin":true}`)
	if e := os.WriteFile(file, old, 0600); e != nil {
		t.Fatal(e)
	}
	if e := enableNewDDSPlugin(app); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(file)
	var settings map[string]bool
	if json.Unmarshal(after, &settings) != nil || settings["DDS"] != true || settings["OtherPlugin"] != true {
		t.Fatalf("other plugin changed: %s", after)
	}
}
func TestDDSPluginEnableRejectsBrokenSettings(t *testing.T) {
	app, file := mockNewBD(t)
	bad := []byte(`[]`)
	if e := os.WriteFile(file, bad, 0600); e != nil {
		t.Fatal(e)
	}
	if e := enableNewDDSPlugin(app); e == nil {
		t.Fatal("accepted non-object JSON")
	}
	after, _ := os.ReadFile(file)
	if string(after) != string(bad) {
		t.Fatal("mutated invalid file")
	}
}
func TestFirstStartChecksAllSteps(t *testing.T) {
	d, c, h, a := false, false, false, false
	var calls []string
	hook := startupHooks{
		StartDiscord:   func() error { calls = append(calls, "discord"); d = true; return nil },
		StartCompanion: func() error { calls = append(calls, "companion"); c = true; return nil },
		DiscordRunning: func() bool { return d }, CompanionRunning: func() bool { return c },
		PluginHeartbeat: func() bool { return h }, ArchiveReady: func() bool { return a },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	err := runSetupFirstStart(ctx, os.Stdout, hook)
	if err == nil || strings.Join(calls, ",") != "discord,companion" {
		t.Fatalf("failed gate %+v %v", calls, err)
	}
	h = true
	a = true
	err = runSetupFirstStart(context.Background(), os.Stdout, hook)
	if err != nil {
		t.Fatal(err)
	}
}
func TestFirstStartNeverStartsCompanionAfterDiscordFailure(t *testing.T) {
	calls := 0
	h := startupHooks{StartDiscord: func() error { return errors.New("failed") }, StartCompanion: func() error { calls++; return nil }, DiscordRunning: func() bool { return false }, CompanionRunning: func() bool { return false }, PluginHeartbeat: func() bool { return false }, ArchiveReady: func() bool { return false }}
	if e := runSetupFirstStart(context.Background(), os.Stdout, h); e == nil || calls != 0 {
		t.Fatal("unexpected launch")
	}
}
func TestFirstStartFreshHeartbeat(t *testing.T) {
	app, _ := mockNewBD(t)
	p := filepath.Join(app, "BetterDiscord", "DDS_Data", "plugin_heartbeat.json")
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	write := func(state string, at time.Time) {
		t.Helper()
		b, _ := json.Marshal(map[string]interface{}{"state": state, "pluginVersion": "0.5.3", "updatedAt": at.UTC().Format(time.RFC3339Nano)})
		if e := os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	write("RUNNING", time.Now())
	if !freshPluginHeartbeat(app) {
		t.Fatal("fresh heartbeat rejected")
	}
	write("RUNNING", time.Now().Add(-20*time.Minute))
	if freshPluginHeartbeat(app) {
		t.Fatal("stale heartbeat accepted")
	}
	write("STOPPED", time.Now())
	if freshPluginHeartbeat(app) {
		t.Fatal("STOPPED accepted")
	}
}
func TestFirstStartArchiveHeader(t *testing.T) {
	local := t.TempDir()
	p := filepath.Join(local, "DDS_Companion", "database", "dds.sqlite3")
	if sqliteHeaderPresent(local) {
		t.Fatal("missing DB accepted")
	}
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte("not sqlite"), 0600); e != nil {
		t.Fatal(e)
	}
	if sqliteHeaderPresent(local) {
		t.Fatal("fake DB accepted")
	}
	if e := os.WriteFile(p, []byte("SQLite format 3\x00test"), 0600); e != nil {
		t.Fatal(e)
	}
	if !sqliteHeaderPresent(local) {
		t.Fatal("SQLite header not accepted")
	}
}
