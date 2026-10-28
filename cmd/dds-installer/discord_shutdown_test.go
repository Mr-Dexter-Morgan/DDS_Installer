package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDiscordShutdownAlreadyStopped(t *testing.T) {
	calls := 0
	a := discordStopActions{probe: func(context.Context) (bool, error) { return false, nil }, graceful: func(context.Context) error { calls++; return nil }, force: func(context.Context) (string, error) { calls++; return "", nil }, sleep: func(context.Context, time.Duration) error { calls++; return nil }}
	if err := shutdownDiscordWith(context.Background(), a); err != nil || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}
func TestDiscordShutdownGracefulSuccess(t *testing.T) {
	n, force := 0, 0
	a := discordStopActions{probe: func(context.Context) (bool, error) { n++; return n < 3, nil }, graceful: func(context.Context) error { return nil }, force: func(context.Context) (string, error) { force++; return "", nil }, sleep: func(context.Context, time.Duration) error { return nil }}
	if err := shutdownDiscordWith(context.Background(), a); err != nil || force != 0 {
		t.Fatalf("err=%v force=%d", err, force)
	}
}
func TestDiscordShutdownForcedSuccessEvenOnTaskkillRace(t *testing.T) {
	n := 0
	a := discordStopActions{probe: func(context.Context) (bool, error) { n++; return n <= 12, nil }, graceful: func(context.Context) error { return errors.New("no window") }, force: func(context.Context) (string, error) { return "not found", errors.New("exit 128") }, sleep: func(context.Context, time.Duration) error { return nil }}
	if err := shutdownDiscordWith(context.Background(), a); err != nil {
		t.Fatal(err)
	}
}
func TestDiscordShutdownForcedFailureKeepsDiagnostics(t *testing.T) {
	a := discordStopActions{probe: func(context.Context) (bool, error) { return true, nil }, graceful: func(context.Context) error { return errors.New("grace denied") }, force: func(context.Context) (string, error) { return "Access is denied", errors.New("exit status 5") }, sleep: func(context.Context, time.Duration) error { return nil }}
	err := shutdownDiscordWith(context.Background(), a)
	for _, s := range []string{"всё ещё запущен", "Access is denied", "exit status 5", "grace denied"} {
		if err == nil || !strings.Contains(err.Error(), s) {
			t.Fatalf("missing %s: %v", s, err)
		}
	}
}
func TestDiscordShutdownProbeErrorNotMisreported(t *testing.T) {
	a := discordStopActions{probe: func(context.Context) (bool, error) { return false, errors.New("tasklist unavailable") }, graceful: func(context.Context) error { return nil }, force: func(context.Context) (string, error) { return "", nil }, sleep: func(context.Context, time.Duration) error { return nil }}
	err := shutdownDiscordWith(context.Background(), a)
	if err == nil || !strings.Contains(err.Error(), "tasklist unavailable") || strings.Contains(err.Error(), "всё ещё запущен") {
		t.Fatal(err)
	}
}
func TestDiscordShutdownCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	a := discordStopActions{probe: func(context.Context) (bool, error) { return true, nil }, graceful: func(context.Context) error { return nil }, force: func(context.Context) (string, error) { return "", nil }, sleep: func(context.Context, time.Duration) error { n++; cancel(); return ctx.Err() }}
	err := shutdownDiscordWith(ctx, a)
	if err == nil || !errors.Is(err, context.Canceled) || n != 1 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}
