//go:build finished

package main

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	kolomedia "github.com/ciranda-dev/kolo-media"
	kolomoq "github.com/ciranda-dev/kolo-moq"
	"github.com/ciranda-dev/kolo-moq/secobj"
	kolovideo "github.com/ciranda-dev/kolo-video"
	"github.com/ciranda-dev/kolo-video/vpx"
)

// rungsOf is the ladder of a camera of w x h for a setting, with the native engine and one other person.
func rungsOf(v videoMode, w, h int) []rung {
	rs, _ := ladderRungs(v, w, h, nativeLimits, 1)
	return rs
}

// The rungs are the heights of the ladder below the top, at the camera's shape, and the top itself, which is never
// larger than the camera or the setting; the people in the call and the engine limit which of them are sent.
func TestLadderRungs(t *testing.T) {
	v := videoMode{w: 1920, h: 1080, fps: 30}
	hd := videoMode{w: 1280, h: 720, fps: 30}
	for _, tc := range []struct {
		name   string
		v      videoMode
		fw, fh int
		want   string
	}{
		{"1080p", v, 1920, 1080, "640x360 1280x720 1920x1080"},
		{"720p", v, 1280, 720, "640x360 1280x720"},
		{"a camera larger than the setting", hd, 1920, 1080, "640x360 1280x720"},
		{"a 4:3 camera", v, 640, 480, "480x360 640x480"},
		{"a 480p camera of the 16:9 shape", v, 854, 480, "640x360 854x480"},
		{"360p", v, 640, 360, "640x360"},
		{"a small camera", v, 320, 180, "320x180"},
		{"a 1000p camera is not given a rung close to it", v, 1778, 1000, "640x360 1280x720 1778x1000"},
		{"a 800p camera has no 720p rung", v, 1422, 800, "640x360 1422x800"},
		{"a 4:3 setting", videoMode{w: 640, h: 480, fps: 30}, 1920, 1080, "640x360"},
	} {
		var got []string
		for _, r := range rungsOf(tc.v, tc.fw, tc.fh) {
			got = append(got, fmt.Sprintf("%dx%d", r.w, r.h))
		}
		if s := strings.Join(got, " "); s != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, s, tc.want)
		}
	}
	rs := rungsOf(v, 1920, 1080)
	for i, want := range []float64{483840, 1935360, 3_800_000} {
		if rs[i].rate != want {
			t.Errorf("rung %d: %.0f bit/s, want %.0f", i, rs[i].rate, want)
		}
	}
	v.bitrate = 2_000_000 // a bitrate given for the setting keeps its bits per pixel at each rung
	rs = rungsOf(v, 1920, 1080)
	if int(rs[2].rate) != 2_000_000 || int(rs[1].rate) != 888888 || int(rs[0].rate) != 222222 {
		t.Errorf("rates with -video-bitrate: %v", rs)
	}
}

// The people in a call and the engine's limits keep the top rungs from being sent: each receiver decodes at most one
// real-time decoder's pixels a second, and nothing is larger than the engine encodes in real time.
func TestLadderAllowedRung(t *testing.T) {
	v := videoMode{w: 1920, h: 1080, fps: 30}
	for _, tc := range []struct {
		name   string
		lim    vpx.Limits
		others int
		limit  int
		low    string
	}{
		{"alone", nativeLimits, 0, 2, "640x360"},
		{"two people", nativeLimits, 1, 2, "640x360"},
		{"three people", nativeLimits, 2, 1, "640x360"},
		{"four people", nativeLimits, 3, 0, "640x360"},
		{"ten people", nativeLimits, 9, 0, "640x360"},
		{"eleven people", nativeLimits, 10, 0, "320x180"},
		{"the portable engine encodes up to 640x480", portableLimits, 1, 0, "640x360"},
		{"no engine: only the setting", vpx.Limits{}, 19, 2, "640x360"},
	} {
		rs, limit := ladderRungs(v, 1920, 1080, tc.lim, tc.others)
		if got := fmt.Sprintf("%dx%d", rs[0].w, rs[0].h); limit != tc.limit || got != tc.low {
			t.Errorf("%s: highest rung %d, lowest %s, want %d and %s", tc.name, limit, got, tc.limit, tc.low)
		}
	}
}

