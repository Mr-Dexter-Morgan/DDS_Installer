package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Discord shutdown is a blocking safety gate for BetterDiscord injection.
// Preserve the exact failure stage: process-probe errors must never be
// reported as proof that Discord remained running.
type discordStopActions struct {
	probe    func(context.Context) (bool, error)
	graceful func(context.Context) error
	force    func(context.Context) (string, error)
	sleep    func(context.Context, time.Duration) error
}

func shutdownWait(ctx context.Context, a discordStopActions, attempts int, interval time.Duration) (bool, error) {
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return false, fmt.Errorf("ожидание завершения Discord прервано: %w", err)
		}
		running, err := a.probe(ctx)
		if err != nil {
			return false, fmt.Errorf("проверка процессов Discord: %w", err)
		}
		if !running {
			return true, nil
		}
		if i < attempts-1 {
			if err := a.sleep(ctx, interval); err != nil {
				return false, fmt.Errorf("ожидание завершения Discord: %w", err)
			}
		}
	}
	return false, nil
}

func shutdownDiscordWith(ctx context.Context, a discordStopActions) error {
	if a.probe == nil || a.graceful == nil || a.force == nil || a.sleep == nil {
		return fmt.Errorf("неполные обработчики завершения Discord")
	}
	exited, err := shutdownWait(ctx, a, 1, 0)
	if err != nil {
		return err
	}
	if exited {
		return nil
	}
	// A failed graceful close is not fatal when forced close succeeds.
	graceErr := a.graceful(ctx)
	exited, err = shutdownWait(ctx, a, 11, 500*time.Millisecond) // up to 5 seconds
	if err != nil {
		return err
	}
	if exited {
		return nil
	}
	killOut, killErr := a.force(ctx)
	exited, err = shutdownWait(ctx, a, 17, 500*time.Millisecond) // up to 8 seconds after taskkill
	if err != nil {
		return err
	}
	if exited {
		return nil
	} // process may disappear even when taskkill reported a race/error
	parts := []string{"Discord.exe всё ещё запущен после принудительного завершения"}
	if killErr != nil {
		parts = append(parts, "taskkill: "+killErr.Error())
	}
	if s := strings.TrimSpace(killOut); s != "" {
		parts = append(parts, "вывод taskkill: "+s)
	}
	if graceErr != nil {
		parts = append(parts, "штатное закрытие: "+graceErr.Error())
	}
	return fmt.Errorf("%s", strings.Join(parts, "; "))
}

func discordShutdownSleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
