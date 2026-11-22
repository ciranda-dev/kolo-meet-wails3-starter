//go:build finished

package main

import (
	"testing"
	"time"

	kolotransport "github.com/ciranda-dev/kolo-transport"
)

// The bitrate follows the path: it falls to a share of the throughput measured while the path is full, never goes
// below its floor, grows back slowly once the path stays clear, and a lost group asks for a key frame.
func TestAdapterFollowsThePath(t *testing.T) {
	const full = 1_900_000.0 // bits a second
	a := newAdapter()
	now := time.Unix(1000, 0)
	var s pathSample
	step := func(carry float64, delay time.Duration, drops uint64) decision {
		now = now.Add(adaptEvery)
		s.sent += uint64(carry / 8 * adaptEvery.Seconds())
		s.delay, s.drops = delay, drops
		return a.step(now, s, full)
	}
	if d := step(2e6, 0, 0); d.scale != 1 || d.key || d.skip {
		t.Fatalf("first sample: %+v", d)
	}
	coldRun(a, &now, &s, full)
	a.scale = 1
	// The path carries 1 Mbit/s and the queue grows: the rate comes down to 75% of it minus the audio's share.
	var d decision
	for range 8 {
		d = step(1e6, 400*time.Millisecond, 0)
	}
	want := (keepShare*1e6 - audioShare) / full
	if d.scale > want*1.05 || d.scale < want*0.95 {
		t.Fatalf("scale %.3f, want about %.3f", d.scale, want)
	}
	// An older queue skips frames until it is under skipDelay again.
	if d = step(1e6, 2*time.Second, 0); !d.skip {
		t.Fatalf("a 2 s queue does not skip: %+v", d)
	}
	if d = step(1e6, 0, 0); d.skip {
		t.Fatalf("an empty queue skips: %+v", d)
	}
	// A reset or expired group asks for a key frame, once.
	if d = step(1e6, 0, 1); !d.key {
		t.Fatalf("a lost group asks for no key frame: %+v", d)
	}
	if d = step(1e6, 0, 1); d.key {
		t.Fatalf("the same lost group asks twice: %+v", d)
	}
	// Clear for a while: the rate grows back, slowly, to the full rate.
	low := d.scale
	for range int(clearFor/adaptEvery) + 4 {
		d = step(1e6, 0, 1)
	}
	if d.scale <= low || d.scale > low+0.1 {
		t.Fatalf("grew from %.3f to %.3f", low, d.scale)
	}
	for range 400 {
		d = step(1e6, 0, 1)
	}
	if d.scale != 1 {
		t.Fatalf("never back to the full rate: %.3f", d.scale)
	}
	// A path that carries almost nothing stops at the floor.
	for range 100 {
		d = step(20e3, time.Second, 1)
	}
	if d.scale != minScale {
		t.Fatalf("floor: %.3f", d.scale)
	}
}

// sampleOf makes the sample of a session that has sent so many bytes with its oldest object waiting that long.
func sampleOf(id any, sent uint64, delay time.Duration, queued bool) sessionSample {
	return sessionSample{id: id, sample: pathSample{sent: sent, delay: delay}, queued: queued}
}

// A guest that carries nothing for stallAfter stops being counted: the camera stays where the guests that receive
// it put it, and the dead one counts again, with a keyframe, when its session moves.
func TestFairIgnoresDeadGuest(t *testing.T) {
	f := newFair()
	now := t0
	full := 1.9e6
	cur := decision{scale: 1}
	var sentOK, sentDead uint64
	var out int
	var d decision
	for i := 0; i < 80; i++ { // 20 s of 250 ms
		now = now.Add(adaptEvery)
		sentOK += 60_000 // 1.9 Mbit/s
		// the dead guest's queue is old and nothing leaves
		d, out = f.step(now, []sessionSample{sampleOf("ok", sentOK, 0, false), sampleOf("dead", sentDead, 3*time.Second, true)}, full, cur)
		cur = d
		if now.Sub(t0) > stallAfter+time.Second && out != 1 {
			t.Fatalf("at %v: %d not counted, want 1", now.Sub(t0), out)
		}
		if now.Sub(t0) > stallAfter+time.Second && (d.scale < 1 || d.skip) {
			t.Fatalf("at %v: the camera is at %.2f skip %v because of the dead guest", now.Sub(t0), d.scale, d.skip)
		}
	}
	// while it is counted (the first stallAfter) the dead one does lower the best of nothing: the healthy one is the best
	if d.scale != 1 {
		t.Fatalf("scale %.2f", d.scale)
	}
	// it moves again
	now = now.Add(adaptEvery)
	d, out = f.step(now, []sessionSample{sampleOf("ok", sentOK+60_000, 0, false), sampleOf("dead", sentDead+100_000, 0, true)}, full, cur)
	if out != 0 || !d.key {
		t.Fatalf("after it moved: %d not counted, key %v", out, d.key)
	}
}