// shrink scales a picture down by any ratio, each pixel the mean of the block it covers, and keeps the times.
func TestShrink(t *testing.T) {
	src, _ := kolovideo.NewFrame(6, 6)
	for y := range 6 {
		for x := range 6 {
			src.Y[y*src.YStride+x] = byte(10 * (x/3 + 2*(y/3))) // four blocks of 3x3: 0 10 20 30
		}
	}
	for i := range src.U {
		src.U[i], src.V[i] = 100, 200
	}
	src.Timestamp, src.Duration = time.Second, time.Second/30
	dst, _ := kolovideo.NewFrame(2, 2) // 3:1
	shrink(dst, src)
	if want := []byte{0, 10, 20, 30}; dst.Y[0] != want[0] || dst.Y[1] != want[1] || dst.Y[dst.YStride] != want[2] || dst.Y[dst.YStride+1] != want[3] {
		t.Errorf("3:1: %v", dst.Y)
	}
	if dst.U[0] != 100 || dst.V[0] != 200 || dst.Timestamp != time.Second || dst.Duration != time.Second/30 || dst.Validate() != nil {
		t.Errorf("chroma or times: %+v", dst)
	}
	three, _ := kolovideo.NewFrame(4, 4) // 3:2: every source pixel is in a block, none twice
	shrink(three, src)
	if three.Validate() != nil || three.Y[0] != 0 || three.Y[3] != 10 && three.Y[3] != 20 && three.Y[3] != 15 {
		t.Errorf("3:2: %v", three.Y)
	}
	flat, _ := kolovideo.NewFrame(1920, 1080)
	for i := range flat.Y {
		flat.Y[i] = 77
	}
	for _, sz := range [][2]int{{1280, 720}, {640, 360}, {960, 540}, {319, 179}} {
		out, _ := kolovideo.NewFrame(sz[0], sz[1])
		shrink(out, flat)
		for y := range out.Height {
			for x := range out.Width {
				if out.Y[y*out.YStride+x] != 77 {
					t.Fatalf("%dx%d: pixel %d,%d is %d", sz[0], sz[1], x, y, out.Y[y*out.YStride+x])
				}
			}
		}
	}
}

// simPath is a model of the path for the ladder, on a virtual clock: a bottleneck with a queue, and groups lost. The
// controllers under test are the real ones; only the link and the encoder's output are modelled.
type simPath struct {
	rs      []rung
	top     int
	f       *fair
	lad     *ladder
	now     time.Time
	cur     int
	backlog float64 // bits queued
	sent    uint64
	drops   uint64
	dropped float64 // groups lost, with the fraction a step adds
	d       decision
	changes []string
	ups     []time.Duration // when the ladder stepped up
	spent   []time.Duration // time at each rung
	// the experiment
	capacity func(el time.Duration) float64 // bits a second, 0 for a path that carries nothing
	loss     func(el time.Duration) float64 // percent of the groups lost
	cpuTop   func(el time.Duration) int     // the highest rung the processor allows
	start    time.Time
}

func newSim(rs []rung, capacity func(time.Duration) float64) *simPath {
	t0 := time.Unix(5000, 0)
	return &simPath{rs: rs, top: len(rs) - 1, f: newFair(), lad: &ladder{}, now: t0, start: t0, capacity: capacity,
		loss: func(time.Duration) float64 { return 0 }, cpuTop: func(time.Duration) int { return len(rs) - 1 },
		d: decision{scale: 1}, spent: make([]time.Duration, len(rs))}
}

// run advances the model for d, one adapter step at a time.
func (s *simPath) run(d time.Duration) {
	for end := s.now.Add(d); s.now.Before(end); {
		el := s.now.Sub(s.start)
		cap := s.capacity(el)
		dt := adaptEvery.Seconds()
		offered := float64(audioShare)
		if !s.d.skip {
			offered += s.rs[s.cur].rate * s.d.scale
		}
		s.backlog += offered * dt
		served := min(s.backlog, cap*dt)
		s.backlog -= served
		s.sent += uint64(served / 8)
		var delay time.Duration
		if cap > 0 {
			delay = time.Duration(s.backlog / cap * float64(time.Second))
		} else if s.backlog > 0 {
			delay = time.Hour
		}
		if delay > 450*time.Millisecond { // what waits longer than the receivers play is expired
			s.dropped += audioFrames * dt
			s.backlog = min(s.backlog, cap*0.45)
		}
		s.dropped += s.loss(el) / 100 * audioFrames * dt
		s.drops = uint64(s.dropped)
		s.spent[s.cur] += adaptEvery
		s.now = s.now.Add(adaptEvery)
		d, _ := s.f.step(s.now, []sessionSample{{id: "s", queued: s.backlog > 0, sample: pathSample{sent: s.sent, delay: delay, drops: s.drops}}}, s.rs[s.cur].rate, s.d)
		top := min(s.top, s.cpuTop(el))
		if next, why := s.lad.step(s.now, s.rs, s.cur, top, d); next != s.cur {
			s.changes = append(s.changes, fmt.Sprintf("%.2fs %dx%d (%s)", s.now.Sub(s.start).Seconds(), s.rs[next].w, s.rs[next].h, why))
			if next > s.cur {
				s.ups = append(s.ups, s.now.Sub(s.start))
			}
			s.cur = next
			s.f.rebase(s.now)
			d.scale = 1
		}
		s.d = d
	}
}

