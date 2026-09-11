//go:build !race

package core

// raceEnabled is the non-race twin of race_enabled.go; see its comment
// for why the sentinel pair exists.
const raceEnabled = false
