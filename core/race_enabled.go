//go:build race

package core

// raceEnabled reports whether the build runs under the race detector
// (-race). The race build mode has no runtime API to query, so the
// standard build-tag sentinel pair (race_enabled.go / race_disabled.go)
// exposes it as a constant. Timing-sensitive benchmark tests consult it
// to skip themselves: race instrumentation slows memory traffic by
// roughly an order of magnitude, so wall-clock throughput assertions
// necessarily fall outside their plausibility windows under -race.
// That is an artifact of the instrumentation, not a regression.
const raceEnabled = true