// table is the seconds spent at each rung, lowest first.
func (s *simPath) table() string {
	var b strings.Builder
	for i, d := range s.spent {
		fmt.Fprintf(&b, "%dp %5.1fs  ", s.rs[i].h, d.Seconds())
	}
	return b.String()
}

func constant(bps float64) func(time.Duration) float64 {
	return func(time.Duration) float64 { return bps }
}

var hd1080 = videoMode{w: 1920, h: 1080, fps: 30}

// On a path that carries everything the ladder climbs to the top, a rung every upHold, and then stays: no change
// after that.
func TestLadderClimbsOnAClearPath(t *testing.T) {
	s := newSim(rungsOf(hd1080, 1920, 1080), constant(100e6))
	s.run(60 * time.Second)
	t.Logf("clear path, 60 s: %s; changes %v", s.table(), s.changes)
	if s.cur != 2 || len(s.changes) != 2 {
		t.Fatalf("at rung %d after %d changes %v, want the top after 2", s.cur, len(s.changes), s.changes)
	}
	if s.spent[0] < upHold || s.spent[0] > upHold+time.Second || s.spent[1] < upHold || s.spent[1] > upHold+time.Second {
		t.Errorf("time at the lower rungs: %s, want about %v each", s.table(), upHold)
	}
}

// A path of 1 Mbit/s carries the lowest rung: the ladder tries the next one once, finds the queue full, steps down
// at once to the rung that fits the throughput it measured, and tries again only after the estimate is old and the hold
// has grown; it does not oscillate.
func TestLadderSettlesOnAThinPath(t *testing.T) {
	s := newSim(rungsOf(hd1080, 1920, 1080), constant(1e6))
	s.run(60 * time.Second)
	t.Logf("1 Mbit/s, 60 s: %s; changes %v", s.table(), s.changes)
	if s.cur != 0 {
		t.Errorf("at rung %d, want the lowest", s.cur)
	}
	if len(s.changes) > 5 {
		t.Errorf("%d changes in 60 s: %v", len(s.changes), s.changes)
	}
	if s.spent[0] < 45*time.Second || s.spent[2] > 0 {
		t.Errorf("time at the rungs: %s", s.table())
	}
	s2 := newSim(rungsOf(hd1080, 1920, 1080), constant(100e6))
	s2.run(20 * time.Second) // at the top
	s2.capacity = constant(1e6)
	from := len(s2.changes)
	s2.run(2 * time.Second)
	if s2.cur != 0 || len(s2.changes) != from+1 {
		t.Errorf("a cap of 1 Mbit/s met at the top: rung %d, changes %v; want one step to the lowest within 2 s", s2.cur, s2.changes[from:])
	}
}

// A path that carries 3 Mbit/s holds the middle rung: 1.25 times its bitrate fits, the top's does not.
func TestLadderFindsTheMiddleRung(t *testing.T) {
	s := newSim(rungsOf(hd1080, 1920, 1080), constant(3e6))
	s.run(90 * time.Second)
	t.Logf("3 Mbit/s, 90 s: %s; changes %v", s.table(), s.changes)
	if s.cur != 1 {
		t.Errorf("at rung %d, want the middle one", s.cur)
	}
	if len(s.changes) > 7 {
		t.Errorf("%d changes in 90 s: %v", len(s.changes), s.changes)
	}
}

