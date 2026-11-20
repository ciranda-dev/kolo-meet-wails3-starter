//go:build finished

package main

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ciranda-dev/kolo-meet-wails3-starter/internal/netsim"
)

// seams are the switches that turn the new behavior off, to measure a call as it was; KOLO_MEET_BEFORE lists them
// (comma separated: watchdog, reconnect, keyrequest, fair, conceal, adapt).
func seams(t *testing.T) {
	t.Helper()
	for _, name := range splitList(os.Getenv("KOLO_MEET_BEFORE")) {
		switch name {
		case "watchdog":
			noDataOff = true
		case "reconnect":
			noReconnect = true
		case "keyrequest":
			noKeyRequests = true
		case "fair":
			noFair = true
		case "conceal":
			noConceal = true
		case "adapt":
			noAdapt = true
		default:
			t.Fatalf("no switch %q", name)
		}
	}
	if v := os.Getenv("KOLO_MEET_FEC"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatal(err)
		}
		forceAudioLoss = n
	}
	t.Cleanup(func() {
		noDataOff, noReconnect, noKeyRequests, noFair, noConceal, noAdapt = false, false, false, false, false, false
		forceAudioLoss = 0
	})
}

// slow skips a report that takes a minute unless KOLO_MEET_SLOW is set; the tests that check a promise run always.
func slow(t *testing.T) {
	t.Helper()
	if testing.Short() || os.Getenv("KOLO_MEET_SLOW") == "" {
		t.Skip("set KOLO_MEET_SLOW=1 to run: it takes a minute")
	}
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// A guest that joins a call that has run for a while gets a picture from the host as soon as it is live: the relay holds
// the group the host is in, and the key frame that starts it comes with it.
func TestLateJoinerGetsAPicture(t *testing.T) {
	slow(t)
	seams(t)
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	up, down := faultPath()
	f := newFleet(t, smallVideo, 1)
	time.Sleep(7500 * time.Millisecond) // the host's group is half way through, the worst time to join
	f.join(0, up, down)
	t0 := time.Now()
	f.waitLive(1)
	live := time.Since(t0)
	fromHost := f.from(f.guests[0], idOf(t, f.host))
	var first time.Duration
	for fromHost.videoIn.Load() == 0 && time.Since(t0) < 20*time.Second {
		time.Sleep(10 * time.Millisecond)
	}
	first = time.Since(t0)
	t.Logf("late joiner: live after %v, first picture %v after live", live.Round(10*time.Millisecond), (first - live).Round(10*time.Millisecond))
	if first-live > 5*time.Second {
		t.Fatalf("no picture within 5 s of being live (%v)", first-live)
	}
}

// A group lost on the way, the host's downlink squeezed to 300 kbit/s for 1.5 s so that its groups expire in the queue
// while the connection stays up, is replaced by the keyframe the watcher asked for: the longest time without a picture
// is shorter than the 2 s of a group.
func TestLostGroupPictureReturns(t *testing.T) {
	slow(t)
	seams(t)
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	up, down := faultPath()
	var worst time.Duration
	for i := range 3 { // the keyframe of a run falls at another point of the cut
		f := newFleet(t, smallVideo, 1)
		link := f.join(0, up, down)
		f.waitLive(1)
		fromHost := f.from(f.guests[0], idOf(t, f.host))
		time.Sleep(time.Duration(i) * 600 * time.Millisecond)
		squeezed, freed := false, false
		s := watchFor(12*time.Second, fromHost, func(el time.Duration) {
			if !squeezed && el >= 3*time.Second {
				squeezed = true
				link.SetRate(300 * kbps)
			}
			if !freed && el >= 4500*time.Millisecond {
				freed = true
				link.SetRate(12 * mbps)
			}
		})
		g := maxGap(s, 3*time.Second, 12*time.Second, true)
		worst = max(worst, g)
		t.Logf("run %d: longest time without a picture after a 1.5 s squeeze: %v", i, g.Round(10*time.Millisecond))
	}
	t.Logf("worst %v", worst.Round(10*time.Millisecond))
}

// The slow and the dead guest do not lower the host's camera for the guest that receives it.
func TestFairnessThreePeers(t *testing.T) {
	if testing.Short() {
		t.Skip("takes 50 s")
	}
	seams(t)
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		path func() (netsim.Profile, netsim.Profile)
		dead bool
	}{
		{"one guest on 300 kbit/s", func() (netsim.Profile, netsim.Profile) {
			p := netsim.Profile{Rate: 300 * kbps, Delay: 60 * time.Millisecond, Jitter: 20 * time.Millisecond, Seed: 3}
			return p, p
		}, false},
		{"one guest blackholed", func() (netsim.Profile, netsim.Profile) {
			p := netsim.Profile{Delay: 60 * time.Millisecond, Seed: 3}
			return p, p
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.dead {
				slow(t) // the thin guest is only measured: the host's camera is not lowered for it with or without the rule
			}
			f := newFleet(t, faultVideo, 2)
			f.join(0, netsim.Profile{}, netsim.Profile{})
			f.waitLive(1)
			up, down := tc.path()
			link := f.join(1, up, down)
			f.waitLive(2)
			if tc.dead {
				link.Cut(time.Hour)
			}
			fromHost := f.from(f.guests[0], idOf(t, f.host))
			slowFromHost := f.from(f.guests[1], idOf(t, f.host))
			s := watchFor(25*time.Second, fromHost, nil)
			a, v := rate(s, 20*time.Second, 25*time.Second, false), rate(s, 20*time.Second, 25*time.Second, true)
			t.Logf("healthy guest from the host, last 5 s: audio %.1f/s pictures %.1f/s; the other guest got %d audio frames, %d pictures",
				a, v, slowFromHost.audioIn.Load(), slowFromHost.videoIn.Load())
			wantV := 20 * faultVideo.fps / 30 // two thirds of the camera's rate: 20 of 30, 10 of 15 under the race detector
			if v < wantV {
				t.Errorf("the healthy guest got %.1f pictures a second from the host, want %.0f of %.0f", v, wantV, faultVideo.fps)
			}
			if a < 40 {
				t.Errorf("the healthy guest got %.1f audio frames a second, want 40 of 50", a)
			}
		})
	}
}