// The camera follows the best guest: a guest on a thin path, queue old and carrying little, does not pull it down.
func TestFairFollowsBestGuest(t *testing.T) {
	f := newFair()
	now := t0
	full := 1.9e6
	cur := decision{scale: 1}
	var okSent, slowSent uint64
	for i := 0; i < 120; i++ {
		now = now.Add(adaptEvery)
		okSent += 60_000
		slowSent += 9_000 // about 300 kbit/s
		var d decision
		d, _ = f.step(now, []sessionSample{sampleOf("ok", okSent, 0, false), sampleOf("slow", slowSent, 800*time.Millisecond, true)}, full, cur)
		cur = d
	}
	if cur.scale != 1 {
		t.Fatalf("the camera is at %.2f of its rate with a healthy guest in the call", cur.scale)
	}
	// alone, the same thin guest does pull it down: the controller is the same as before for one session
	f = newFair()
	cur = decision{scale: 1}
	slowSent = 0
	for i := 0; i < 120; i++ {
		now = now.Add(adaptEvery)
		slowSent += 9_000
		cur, _ = f.step(now, []sessionSample{sampleOf("slow", slowSent, 800*time.Millisecond, true)}, full, cur)
	}
	if cur.scale > 0.25 {
		t.Fatalf("a thin path alone left the camera at %.2f", cur.scale)
	}
}

func TestFairForgetsSessionsThatLeft(t *testing.T) {
	f := newFair()
	f.step(t0, []sessionSample{sampleOf("a", 0, 0, false), sampleOf("b", 0, 0, false)}, 1e6, decision{scale: 1})
	f.step(t0.Add(adaptEvery), []sessionSample{sampleOf("a", 0, 0, false)}, 1e6, decision{scale: 1})
	if len(f.per) != 1 {
		t.Fatalf("%d sessions kept", len(f.per))
	}
}

func TestLossWatch(t *testing.T) {
	var l lossWatch
	now := t0
	if got := l.step(now, 0); got != 0 {
		t.Fatal(got)
	}
	now = now.Add(time.Second)
	if got := l.step(now, 0); got != 0 {
		t.Fatalf("loss %d with none seen", got)
	}
	now = now.Add(time.Second)
	if got := l.step(now, 2); got != minLoss { // 2 groups in 50 a second is 4%: the least is 5
		t.Fatalf("loss %d, want %d", got, minLoss)
	}
	now = now.Add(time.Second)
	if got := l.step(now, 2+40); got != maxLoss {
		t.Fatalf("loss %d, want %d", got, maxLoss)
	}
	now = now.Add(lossHold - time.Second)
	if got := l.step(now, 42); got == 0 {
		t.Fatal("loss dropped before lossHold")
	}
	now = now.Add(2 * time.Second)
	if got := l.step(now, 42); got != 0 {
		t.Fatalf("loss %d after lossHold with none seen", got)
	}
}

// coldRun gives a new adapter its first run of full ticks, which it does not measure, and a clear tick that ends it.
func coldRun(a *adapter, now *time.Time, s *pathSample, full float64) {
	for _, delay := range []time.Duration{0, 400 * time.Millisecond, 400 * time.Millisecond, 0} {
		*now = now.Add(adaptEvery)
		s.delay = delay
		a.step(*now, *s, full)
	}
}

// The first key frame on a cold transport window fills the queue while the window, not the path, sets how much leaves:
// that run gives no estimate of the path, and its cuts follow what the transport lets through, but no lower than the
// usual cut, since a new window reads low.
func TestAdapterIgnoresTheColdKeyFrame(t *testing.T) {
	const full = 1_900_000.0
	for _, c := range []struct {
		bps, want float64
	}{
		{2.5e6, (keepShare*2.5e6 - audioShare) / full}, // the transport carries more than the usual cut leaves
		{0.2e6, 0.8 * 0.8}, // a cold window: two usual cuts in 1.5 s, not the window's 0.2 Mbit/s
	} {
		a := newAdapter()
		now := time.Unix(1000, 0)
		s := pathSample{minRTT: 470 * time.Millisecond, bps: c.bps}
		a.step(now, s, full)
		var d decision
		for range 6 { // 1.5 s of a full queue, 15 kbit/s leaving
			now = now.Add(adaptEvery)
			s.sent += uint64(15e3 / 8 * adaptEvery.Seconds())
			s.delay = 600 * time.Millisecond
			d = a.step(now, s, full)
			if d.est != 0 {
				t.Fatalf("an estimate of %.0f bit/s from the cold window", d.est)
			}
		}
		if d.scale > c.want*1.02 || d.scale < c.want*0.98 {
			t.Errorf("transport at %.0f bit/s: scale %.3f, want %.3f", c.bps, d.scale, c.want)
		}
	}
	a := newAdapter()
	now := time.Unix(1000, 0)
	s := pathSample{minRTT: 470 * time.Millisecond, bps: 1e6}
	a.step(now, s, full)
	for range 6 {
		now = now.Add(adaptEvery)
		s.delay = 600 * time.Millisecond
		a.step(now, s, full)
	}
	var d decision
	// Once that run is over, a full queue measures the path.
	now = now.Add(adaptEvery)
	s.delay = 0
	a.step(now, s, full)
	for range 4 {
		now = now.Add(adaptEvery)
		s.sent += uint64(800e3 / 8 * adaptEvery.Seconds())
		s.delay = 400 * time.Millisecond
		d = a.step(now, s, full)
	}
	if d.est < 700e3 || d.est > 900e3 {
		t.Fatalf("estimate %.0f bit/s after the cold run, want about 800 kbit/s", d.est)
	}
}

