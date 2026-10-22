package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCacheOnlyFourFloorsUseNoGithubAPI(t *testing.T) {
	c, visits := makeSamples(t, nil)
	root := t.TempDir()
	initial := runCascade(context.Background(), c, root, []string{"discord", "betterdiscord", "plugin", "companion"}, func(string) error { return nil })
	for _, x := range initial {
		if x.Result != "VERIFIED_AND_STAGED" {
			t.Fatalf("initial: %#v", initial)
		}
	}
	prev := make(map[string]int)
	for key, count := range visits {
		prev[key] = count
	}
	second := runCascade(context.Background(), c, root, []string{"discord", "betterdiscord", "plugin", "companion"}, func(string) error { return nil })
	for _, x := range second {
		if x.Component != "discord" && x.Result != "VERIFIED_CACHED" {
			t.Fatalf("cached: %#v", second)
		}
		if x.Component == "discord" && x.Result != "VERIFIED_AND_STAGED" {
			t.Fatalf("discord: %#v", second)
		}
	}
	for key, count := range visits {
		if prev[key] != count {
			t.Fatalf("network used for cached floor: %s = %d -> %d", key, prev[key], count)
		}
	}
}

func TestExternalCacheRejectsTamperedReceiptOriginAndVersion(t *testing.T) {
	c, _ := makeSamples(t, nil)
	root := t.TempDir()
	_, path, e := c.stageGitHubExternal(context.Background(), root, "plugin")
	if e != nil {
		t.Fatal(e)
	}
	c.AllowTestHTTP = false // enforce official GitHub identity even in sandbox
	if _, _, ok := validExternalCache(root, "plugin", false); ok {
		t.Fatal("test server origin wrongly trusted")
	}
	c.AllowTestHTTP = true
	for _, file := range []string{filepath.Join(path, "package", "DDS.plugin.js"), filepath.Join(path, "payload", "DDS.plugin.js")} {
		b, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(file, []byte("tampered"), 0600); e != nil {
			t.Fatal(e)
		}
		if _, _, ok := validExternalCache(root, "plugin", true); ok {
			t.Fatal("tampered file reused")
		}
		if e = os.WriteFile(file, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if _, _, ok := validExternalCache(root, "plugin", true); !ok {
		t.Fatal("original valid cache rejected")
	}
}

func TestCompanionCacheRejectsCorruptionWithoutNetwork(t *testing.T) {
	c, calls := mockServer(t, fakePackage("0.8.5", nil), "", nil)
	root := t.TempDir()
	_, path, e := c.stageCompanion(context.Background(), root)
	if e != nil {
		t.Fatal(e)
	}
	old := *calls
	_, _, e = c.stageCompanion(context.Background(), root)
	if e != nil || *calls != old {
		t.Fatalf("cache triggered network: %d->%d, %v", old, *calls, e)
	}
	app := filepath.Join(path, "payload", "versions", "0.8.5", "DDSApp.exe")
	if e = os.WriteFile(app, []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, ok := validCompanionCache(root, true); ok {
		t.Fatal("corrupt manifest payload used")
	}
}

func TestGithubHTTP403DiagnosticsNoRetry(t *testing.T) {
	cases := []struct {
		name    string
		headers http.Header
		want    string
	}{
		{"rate-limit", http.Header{"X-Ratelimit-Remaining": []string{"0"}, "X-Ratelimit-Reset": []string{"1790000000"}}, "rate limit"},
		{"retry", http.Header{"Retry-After": []string{"120"}}, "Retry-After=120"},
		{"unknown", http.Header{}, "причина неизвестна"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Response{StatusCode: 403, Header: tt.headers}
			msg := githubHTTPError(r).Error()
			if !strings.Contains(msg, "HTTP 403") || !strings.Contains(msg, tt.want) {
				t.Fatalf("%s", msg)
			}
		})
	}
}

func TestWhenNoCacheGithub403IsNotSilentlyIgnored(t *testing.T) {
	c, _ := makeSamples(t, nil)
	c.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Header: http.Header{}, Body: http.NoBody, Request: r}, nil
	})}
	_, _, err := c.stageGitHubExternal(context.Background(), t.TempDir(), "plugin")
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("missing block: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
