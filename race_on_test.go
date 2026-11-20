//go:build finished && race

package main

// raceEnabled is true when the tests run under the race detector, which makes encoding several times slower.
const raceEnabled = true

// An Opus encoder with the forward error correction on takes 29 ms for a 20 ms frame under the race detector (0.5 ms
// without it), so a microphone could never deliver 50 frames a second; complexity 5 takes 11 ms.
func init() { audioComplexity = 5 }
