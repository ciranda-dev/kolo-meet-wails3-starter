//go:build finished && !race

package main

// raceEnabled is true when the tests run under the race detector, which makes encoding several times slower.
const raceEnabled = false