// A throughput measured over less than a round trip is no measure: on a 500 ms path the bytes leave in bursts a round
// trip apart, so a tick of 250 ms sees nothing or twice the rate. The estimate waits for a window of a round trip.
func TestAdapterMeasuresOverARoundTrip(t *testing.T) {
	const full = 1_900_000.0
	a := newAdapter()
	now := time.Unix(1000, 0)
	s := pathSample{minRTT: 500 * time.Millisecond}
	a.step(now, s, full)
	coldRun(a, &now, &s, full)
	var ests []float64
	for i := range 8 { // 1 Mbit/s: 62.5 KB every other tick
		now = now.Add(adaptEvery)
		if i%2 == 0 {
			s.sent += 62_500
		}
		s.delay = 400 * time.Millisecond
		ests = append(ests, a.step(now, s, full).est)
	}
	if ests[0] != 0 {
		t.Fatalf("an estimate of %.0f bit/s after half a round trip", ests[0])
	}
	for _, e := range ests[1:] {
		if e < 0.8e6 || e > 1.4e6 {
			t.Fatalf("estimates %v, want about 1 Mbit/s once the window is a round trip", ests)
		}
	}
}

// Clear for long enough, a rate high in its range grows by 8% of itself a second, faster than the 5% of the full rate
// it grows by at the bottom.
func TestAdapterRampsUpByEightPercentASecond(t *testing.T) {
	const full = 1_900_000.0
	a := newAdapter()
	now := time.Unix(1000, 0)
	var s pathSample
	a.step(now, s, full)
	a.scale = 0.8
	var d decision
	for range int(clearFor/adaptEvery) + 1 {
		now = now.Add(adaptEvery)
		d = a.step(now, s, full)
	}
	from := d.scale
	for range int(time.Second / adaptEvery) {
		now = now.Add(adaptEvery)
		d = a.step(now, s, full)
	}
	if got, want := d.scale/from, 1.08; got < want-0.005 || got > want+0.005 {
		t.Fatalf("grew by %.3f in a second from %.3f, want %.2f", got, from, want)
	}
}

// What the transport lets through, in bits a second: the window over the round trip, and for BBR the larger of its
// bandwidth and half that, since its window is twice its bandwidth-delay product and its bandwidth reads low while
// little was sent.
func TestTransportRate(t *testing.T) {
	ms := time.Millisecond
	for _, c := range []struct {
		st   kolotransport.ConnStats
		want float64
	}{
		{kolotransport.ConnStats{CongestionController: "bbr", BandwidthEstimate: 125_000, CongestionWindow: 20_000, SmoothedRTT: 100 * ms}, 1e6},
		{kolotransport.ConnStats{CongestionController: "bbr", BandwidthEstimate: 10_000, CongestionWindow: 125_000, SmoothedRTT: 500 * ms}, 1e6},
		{kolotransport.ConnStats{CongestionController: "reno", CongestionWindow: 62_500, SmoothedRTT: 500 * ms}, 1e6},
		{kolotransport.ConnStats{CongestionController: "reno", CongestionWindow: 62_500}, 0},
	} {
		if got := transportRate(c.st); got != c.want {
			t.Errorf("%+v: %.0f, want %.0f", c.st, got, c.want)
		}
	}
}

// A first run of a full queue that lasts longer than a key frame can is the path: it is measured from then on.
func TestAdapterMeasuresALongFirstRun(t *testing.T) {
	const full = 1_900_000.0
	a := newAdapter()
	now := time.Unix(1000, 0)
	s := pathSample{minRTT: 100 * time.Millisecond}
	a.step(now, s, full)
	var d decision
	for range int(clearFor/adaptEvery) + 4 { // 600 kbit/s leaving a full queue from the start
		now = now.Add(adaptEvery)
		s.sent += uint64(600e3 / 8 * adaptEvery.Seconds())
		s.delay = 400 * time.Millisecond
		d = a.step(now, s, full)
	}
	if d.est < 500e3 || d.est > 700e3 {
		t.Fatalf("estimate %.0f bit/s after a first run of %v, want about 600 kbit/s", d.est, clearFor+time.Second)
	}
}