// Loss steps down one rung, and an emergency goes to the lowest; the ladder climbs again only after the hold.
func TestLadderStepsDownOnLoss(t *testing.T) {
	s := newSim(rungsOf(hd1080, 1920, 1080), constant(100e6))
	s.run(20 * time.Second)
	if s.cur != 2 {
		t.Fatalf("not at the top: %v", s.changes)
	}
	s.loss = func(time.Duration) float64 { return 14 }
	from := len(s.changes)
	s.run(2 * time.Second)
	if s.cur >= 2 || len(s.changes) == from {
		t.Errorf("14%% loss for 2 s left the ladder at rung %d: %v", s.cur, s.changes[from:])
	}
	s.loss = func(time.Duration) float64 { return 60 }
	s.run(3 * time.Second)
	if s.cur != 0 {
		t.Errorf("60%% loss for 3 s left the ladder at rung %d: %v", s.cur, s.changes)
	}
	s.loss = func(time.Duration) float64 { return 0 }
	end := s.now.Sub(s.start)
	s.run(40 * time.Second)
	t.Logf("loss: %s; changes %v", s.table(), s.changes)
	if len(s.ups) < 3 || s.ups[2] < end-3*time.Second+upHold {
		t.Errorf("climbed too soon after the loss: ups at %v, loss ended at %v", s.ups, end)
	}
	if s.cur != 2 {
		t.Errorf("did not climb back: rung %d, %v", s.cur, s.changes)
	}
}

// After a path recovers, the ladder climbs back to the top, each step after the hold, and the time at each rung over a
// run with a bad half minute in it is the one this table shows.
func TestLadderClimbsBackAfterRecovery(t *testing.T) {
	cap := func(el time.Duration) float64 {
		if el >= 20*time.Second && el < 50*time.Second {
			return 1e6
		}
		return 100e6
	}
	s := newSim(rungsOf(hd1080, 1920, 1080), cap)
	s.run(150 * time.Second)
	t.Logf("recovery, 150 s (1 Mbit/s from 20 s to 50 s): %s; changes %v", s.table(), s.changes)
	if s.cur != 2 {
		t.Errorf("at rung %d, want the top", s.cur)
	}
	if len(s.changes) > 8 {
		t.Errorf("%d changes in 150 s: %v", len(s.changes), s.changes)
	}
}

// A step up that fails makes the next one wait 4 times as long, up to 120 s, and a step that lasts resets the wait.
func TestLadderBacksOffAfterAFailedStep(t *testing.T) {
	rs := rungsOf(hd1080, 1920, 1080)
	var l ladder
	now := time.Unix(100, 0)
	clear := decision{scale: 1, counted: 1}
	step := func(d decision, cur int) (int, string) {
		now = now.Add(adaptEvery)
		return l.step(now, rs, cur, 2, d)
	}
	cur := 0
	step(clear, cur)
	climbed := func(from time.Time) time.Duration {
		for range 4000 {
			if next, _ := step(clear, cur); next != cur {
				cur = next
				return now.Sub(from)
			}
		}
		t.Fatal("never climbed")
		return 0
	}
	if d := climbed(now); d < upHold || d > upHold+adaptEvery {
		t.Errorf("first step up after %v, want %v", d, upHold)
	}
	// the step fails 1 s later: the queue is full and the path carries 2 Mbit/s, short of the 2.5 the rung needs but
	// within what a rate grows to from there
	now = now.Add(time.Second)
	busy := decision{scale: 0.5, counted: 1, busy: true, est: 2e6, estAt: now}
	if next, _ := step(busy, cur); next != 0 {
		t.Fatalf("a full queue at the rung above its estimate: rung %d", next)
	}
	cur = 0
	// the estimate is gone for the test: only the hold is measured
	from := now
	d := climbed(from)
	if d < holdBackoff*upHold || d > holdBackoff*upHold+adaptEvery {
		t.Errorf("after one failure the step up came after %v, want %v", d, holdBackoff*upHold)
	}
	now = now.Add(time.Second)
	busy.estAt = now
	step(busy, cur)
	cur = 0
	d = climbed(now)
	if d < holdBackoff*holdBackoff*upHold || d > holdBackoff*holdBackoff*upHold+adaptEvery {
		t.Errorf("after two failures the step up came after %v, want %v", d, holdBackoff*holdBackoff*upHold)
	}
	// many failures: the hold stops at 120 s
	for range 6 {
		now = now.Add(time.Second)
		busy.estAt = now
		step(busy, cur)
		cur = 0
		climbed(now)
	}
	now = now.Add(time.Second)
	busy.estAt = now
	step(busy, cur)
	cur = 0
	if d := climbed(now); d > maxBackoff*upHold+adaptEvery {
		t.Errorf("the hold grew to %v, want at most %v", d, maxBackoff*upHold)
	}
}

