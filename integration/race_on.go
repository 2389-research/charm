// ABOUTME: Build-tagged file that indicates race detector is enabled.
// ABOUTME: Used to skip tests with known upstream data races (e.g., BadgerDB).
//go:build race

package integration

// raceEnabled is true when the race detector is active.
const raceEnabled = true