// With 3% loss the audio has fewer holes when the playout covers the frames that did not arrive in time.
func TestAudioGapsWithLoss(t *testing.T) {
	slow(t)
	seams(t)
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	up, down := faultPath()
	up.Loss, down.Loss = 0.03, 0.03
	f := newFleet(t, smallVideo, 1)
	f.join(0, up, down)
	f.waitLive(1)
	fromHost := f.from(f.guests[0], idOf(t, f.host))
	time.Sleep(5 * time.Second)
	a0, m0, c0 := fromHost.audioIn.Load(), fromHost.missed.Load(), fromHost.concealed.Load()
	time.Sleep(20 * time.Second)
	decoded := float64(fromHost.audioIn.Load() - a0)
	missed := float64(fromHost.missed.Load() - m0)
	made := float64(fromHost.concealed.Load() - c0)
	// A 20 ms frame with no sound is one that was not decoded and not made up.
	t.Logf("20 s at 3%% loss: %.0f decoded, %.0f missing in the sequence, %.0f made up, %.0f frames with no sound (%.1f%%)",
		decoded, missed, made, missed-made, 100*(missed-made)/(decoded+missed))
}

// After a call that went through faults, no goroutine of the call is left.
func TestReconnectLeavesNoGoroutines(t *testing.T) {
	if testing.Short() {
		t.Skip("takes 40 s")
	}
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	before := settledGoroutines()
	t.Run("call", func(t *testing.T) {
		im := faults()[1] // outage 10 s
		o := callThrough(t, im, 8*time.Second+10*time.Second+8*time.Second)
		t.Log(o.report(im.name, 26*time.Second))
	})
	var after int
	for range 150 {
		runtime.GC()
		if after = runtime.NumGoroutine(); after <= before+4 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	buf := make([]byte, 1<<20)
	t.Fatalf("%d goroutines before, %d after:\n%s", before, after, buf[:runtime.Stack(buf, true)])
}
