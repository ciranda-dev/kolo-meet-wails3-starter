package main

import "math"

// How lost audio is covered. They are the app's choices, from the 20 ms frames of the call.
const (
	// maxConceal is the most frames in a row made up for a gap; the rest of a longer gap stays silent, since a
	// decoder asked to go on for longer than 60 ms makes a drone.
	maxConceal = 3
	// maxGapFrames is the longest gap (500 ms) that is treated as lost frames. A longer one is a person who turned
	// the microphone off and on, or a stall, and nothing is made up for it.
	maxGapFrames = 25
	// mixTarget is the frames a person's queue may hold before the mixer starts to catch up: 100 ms, the playout's
	// least delay.
	mixTarget = 5
	// mixCatchEvery is how many mixer ticks pass between two steps of the catch-up. Each step takes one frame, 20 ms,
	// out of the queue, so a backlog shrinks by a third of its speed of playing: 20 ms in 60 ms.
	mixCatchEvery = 3
)

// noConceal is a test seam: a test sets it to hear a call that leaves gaps where frames are lost.
var noConceal bool

// gaps finds the frames missing between the audio frames of a person, from their timestamps in samples.
type gaps struct {
	last uint64
	have bool
}

// missing is how many frames of frameSamples were skipped between the previous frame and one made at ts.
func (g *gaps) missing(ts uint64) int {
	n := 0
	if g.have {
		if ts <= g.last {
			return 0 // not newer: nothing is missing, and the next gap counts from the newest
		}
		if d := ts - g.last; d > frameSamples {
			n = int((d - frameSamples + frameSamples/2) / frameSamples)
		}
	}
	g.last, g.have = ts, true
	return n
}

// crossfade makes one frame of two that follow each other, a and b of the same length: it starts as a and ends as b,
// so neither edge jumps, and the 20 ms between them are gone.
func crossfade(a, b []int16) []int16 {
	n := min(len(a), len(b))
	out := make([]int16, n)
	for i := range n {
		w := float64(i+1) / float64(n+1)
		out[i] = int16(math.Round(float64(a[i])*(1-w) + float64(b[i])*w))
	}
	return out
}
