//go:build windows

package main

import "testing"

func TestUserElevationPolicy(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		elevated, disabled, blocked bool
	}{
		{"ordinary UAC desktop", false, false, false},
		{"explicit elevation with UAC", true, false, true},
		{"ordinary admin desktop without UAC", true, true, false},
		{"standard user without UAC", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := elevationBlockedByPolicy(tc.elevated, tc.disabled); got != tc.blocked {
				t.Fatalf("blocked=%v, want %v", got, tc.blocked)
			}
		})
	}
}

func TestReadActualElevationPolicy(t *testing.T) {
	t.Logf("elevated=%v UAC explicitly disabled=%v blocked=%v", processElevated(), uacExplicitlyDisabled(), userProcessElevationBlocked())
	if got, want := userProcessElevationBlocked(), elevationBlockedByPolicy(processElevated(), uacExplicitlyDisabled()); got != want {
		t.Fatalf("blocked=%v, want %v", got, want)
	}
}
