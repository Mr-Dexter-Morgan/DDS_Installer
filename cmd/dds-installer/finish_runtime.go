package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type finishRuntimeHooks struct {
	DiscordRunning, CompanionRunning, Heartbeat, Archive func() bool
	Injection                                            func() error
}
type finishRuntimeReport struct {
	Discord, BetterDiscord, Plugin, Companion, Archive string
	Problems                                           []string
}

func (r finishRuntimeReport) Text() string {
	return strings.Join([]string{"Результат проверки после закрытия мастера:", "Discord: " + r.Discord, "BetterDiscord: " + r.BetterDiscord, "DDS Plugin: " + r.Plugin, "DDS Companion: " + r.Companion, "SQLite: " + r.Archive, "Отображение Plugin в статусе Companion проверяется в открытом приложении; полный LIVE PASS ещё не установлен."}, "\n")
}

// Processes, static injection, fresh plugin execution and archive presence are
// separate evidence. No daemon, no repair, no app launches inside this check.
func checkFinishRuntime(ctx context.Context, discord, companion bool, h finishRuntimeHooks) finishRuntimeReport {
	const skipped = "НЕ ПРОВЕРЯЛОСЬ — запуск не выбран"
	r := finishRuntimeReport{Discord: skipped, BetterDiscord: skipped, Plugin: skipped, Companion: skipped, Archive: skipped}
	if discord {
		r.Discord = "ЗАПУСК НЕ ПОДТВЕРЖДЁН"
		r.BetterDiscord = "РАБОТА НЕ ПОДТВЕРЖДЕНА"
		r.Plugin = "РАБОТА НЕ ПОДТВЕРЖДЕНА"
		if err := awaitSetupComponent(ctx, 40*time.Second, h.DiscordRunning); err != nil {
			r.Problems = append(r.Problems, "Discord: "+err.Error())
		} else {
			r.Discord = "ЗАПУЩЕНО"
			if err := h.Injection(); err != nil {
				r.Problems = append(r.Problems, err.Error())
			} else if err := awaitSetupComponent(ctx, 90*time.Second, h.Heartbeat); err != nil {
				r.BetterDiscord = "ВНЕДРЕНИЕ ПРОВЕРЕНО; загрузка не подтверждена"
				r.Problems = append(r.Problems, "Нет свежего heartbeat DDS после текущего запуска: "+err.Error())
			} else {
				// A newly executing DDS plugin is evidence of BD loading, not a process-only guess.
				r.BetterDiscord = "РАБОТАЕТ — подтверждено свежим heartbeat DDS"
				r.Plugin = "РАБОТАЕТ — свежий heartbeat текущего запуска"
			}
		}
	}
	if companion {
		r.Companion = "ЗАПУСК НЕ ПОДТВЕРЖДЁН"
		r.Archive = "НЕ ПОДТВЕРЖДЕНО"
		if err := awaitSetupComponent(ctx, 30*time.Second, h.CompanionRunning); err != nil {
			r.Problems = append(r.Problems, "Companion: "+err.Error())
		} else {
			r.Companion = "ЗАПУЩЕНО; обнаружение Plugin проверить в статусе приложения"
			if err := awaitSetupComponent(ctx, 20*time.Second, h.Archive); err != nil {
				r.Problems = append(r.Problems, fmt.Sprintf("SQLite: %v", err))
			} else {
				r.Archive = "ФАЙЛ АРХИВА ПОДТВЕРЖДЁН"
			}
		}
	}
	return r
}