// A path that is not clear, or a rung whose bitrate the estimate does not cover, holds the ladder where it is.
func TestLadderHoldsUntilTheStepFits(t *testing.T) {
	rs := rungsOf(hd1080, 1920, 1080)
	var l ladder
	now := time.Unix(100, 0)
	l.reset(now)
	at := func(d decision, cur int, el time.Duration) int {
		next, _ := l.step(now.Add(el), rs, cur, 2, d)
		return next
	}
	if got := at(decision{scale: 1, counted: 1}, 0, upHold+time.Second); got != 1 {
		t.Fatalf("clear for the hold: rung %d", got)
	}
	l.reset(now)
	// the estimate, fresh, is 2 Mbit/s: the middle rung needs 1.25 x (1.94 + 0.064) = 2.5
	est := decision{scale: 1, counted: 1, est: 2e6, estAt: now.Add(upHold)}
	if got := at(est, 0, upHold+time.Second); got != 0 {
		t.Errorf("a step to a rung the estimate does not cover: rung %d", got)
	}
	est.est = 2.6e6
	if got := at(est, 0, upHold+time.Second); got != 1 {
		t.Errorf("a step to a rung the estimate covers: rung %d", got)
	}
	l.reset(now)
	est.estAt = now // old: the estimate is no longer believed
	if got := at(est, 0, estimateLife+upHold); got != 1 {
		t.Errorf("an old estimate ruled out the step: rung %d", got)
	}
	l.reset(now)
	if got := at(decision{scale: 0.6, counted: 1}, 0, upHold+time.Second); got != 0 {
		t.Errorf("a step up while the adapter is below the rung's full rate: rung %d", got)
	}
	l.reset(now)
	if got := at(decision{scale: 1, counted: 0}, 0, upHold+time.Second); got != 0 {
		t.Errorf("a step up with no session counted: rung %d", got)
	}
	l.reset(now)
	l.step(now.Add(upHold-time.Second), rs, 0, 2, decision{scale: 1, counted: 1, busy: true, est: 1e9, estAt: now})
	if got := at(decision{scale: 1, counted: 1}, 0, upHold+time.Second); got != 0 {
		t.Errorf("a step up 2 s after the queue was full: rung %d", got)
	}
}

// A processor that cannot keep up with a rung steps the ladder down, at once, and keeps it from the rungs it cannot
// carry; the lowest rung stays whatever it costs.
func TestLadderObeysTheProcessor(t *testing.T) {
	s := newSim(rungsOf(hd1080, 1920, 1080), constant(100e6))
	s.run(20 * time.Second)
	if s.cur != 2 {
		t.Fatalf("not at the top: %v", s.changes)
	}
	s.cpuTop = func(time.Duration) int { return 1 }
	from := len(s.changes)
	s.run(adaptEvery)
	if s.cur != 1 || len(s.changes) != from+1 || !strings.Contains(s.changes[from], "encoder") {
		t.Errorf("rung %d, changes %v", s.cur, s.changes[from:])
	}
	s.run(60 * time.Second)
	if s.cur != 1 || len(s.changes) > from+3 {
		t.Errorf("the ladder came back to a rung the processor cannot carry or oscillated: rung %d, %v", s.cur, s.changes[from:])
	}
	s.cpuTop = func(time.Duration) int { return 0 }
	s.run(adaptEvery)
	s.cpuTop = func(time.Duration) int { return 2 }
	s.run(30 * time.Second)
	t.Logf("processor: %s; changes %v", s.table(), s.changes)
	if s.cur != 2 {
		t.Errorf("did not climb when the processor was free again: rung %d", s.cur)
	}
}

// testPublisher is a publisher of a protected broadcast with one other person watching, and no engine limits.
func testPublisher(t *testing.T, v videoMode) *publisher {
	t.Helper()
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	origin, err := kolomoq.NewOrigin(nil)
	if err != nil {
		t.Fatal(err)
	}
	sender, err := secobj.NewSender(origin, kolomoq.Namespace{"r", "k"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sender.Close() })
	bc, err := kolomedia.NewSecureBroadcast(origin, sender, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bc.Close() })
	var others atomic.Int32
	others.Store(1)
	p := newPublisher(bc, sender, v, &others)
	p.lim = vpx.Limits{}
	t.Cleanup(p.close)
	return p
}

