package main

import (
	"fmt"
	"net/http"
	"strings"
)

// Only expose bounded status/header diagnostics; do not print response bodies,
// access tokens or request URLs that may contain credentials.
func githubHTTPError(resp *http.Response) error {
	base := fmt.Sprintf("сервер вернул HTTP %d", resp.StatusCode)
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return fmt.Errorf("%s", base)
	}
	remaining := strings.TrimSpace(resp.Header.Get("X-RateLimit-Remaining"))
	reset := strings.TrimSpace(resp.Header.Get("X-RateLimit-Reset"))
	retry := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if remaining == "0" {
		if reset != "" {
			return fmt.Errorf("%s: GitHub API rate limit исчерпан; reset UNIX %s; повторные запросы не выполнялись", base, reset)
		}
		return fmt.Errorf("%s: GitHub API rate limit исчерпан; повторные запросы не выполнялись", base)
	}
	if retry != "" {
		return fmt.Errorf("%s: GitHub запросил паузу Retry-After=%s; повторные запросы не выполнялись", base, retry)
	}
	return fmt.Errorf("%s: причина неизвестна (возможны ограничения GitHub API или сети); повторные запросы не выполнялись", base)
}
