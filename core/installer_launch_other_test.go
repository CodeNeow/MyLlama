//go:build !windows

package core

import "testing"

// TestLaunchInstallerOther verifies the non-Windows installer launcher is the
// plain detached exec: a nonexistent path fails (the underlying exec errors),
// proving the launcher plumbing runs without elevation concerns.
func TestLaunchInstallerOther(t *testing.T) {
	if err := launchInstaller("nonexistent-myllama-installer"); err == nil {
		t.Fatal("launchInstaller on a nonexistent path must return an error")
	}
}
