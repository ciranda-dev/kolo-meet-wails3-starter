//go:build finished

package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ciranda-dev/kolo-meet-wails3-starter/internal/netsim"
)

// smallVideo is a picture light enough for the machine at the owner's 1.9 Mbit/s: 640x360, and 320x180 under the race
// detector, which makes encoding several times slower.
var smallVideo = func() videoMode {
	if raceEnabled {
		return videoMode{w: 320, h: 180, fps: 30, bitrate: 1_900_000}
	}
	return videoMode{w: 640, h: 360, fps: 30, bitrate: 1_900_000}
}()

// faultVideo is the picture of the tests that break the connection rather than fill it: the same as smallVideo, and a
// 160x120 picture at 15 frames a second under the race detector, where several services encode and decode in one
// process on a machine that other work shares.
var faultVideo = func() videoMode {
	if raceEnabled {
		return videoMode{w: 160, h: 120, fps: 15, bitrate: 400_000}
	}
	return smallVideo
}()

// faultPath is the mobile path of the owner's report: 3 Mbit/s up, 12 down, 60 ms and 1% loss each way.
func faultPath() (netsim.Profile, netsim.Profile) {
	return netsim.Profile{Rate: 3 * mbps, Delay: 60 * time.Millisecond, Jitter: 20 * time.Millisecond, Loss: 0.01, Seed: 1},
		netsim.Profile{Rate: 12 * mbps, Delay: 60 * time.Millisecond, Jitter: 20 * time.Millisecond, Loss: 0.01, Seed: 2}
}

// faults are the failures a call has to come back from. A fault happens at 8 s of the run.
func faults() []impairment {
	up, down := faultPath()
	mk := func(name string, f func(*netsim.Link)) impairment {
		return impairment{name: name, up: up, down: down, video: faultVideo, events: []linkEvent{{8 * time.Second, f}}}
	}
	cut := func(d time.Duration) impairment {
		return impairment{name: "outage " + d.String(), up: up, down: down, video: faultVideo, cutAt: 8 * time.Second, cutFor: d}
	}
	short := cut(3 * time.Second)
	short.slow = true // the transport carries a call through 3 s by itself
	dialer := mk("dialer port change", func(l *netsim.Link) { l.Rebind() })
	dialer.slow = true // the connection migrates by itself
	return []impairment{
		short,
		cut(10 * time.Second),
		cut(40 * time.Second),
		mk("large datagrams dropped (path MTU 1300)", func(l *netsim.Link) { l.SetMTU(1300) }),
		dialer,
		mk("listener port change", func(l *netsim.Link) { l.RebindFront() }),
	}
}

// TestFaultReport measures a call through each fault and prints the numbers; set KOLO_MEET_FAULT=<seconds after the
// fault to watch> and KOLO_MEET_FAULT_ONLY=<part of a name>.
func TestFaultReport(t *testing.T) {
	v := os.Getenv("KOLO_MEET_FAULT")
	if v == "" {
		t.Skip("set KOLO_MEET_FAULT=<seconds> to measure")
	}
	after, err := time.ParseDuration(v + "s")
	if err != nil {
		t.Fatal(err)
	}
	only := os.Getenv("KOLO_MEET_FAULT_ONLY")
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, im := range faults() {
		if only != "" && !strings.Contains(im.name, only) {
			continue
		}
		t.Run(im.name, func(t *testing.T) {
			dur := im.cutAt + im.cutFor + after
			if im.cutAt == 0 {
				dur = 8*time.Second + after
			}
			o := callThrough(t, im, dur)
			t.Log("\n" + o.report(im.name, dur) + "\nstate: " + o.state)
		})
	}
}

// A call that meets a fault comes back by itself, with its people, its pictures and its sound, and does not end.
func TestCallRecoversFromFaults(t *testing.T) {
	if testing.Short() {
		t.Skip("takes 3 minutes")
	}
	seams(t)
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	const after = 18 * time.Second // what the call has to come back in, after the fault ends
	for _, im := range faults() {
		if im.slow && os.Getenv("KOLO_MEET_SLOW") == "" {
			continue
		}
		t.Run(im.name, func(t *testing.T) {
			faultEnd := 8*time.Second + im.cutFor
			dur := faultEnd + after
			o := callThrough(t, im, dur)
			t.Log("\n" + o.report(im.name, dur) + "\nstate: " + o.state)
			if o.ended != nil {
				t.Fatalf("the call ended: %s", o.ended.Reason)
			}
			if o.left != 0 || o.people != [2]int{2, 2} {
				t.Errorf("%d people left the call, %v in the call at the end, want 0 and 2 each", o.left, o.people)
			}
			for _, who := range []struct {
				name string
				s    []sample
			}{{"host from guest", o.atHost}, {"guest from host", o.atGuest}} {
				if a := rate(who.s, dur-5*time.Second, dur, false); a < 40 {
					t.Errorf("%s: %.1f audio frames a second in the last 5 s, want 40 of 50", who.name, a)
				}
				if v := rate(who.s, dur-5*time.Second, dur, true); v < 10 {
					t.Errorf("%s: %.1f pictures a second in the last 5 s, want 10 of 30", who.name, v)
				}
			}
		})
	}
}