// The publisher measures what a pixel costs to encode on delta frames, and shows the ladder the highest rung whose
// encode time stays under cpuShare of a frame interval.
func TestPublisherGuardsTheProcessor(t *testing.T) {
	old := ladderHeights
	ladderHeights = []int{90, 180}
	t.Cleanup(func() { ladderHeights, fakeEncodeTime = old, nil })
	p := testPublisher(t, videoMode{w: 320, h: 180, fps: 30})
	f, _ := kolovideo.NewFrame(320, 180)
	for _, tc := range []struct {
		name string
		ns   float64 // nanoseconds a pixel
		top  int
	}{
		{"320x180 takes 25 ms, over 60% of 33 ms", 434, 0},
		{"320x180 takes 12 ms", 208, 1},
	} {
		fakeEncodeTime = func(px int) time.Duration { return time.Duration(float64(px) * tc.ns) }
		for range 80 { // the mean takes the new cost over
			p.onVideo(f, time.Now())
		}
		rs, cur, top := p.ladder()
		if len(rs) != 2 || cur != 0 || top != tc.top {
			t.Errorf("%s: %d rungs, at %d, top %d, want 2, 0, %d", tc.name, len(rs), cur, top, tc.top)
		}
	}
	p.setRung(1)
	p.onVideo(f, time.Now().Add(vpx.DefaultKeyframeInterval))
	if rs, cur, _ := p.ladder(); cur != 1 || p.vw != rs[1].w {
		t.Errorf("rung 1 asked: at %d, encoding %dx%d", cur, p.vw, p.vh)
	}
}

// The encoder never gets a picture larger than the camera's, whatever the ceiling and the rung asked for, nor one larger
// than the ceiling when the camera gives more.
func TestPublisherNeverSendsMoreThanTheCamera(t *testing.T) {
	for _, tc := range []struct {
		name         string
		v            videoMode
		cw, ch       int
		wantW, wantH int
	}{
		{"a camera smaller than the ceiling", videoMode{w: 1920, h: 1080, fps: 30}, 320, 180, 320, 180},
		{"a 4:3 camera", videoMode{w: 1920, h: 1080, fps: 30}, 640, 480, 640, 480},
		{"a camera larger than the ceiling", videoMode{w: 640, h: 360, fps: 30}, 1920, 1080, 640, 360},
	} {
		p := testPublisher(t, tc.v)
		f, _ := kolovideo.NewFrame(tc.cw, tc.ch)
		p.onVideo(f, time.Now())
		rs, _, _ := p.ladder()
		p.setRung(len(rs) - 1) // asks for the top, which cannot be more than the camera
		for range 3 {
			p.onVideo(f, time.Now().Add(vpx.DefaultKeyframeInterval))
		}
		p.mu.Lock()
		w, h := p.vw, p.vh
		p.mu.Unlock()
		if w != tc.wantW || h != tc.wantH {
			t.Errorf("%s: encodes %dx%d, want %dx%d", tc.name, w, h, tc.wantW, tc.wantH)
		}
	}
}

// The ladder never sends more than the camera gives, whatever the ceiling.
func TestLadderNeverExceedsTheCamera(t *testing.T) {
	for _, cam := range [][2]int{{1920, 1080}, {1280, 720}, {854, 480}, {640, 360}, {320, 180}, {160, 120}, {1, 1}, {1600, 1200}, {2560, 1440}} {
		v := videoMode{w: 1920, h: 1080, fps: 30}
		rs := rungsOf(v, cam[0], cam[1])
		for _, r := range rs {
			if r.w > cam[0] || r.h > cam[1] || r.w*r.h > v.w*v.h {
				t.Errorf("camera %v: a rung of %dx%d", cam, r.w, r.h)
			}
		}
		if last := rs[len(rs)-1]; last.w > cam[0] || last.h > cam[1] {
			t.Errorf("camera %v: top %dx%d", cam, last.w, last.h)
		}
	}
}

// The microphone's timeline follows the clock: when its frames come slower than 50 a second (a starved thread, a device
// that overran), the timestamps jump forward by the frames lost instead of falling behind without end, which a receiver
// reads as audio that is always late and drops (see audioResync).
func TestAudioTimelineFollowsTheClock(t *testing.T) {
	p := testPublisher(t, testVideo)
	if err := p.setAudio(true); err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, frameSamples)
	for range 20 { // a microphone that delivers a frame every 80 ms, a quarter of the pace
		p.onAudio(pcm)
		time.Sleep(80 * time.Millisecond)
	}
	p.onAudio(pcm)
	p.amu.Lock()
	end := p.abase + p.asamples
	p.amu.Unlock()
	wall, _ := p.clock.At(time.Now(), sampleRate)
	if lag := time.Duration(int64(wall)-int64(end)) * time.Second / sampleRate; lag > 400*time.Millisecond {
		t.Errorf("the timeline is %v behind the clock after 1.6 s of a microphone at a quarter of its pace, want under 400 ms", lag)
	}
}

