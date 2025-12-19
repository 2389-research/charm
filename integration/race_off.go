// ABOUTME: Build-tagged file that indicates race detector is disabled.
// ABOUTME: Used to skip tests with known upstream data races (e.g., BadgerDB).
//go:build !race

package integration

// raceEnabled is false when the race detector is not active.
// nolint:unused // This const is used via build tag switching with race_on.go
const raceEnabled = false
