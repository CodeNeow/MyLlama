package core

import "testing"

// TestEffectiveHost verifies effectiveHost pure function: lan → 0.0.0.0,
// all other values (including empty and invalid) → 127.0.0.1.
// Shared by SaveServerConfig / loadConfig / buildServerCommand for consistent host derivation.
func TestEffectiveHost(t *testing.T) {
	withPlatformGOOS(t, "windows")
	cases := []struct {
		mode string
		want string
	}{
		{accessLocal, "127.0.0.1"},
		{accessLAN, "0.0.0.0"},
		{"", "127.0.0.1"},       // empty string falls back to loopback
		{"local ", "127.0.0.1"}, // whitespace-padded invalid value falls back to loopback
		{"wan", "127.0.0.1"},    // out-of-whitelist value falls back to loopback
		{"0.0.0.0", "127.0.0.1"},
	}
	for _, c := range cases {
		if got := effectiveHost(c.mode); got != c.want {
			t.Errorf("effectiveHost(%q) = %q, want %q", c.mode, got, c.want)
		}
	}
}

// TestEffectiveHostAndroidLocalOnly pins the product decision: a phone never
// serves other devices, so even a stale persisted "lan" access mode (left by
// an older version) derives the loopback listen address on Android — the
// lan bind is refused at the single derivation point shared by
// SaveServerConfig, loadConfig and buildServerCommand.
func TestEffectiveHostAndroidLocalOnly(t *testing.T) {
	withPlatformGOOS(t, "android")
	if got := effectiveHost(accessLAN); got != "127.0.0.1" {
		t.Errorf("effectiveHost(lan) on android = %q, want 127.0.0.1 (phones never bind the LAN)", got)
	}
	if got := effectiveHost(accessLocal); got != "127.0.0.1" {
		t.Errorf("effectiveHost(local) on android = %q, want 127.0.0.1", got)
	}
}