// The microphone does not wait for a camera frame being encoded.
func TestAudioDoesNotWaitForTheCamera(t *testing.T) {
	p := testPublisher(t, testVideo)
	if err := p.setAudio(true); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock() // onVideo holds it for the whole encode of a frame
	done := make(chan struct{})
	go func() {
		p.onAudio(make([]int16, frameSamples))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("an audio frame waited for the camera's lock")
	}
	p.mu.Unlock()
	<-done
}

// BenchmarkShrink is the time to scale one 1080p camera frame to each rung.
func BenchmarkShrink(b *testing.B) {
	src, _ := kolovideo.NewFrame(1920, 1080)
	paintTexture(src, 1)
	for _, sz := range [][2]int{{1280, 720}, {640, 360}} {
		dst, _ := kolovideo.NewFrame(sz[0], sz[1])
		b.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(b *testing.B) {
			for b.Loop() {
				shrink(dst, src)
			}
		})
	}
}

// A step up that fails while the path carries less than the rung needs, by more than a rate grows without a probe, puts
// the rung out of reach: the next try waits the longest hold, not 4 times the first.
func TestLadderCapsBelowARungOutOfReach(t *testing.T) {
	rs := rungsOf(hd1080, 1920, 1080)
	var l ladder
	now := time.Unix(100, 0)
	l.reset(now)
	step := func(d decision, cur int) int {
		now = now.Add(adaptEvery)
		next, _ := l.step(now, rs, cur, 2, d)
		return next
	}
	clear := decision{scale: 1, counted: 1}
	cur := 0
	for cur == 0 {
		cur = step(clear, cur)
	}
	// 1280x720 needs 2.5 Mbit/s with the audio; the path carries 700 kbit/s, and 1.5 times that is 1.05
	busy := decision{scale: 0.3, counted: 1, busy: true, est: 700e3, estAt: now.Add(adaptEvery)}
	if cur = step(busy, cur); cur != 0 {
		t.Fatalf("rung %d after the failed step", cur)
	}
	from := now
	for cur == 0 && now.Sub(from) < 3*time.Minute {
		cur = step(clear, cur)
	}
	if d := now.Sub(from); d < maxBackoff*upHold || d > maxBackoff*upHold+adaptEvery {
		t.Errorf("the next try came after %v, want %v", d, maxBackoff*upHold)
	}
}

// Each rung keeps its own count of failed tries: the middle rung holding for a whole trial does not clear the count of
// the top one, which keeps failing.
func TestLadderCountsTheFailuresOfEachRung(t *testing.T) {
	rs := rungsOf(hd1080, 1920, 1080)
	var l ladder
	now := time.Unix(100, 0)
	l.reset(now)
	clear := decision{scale: 1, counted: 1}
	cur := 0
	climb := func() time.Duration {
		from := now
		for start := cur; cur == start; {
			now = now.Add(adaptEvery)
			cur, _ = l.step(now, rs, cur, 2, clear)
			if now.Sub(from) > 5*time.Minute {
				t.Fatal("never climbed")
			}
		}
		return now.Sub(from)
	}
	climb()
	now = now.Add(trial) // the middle rung lasts a trial
	climb()
	for range 2 { // the top fails twice, each time within reach, and the middle rung lasts a trial in between
		now = now.Add(time.Second)
		busy := decision{scale: 0.5, counted: 1, busy: true, est: 3.5e6, estAt: now}
		if cur, _ = l.step(now, rs, cur, 2, busy); cur != 1 {
			t.Fatalf("rung %d after a failure at the top", cur)
		}
		now = now.Add(trial)
		if d := climb(); cur != 2 {
			t.Fatalf("rung %d after %v", cur, d)
		}
	}
	now = now.Add(time.Second)
	if cur, _ = l.step(now, rs, cur, 2, decision{scale: 0.5, counted: 1, busy: true, est: 3.5e6, estAt: now}); cur != 1 {
		t.Fatalf("rung %d after the third failure", cur)
	}
	from := now
	climb()
	if d := now.Sub(from); d < holdBackoff*holdBackoff*holdBackoff*upHold && d < maxBackoff*upHold {
		t.Errorf("after three failures of the top the next try came after %v", d)
	}
}

