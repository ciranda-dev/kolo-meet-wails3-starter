//go:build finished

package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ciranda-dev/kolo-meet-wails3-starter/internal/netsim"
)

// ladderRung is one change of the rung of the host's camera, at a time since the run began.
type ladderRung struct {
	at  time.Duration
	h   int
	why string
}

// ladderOut is what a run of the ladder over a link measured: the changes, the time at each rung, and what the guest
// received from the host.
type ladderOut struct {
	heights []int // the rungs, lowest first
	changes []ladderRung
	spent   map[int]time.Duration
	guest   []sample
	dur     time.Duration
}

func (o ladderOut) final() int {
	if len(o.changes) == 0 {
		return o.heights[0]
	}
	return o.changes[len(o.changes)-1].h
}

func (o ladderOut) ups() (n int) {
	prev := o.heights[0]
	for _, c := range o.changes {
		if c.h > prev {
			n++
		}
		prev = c.h
	}
	return n
}

// table is the seconds at each rung and the changes, for the log.
func (o ladderOut) table(name string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-34s", name)
	for _, h := range o.heights {
		fmt.Fprintf(&b, " %dp %5.1fs", h, o.spent[h].Seconds())
	}
	fmt.Fprintf(&b, " | longest gap between pictures %v | %d changes:", maxGap(o.guest, 2*time.Second, o.dur, true).Round(10*time.Millisecond), len(o.changes))
	for _, c := range o.changes {
		fmt.Fprintf(&b, " %.1fs->%dp (%s)", c.at.Seconds(), c.h, c.why)
	}
	return b.String()
}

// ladderVideo is the picture of the ladder tests: smallVideo, with rungs at a quarter and a half of its height, which
// keeps their bitrates, 0.12, 0.48 and 1.9 Mbit/s, those of 360p, 720p and a 1.9 Mbit/s 1080p picture at a thirtieth of
// the pixels.
func ladderVideo(t *testing.T) videoMode {
	t.Helper()
	old := ladderHeights
	ladderHeights = []int{smallVideo.h / 4, smallVideo.h / 2}
	t.Cleanup(func() { ladderHeights = old })
	return smallVideo
}

// runLadder runs a call of a host and a guest, whose path from the host is down (the guest's own is up), for dur, and
// measures the rungs the host's camera is sent at. events change the link at times since the guest was live.
func runLadder(t *testing.T, up, down netsim.Profile, dur time.Duration, events []linkEvent) ladderOut {
	t.Helper()
	v := ladderVideo(t)
	f := newFleet(t, v, 1)
	link := f.join(0, up, down)
	f.waitLive(1)
	o := ladderOut{heights: []int{v.h / 4, v.h / 2, v.h}, spent: map[int]time.Duration{}, dur: dur}
	var mu sync.Mutex
	begin := time.Now()
	rungChanged = func(c *call, r rung, why string) {
		if c.role != roleHost {
			return
		}
		mu.Lock()
		o.changes = append(o.changes, ladderRung{time.Since(begin), r.h, why})
		mu.Unlock()
	}
	t.Cleanup(func() { rungChanged = nil })
	fromHost := f.from(f.guests[0], idOf(t, f.host))
	events = append([]linkEvent(nil), events...)
	o.guest = watchFor(dur, fromHost, func(el time.Duration) {
		for i := range events {
			if e := &events[i]; e.do != nil && el >= e.at {
				e.do(link)
				e.do = nil
			}
		}
	})
	mu.Lock()
	defer mu.Unlock()
	cur, at := o.heights[0], time.Duration(0)
	for _, c := range o.changes {
		o.spent[cur] += min(c.at, dur) - min(at, dur)
		cur, at = c.h, c.at
	}
	o.spent[cur] += dur - min(at, dur)
	return o
}

func clearPath() (netsim.Profile, netsim.Profile) {
	return netsim.Profile{Delay: 30 * time.Millisecond, Seed: 1}, netsim.Profile{Delay: 30 * time.Millisecond, Seed: 2}
}

// On a path with nothing in its way, the host's camera climbs the ladder to the top, a rung at a time after the hold,
// and stays: the pictures reach the guest all the time.
func TestLadderClimbsOnTheHarness(t *testing.T) {
	if testing.Short() {
		t.Skip("takes 25 s")
	}
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	up, down := clearPath()
	o := runLadder(t, up, down, 20*time.Second, nil)
	t.Log("\n" + o.table("clear path"))
	if o.final() != o.heights[2] || o.ups() != 2 {
		t.Errorf("at %dp after %d steps up, want %dp after 2", o.final(), o.ups(), o.heights[2])
	}
	if len(o.changes) > 3 {
		t.Errorf("%d changes on a clear path", len(o.changes))
	}
	if v := rate(o.guest, 14*time.Second, 20*time.Second, true); v < 20 {
		t.Errorf("%.1f pictures a second at the guest in the last 6 s, want 20 of 30", v)
	}
}

// A path of 1 Mbit/s carries the lowest rung (0.12 Mbit/s of picture) or the middle one (0.48), and not the top (1.9):
// the camera tries the top, finds the queue full, steps down at once to a rung under the throughput it measured (which of
// the two depends on that measure, 0.5 to 0.7 Mbit/s, against the 1.25 times the rung and the audio it needs) and stays
// there, without a picture lost for more than a moment.
func TestLadderStepsDownOnAThinPathOnTheHarness(t *testing.T) {
	if testing.Short() {
		t.Skip("takes 30 s")
	}
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	up, down := clearPath()
	down.Rate = 1 * mbps
	o := runLadder(t, up, down, 28*time.Second, nil)
	t.Log("\n" + o.table("1 Mbit/s"))
	if o.final() > o.heights[1] || o.spent[o.heights[2]] > 3*time.Second {
		t.Errorf("at %dp, %v at the top", o.final(), o.spent[o.heights[2]])
	}
	if len(o.changes) > 6 {
		t.Errorf("%d changes in 28 s", len(o.changes))
	}
	if g := maxGap(o.guest, 8*time.Second, 28*time.Second, false); g > 3*time.Second {
		t.Errorf("the audio stopped for %v", g)
	}
	if v := rate(o.guest, 20*time.Second, 28*time.Second, true); v < 10 {
		t.Errorf("%.1f pictures a second in the last 8 s, want 10 of 30", v)
	}
}

// A path that loses a fifth of its packets takes the camera down the ladder.
func TestLadderStepsDownOnLossOnTheHarness(t *testing.T) {
	if testing.Short() {
		t.Skip("takes 30 s")
	}
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	up, down := clearPath()
	down.Loss = 0.2
	o := runLadder(t, up, down, 24*time.Second, nil)
	t.Log("\n" + o.table("20% loss"))
	if o.final() == o.heights[2] {
		t.Errorf("still at the top rung")
	}
}

// After a bad half minute the camera climbs back to the top, each step after a hold, and does not oscillate.
func TestLadderClimbsBackOnTheHarness(t *testing.T) {
	slow(t)
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	up, down := clearPath()
	o := runLadder(t, up, down, 80*time.Second, []linkEvent{
		{16 * time.Second, func(l *netsim.Link) { l.SetRate(1 * mbps) }},
		{36 * time.Second, func(l *netsim.Link) { l.SetRate(0) }},
	})
	t.Log("\n" + o.table("1 Mbit/s from 16 s to 36 s"))
	if o.final() != o.heights[2] {
		t.Errorf("at %dp, want %dp", o.final(), o.heights[2])
	}
	if len(o.changes) > 9 {
		t.Errorf("%d changes in 80 s", len(o.changes))
	}
}
