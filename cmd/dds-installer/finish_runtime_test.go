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

func TestHeartbeatMustBelongToCurrentLaunch(t *testing.T) {
	app, _ := mockNewBD(t)
	p := filepath.Join(app, "BetterDiscord", "DDS_Data", "plugin_heartbeat.json")
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	start := now.Add(-5 * time.Second)
	for _, tc := range []struct {
		name, state, version string
		at                   time.Time
		want                 bool
	}{
		{"before launch but only 10 seconds old", "RUNNING", "0.5.3", now.Add(-10 * time.Second), false},
		{"current launch", "RUNNING", "0.5.3", now.Add(-time.Second), true},
		{"stopped", "STOPPED", "0.5.3", now, false},
		{"wrong version", "RUNNING", "0.5.2", now, false},
		{"future", "RUNNING", "0.5.3", now.Add(time.Second), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(map[string]string{"state": tc.state, "pluginVersion": tc.version, "updatedAt": tc.at.Format(time.RFC3339Nano)})
			if e := os.WriteFile(p, b, 0600); e != nil {
				t.Fatal(e)
			}
			if got := freshPluginHeartbeatSince(app, "0.5.3", start, now); got != tc.want {
				t.Fatalf("%v", got)
			}
		})
	}
}
func goodFinishHooks() finishRuntimeHooks {
	return finishRuntimeHooks{DiscordRunning: func() bool { return true }, CompanionRunning: func() bool { return true }, Injection: func() error { return nil }, Heartbeat: func() bool { return true }, Archive: func() bool { return true }}
}
func TestFinishRuntimeChoiceIndependenceAndNoUncheckedLaunchError(t *testing.T) {
	h := goodFinishHooks()
	h.DiscordRunning = func() bool { t.Fatal("unchecked Discord probed"); return false }
	h.Injection = func() error { t.Fatal("unchecked BD"); return nil }
	h.Heartbeat = func() bool { t.Fatal("unchecked heartbeat"); return false }
	r := checkFinishRuntime(context.Background(), false, true, h)
	if len(r.Problems) != 0 || !strings.Contains(r.Plugin, "НЕ ПРОВЕРЯЛОСЬ") || !strings.HasPrefix(r.Companion, "ЗАПУЩЕНО") {
		t.Fatalf("%+v", r)
	}
	r = checkFinishRuntime(context.Background(), false, false, finishRuntimeHooks{})
	if len(r.Problems) != 0 {
		t.Fatal(r)
	}
}
func TestFinishRuntimeNeverAcceptsProcessOrStaticFilesAsPluginHealth(t *testing.T) {
	h := goodFinishHooks()
	h.Heartbeat = func() bool { return false }
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	r := checkFinishRuntime(ctx, true, false, h)
	if len(r.Problems) == 0 || strings.Contains(r.Plugin, "РАБОТАЕТ") {
		t.Fatal(r)
	}
	h = goodFinishHooks()
	h.Injection = func() error { return errors.New("loader absent") }
	r = checkFinishRuntime(context.Background(), true, false, h)
	if len(r.Problems) == 0 || strings.Contains(r.Plugin, "РАБОТАЕТ") {
		t.Fatal(r)
	}
	r = checkFinishRuntime(context.Background(), true, true, goodFinishHooks())
	if len(r.Problems) != 0 || !strings.Contains(r.Plugin, "РАБОТАЕТ") || !strings.Contains(r.BetterDiscord, "РАБОТАЕТ") {
		t.Fatal(r)
	}
	if strings.Contains(r.Text(), "RUNTIME_READY") {
		t.Fatal("unproven Companion consumption promoted")
	}
}