// A step up waits for the key frame the encoder makes every vpx.DefaultKeyframeInterval: the new size starts with that
// one instead of adding its own. A step down does not wait.
func TestPublisherStepsUpOnTheKeyFrame(t *testing.T) {
	old := ladderHeights
	ladderHeights = []int{90, 180}
	t.Cleanup(func() { ladderHeights = old })
	p := testPublisher(t, videoMode{w: 320, h: 180, fps: 30})
	f, _ := kolovideo.NewFrame(320, 180)
	now := time.Now()
	p.onVideo(f, now) // the first frame: a key frame at the lowest rung
	size := func() int {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.vh
	}
	p.setRung(1)
	for el := time.Duration(0); el < vpx.DefaultKeyframeInterval-2*frameEvery(30); el += frameEvery(30) {
		p.onVideo(f, now.Add(el))
		if h := size(); h != 90 {
			t.Fatalf("at %v, before the key frame is due, encoding %dp", el, h)
		}
		if _, cur, _ := p.ladder(); cur != 0 {
			t.Fatalf("at %v the ladder is told rung %d, which is not sent yet", el, cur)
		}
	}
	p.mu.Lock()
	keys := p.nkey
	p.mu.Unlock()
	p.onVideo(f, now.Add(vpx.DefaultKeyframeInterval))
	if h := size(); h != 180 {
		t.Fatalf("encoding %dp when the key frame is due", h)
	}
	p.mu.Lock()
	if p.nkey != keys+1 {
		t.Errorf("%d key frames for the step up, want 1", p.nkey-keys)
	}
	p.mu.Unlock()
	p.setRung(0)
	p.onVideo(f, now.Add(vpx.DefaultKeyframeInterval+frameEvery(30)))
	if h := size(); h != 90 {
		t.Errorf("a step down waits: encoding %dp", h)
	}
}

// A step up is sent with the encoder's next key frame: a step down before that is no failed try of the rung, and the
// trial of the rung begins when it is sent.
func TestLadderTrialBeginsWhenTheStepIsSent(t *testing.T) {
	rs := rungsOf(hd1080, 1920, 1080)
	var l ladder
	now := time.Unix(100, 0)
	l.reset(now)
	clear := decision{scale: 1, counted: 1}
	next := 1
	for next == 1 {
		now = now.Add(adaptEvery)
		next, _ = l.step(now, rs, 1, 2, clear)
	}
	// still encoding rung 1 when the path fills: the step down lowers the request, and rung 2 was never tried
	now = now.Add(adaptEvery)
	if n, _ := l.step(now, rs, 1, 2, decision{scale: 0.5, counted: 1, busy: true, est: 700e3, estAt: now}); n != 0 {
		t.Fatalf("rung %d", n)
	}
	if n := *l.tried(2); n != 0 {
		t.Fatalf("%d failed tries of a rung that was not sent", n)
	}
	// rung 1 is asked for, and sent after 1.5 s; a step down 20.5 s after the decision but 19 s after it was sent is a
	// failed try
	next = 0
	for next == 0 {
		now = now.Add(adaptEvery)
		next, _ = l.step(now, rs, 0, 2, clear)
	}
	for range 6 {
		now = now.Add(adaptEvery)
		l.step(now, rs, 0, 2, clear)
	}
	sent := now
	for now.Sub(sent) < trial-time.Second { // the processor allows no more than rung 1
		now = now.Add(adaptEvery)
		l.step(now, rs, 1, 1, clear)
	}
	if n, _ := l.step(now, rs, 1, 1, decision{scale: 0.5, counted: 1, busy: true, est: 2e6, estAt: now}); n != 0 || *l.tried(1) != 1 {
		t.Fatalf("rung %d, %d failed tries after a step down within the trial", n, *l.tried(1))
	}
}

// The counts of failed tries are for the rungs they were made on: when the rungs change (people join, the camera
// changes) they are forgotten.
func TestLadderForgetsTheCountsOfOtherRungs(t *testing.T) {
	rs := rungsOf(hd1080, 1920, 1080)
	var l ladder
	now := time.Unix(100, 0)
	l.reset(now)
	l.step(now, rs, 0, 2, decision{scale: 1, counted: 1})
	*l.tried(1) = maxTries
	if l.hold(1) != maxBackoff*upHold {
		t.Fatalf("hold %v", l.hold(1))
	}
	now = now.Add(adaptEvery)
	l.step(now, rungsOf(hd1080, 1280, 720), 0, 1, decision{scale: 1, counted: 1})
	if h := l.hold(1); h != upHold {
		t.Errorf("hold %v for the rungs of another camera, want %v", h, upHold)
	}
}
