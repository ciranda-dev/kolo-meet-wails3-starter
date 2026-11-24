//go:build finished

package main

import (
	"context"
	"fmt"
	"os"
	"runtime/pprof"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	kolomedia "github.com/ciranda-dev/kolo-media"
	"github.com/ciranda-dev/kolo-meet-wails3-starter/internal/netsim"
	kolomoq "github.com/ciranda-dev/kolo-moq"
	kolosecure "github.com/ciranda-dev/kolo-secure"
	"github.com/ciranda-dev/kolo-video/vpx"
)

// hd is the owner's setting: 1280x720 at 30 frames a second, a key frame every 2 s.
var hd = videoMode{w: 1280, h: 720, fps: 30}

// impairment is one condition of the link between a guest and the call's relay.
type impairment struct {
	name     string
	up, down netsim.Profile
	cutAt    time.Duration // when to cut the link, 0 never
	cutFor   time.Duration
	video    videoMode // the call's video setting; the zero value is hd
	slow     bool      // only measured when KOLO_MEET_SLOW is set: the call comes through without help
	cc       string    // the congestion controller of both people, the default when empty
	// events change the link while the call runs, at a time since the start of the sampling.
	events []linkEvent
}

// linkEvent is a change of the link at a moment of the run.
type linkEvent struct {
	at time.Duration
	do func(*netsim.Link)
}

const (
	kbps = 1000 / 8
	mbps = 1000 * kbps
)

// sample is the count of what was decoded at one end, at a time since the start.
type sample struct {
	at           time.Duration
	audio, video uint64
	conc         uint64 // audio frames made up for lost ones
}

// outcome is what one run measured: the decoded audio frames and pictures at the host (from the guest, over the link
// up) and at the guest (from the host, over the link down).
type outcome struct {
	atHost, atGuest []sample
	up, down        netsim.Stats
	state           string
	heal            time.Duration // when the fault ended (or began, for one that does not end); 0 for none
	left            int           // people who left the call, as the windows were told
	people          [2]int        // people in the call at the end, as the host's and the guest's window see it
	ended           *MeetError    // why the guest's call ended, nil while it runs
}

// rate is the frames a second of one kind between times a and b of the samples.
func rate(s []sample, from, to time.Duration, video bool) float64 {
	var first, last *sample
	for i := range s {
		if s[i].at >= from && first == nil {
			first = &s[i]
		}
		if s[i].at <= to {
			last = &s[i]
		}
	}
	if first == nil || last == nil || last.at <= first.at {
		return 0
	}
	d, c := last.audio-first.audio, last.at-first.at
	if video {
		d = last.video - first.video
	}
	return float64(d) / c.Seconds()
}

// firstStall is when the counter of a kind stopped for at least gap, after it had counted something; -1 if it never did.
func firstStall(s []sample, gap time.Duration, video bool) time.Duration {
	started := false
	var lastChange time.Duration
	var prev uint64
	for _, x := range s {
		v := x.audio
		if video {
			v = x.video
		}
		if v > 0 && !started {
			started, lastChange, prev = true, x.at, v
			continue
		}
		if !started {
			continue
		}
		if v != prev {
			prev, lastChange = v, x.at
		} else if x.at-lastChange >= gap {
			return lastChange
		}
	}
	return -1
}

// recoverAfter is how long after from the counter of a kind first carries a second of media again (40 audio frames or
// 10 pictures within one second); -1 if it never does.
func recoverAfter(s []sample, from time.Duration, video bool) time.Duration {
	val := func(x sample) uint64 {
		if video {
			return x.video
		}
		return x.audio
	}
	want := uint64(40)
	if video {
		want = 10
	}
	for i, x := range s {
		if x.at < from {
			continue
		}
		for _, y := range s[i:] {
			if y.at-x.at >= time.Second {
				if val(y)-val(x) >= want {
					return x.at - from
				}
				break
			}
		}
	}
	return -1
}

func fmtRecover(d time.Duration) string {
	if d < 0 {
		return "never"
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// callThrough runs a call between two in-process peers with the app's whole media path: textured 1280x720 camera at 30
// fps, 48 kHz microphone, VP8 and Opus, protected broadcasts, the call's relay on the host and the guest's connection to
// it through a netsim link. It samples the decoded counters every 50 ms for dur.
func callThrough(t *testing.T, im impairment, dur time.Duration) outcome {
	t.Helper()
	first := freeUDP(t)
	vid := im.video
	if vid == (videoMode{}) {
		vid = hd
	}
	mk := func(i int, cfg Config) *peer {
		hw := newFakeHW(parties[i].luma, parties[i].freq)
		hw.texture = true
		p := startPeer(t, parties[i].name, cfg, hw, func(s *MeetService) { s.vid = vid })
		p.rec.waitNetwork(t, netReady)
		return p
	}
	host, guest := mk(0, Config{First: first, Congestion: im.cc}), mk(1, Config{Bootstrap: first, Congestion: im.cc})
	host.turnOn(t)
	if _, err := host.CreateCall(); err != nil {
		t.Fatal(err)
	}
	c := host.rec.waitCall(t, "had an invite", func(c Call) bool { return c.Invite != "" && c.State == callActive })
	hc := host.activeCall()
	link, err := netsim.New(hc.ln.Addr().String(), im.up, im.down)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { link.Close() })
	relayHook = func(c *call, conn kolomoq.Conn) kolomoq.Conn {
		conn.Close()
		tc := transportConfig()
		tc.CongestionControl = c.svc.settingsCopy().Congestion
		d, err := kolosecure.Dial(c.ctx, link.Addr(), c.svc.id, hc.relID, &kolosecure.Config{Transport: tc})
		if err != nil {
			t.Logf("dial through the link: %v", err) // a reconnection during an outage fails, and tries again
			return conn
		}
		return d
	}
	t.Cleanup(func() { relayHook = nil })
	guest.turnOn(t)
	if _, err := guest.JoinCall(c.Invite); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*peer{host, guest} {
		p.rec.waitCall(t, "had everyone live", func(c Call) bool {
			if c.State != callActive || len(c.Participants) != 2 {
				return false
			}
			for _, x := range c.Participants {
				if x.State != partLive {
					return false
				}
			}
			return true
		})
	}
	hid, gid := idOf(t, host), idOf(t, guest)
	remote := func(p *peer, id string) *participant {
		cl := p.activeCall()
		cl.mu.Lock()
		defer cl.mu.Unlock()
		return cl.people[id]
	}
	fromGuest, fromHost := remote(host, gid), remote(guest, hid)
	var out outcome
	start := time.Now()
	cut := false
	events := slices.Clone(im.events)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for now := range tick.C {
		el := now.Sub(start)
		if im.cutAt > 0 && !cut && el >= im.cutAt {
			cut = true
			link.Cut(im.cutFor)
		}
		for i := range events {
			if e := &events[i]; e.do != nil && el >= e.at {
				e.do(link)
				e.do = nil
			}
		}
		out.atHost = append(out.atHost, sample{el, fromGuest.audioIn.Load(), fromGuest.videoIn.Load(), fromGuest.concealed.Load()})
		out.atGuest = append(out.atGuest, sample{el, fromHost.audioIn.Load(), fromHost.videoIn.Load(), fromHost.concealed.Load()})
		if el >= dur {
			break
		}
	}
	if f := os.Getenv("KOLO_MEET_IMPAIR_DUMP"); f != "" {
		if w, err := os.Create(f); err == nil {
			pprof.Lookup("goroutine").WriteTo(w, 2)
			w.Close()
		}
	}
	if im.cutAt > 0 {
		out.heal = im.cutAt + im.cutFor
	}
	for _, e := range im.events {
		out.heal = max(out.heal, e.at)
	}
	out.up, out.down = link.Up(), link.Down()
	if cl, ok := guest.rec.lastCall(); ok {
		out.state = describe(cl)
		out.people[1], out.ended = len(cl.Participants), cl.Error
	}
	if cl, ok := host.rec.lastCall(); ok {
		out.people[0] = len(cl.Participants)
	}
	for _, p := range []*peer{host, guest} {
		p.rec.mu.Lock()
		out.left += len(p.rec.left)
		p.rec.mu.Unlock()
	}
	return out
}

func (o outcome) report(name string, dur time.Duration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-34s", name)
	for _, who := range []struct {
		n string
		s []sample
	}{{"host<-guest", o.atHost}, {"guest<-host", o.atGuest}} {
		stall := func(video bool) string {
			if d := firstStall(who.s, 2*time.Second, video); d >= 0 {
				return fmt.Sprintf("%.1fs", d.Seconds())
			}
			return "never"
		}
		fmt.Fprintf(&b, " | %s a/s %5.1f v/s %5.1f last5s a %5.1f v %5.1f stall a:%s v:%s", who.n,
			rate(who.s, 0, dur, false), rate(who.s, 0, dur, true), rate(who.s, dur-5*time.Second, dur, false), rate(who.s, dur-5*time.Second, dur, true), stall(false), stall(true))
	}
	if o.heal > 0 {
		fmt.Fprintf(&b, " | recover after %.0fs: host<-guest a %s v %s, guest<-host a %s v %s", o.heal.Seconds(),
			fmtRecover(recoverAfter(o.atHost, o.heal, false)), fmtRecover(recoverAfter(o.atHost, o.heal, true)),
			fmtRecover(recoverAfter(o.atGuest, o.heal, false)), fmtRecover(recoverAfter(o.atGuest, o.heal, true)))
	}
	fmt.Fprintf(&b, " | up pk %d drop mtu %d loss %d q %d maxq %v | down pk %d drop mtu %d loss %d q %d", o.up.Packets, o.up.DroppedMTU, o.up.DroppedLoss, o.up.DroppedQ,
		o.up.MaxQueueDelay.Round(time.Millisecond), o.down.Packets, o.down.DroppedMTU, o.down.DroppedLoss, o.down.DroppedQ)
	return b.String()
}

// conditions are the paths of the owner's report: a mobile uplink and a Wi-Fi one, with delay, jitter and loss.
func conditions() []impairment {
	mobile := func(rate int, loss float64, mtu int) (netsim.Profile, netsim.Profile) {
		return netsim.Profile{Rate: rate, Delay: 60 * time.Millisecond, Jitter: 20 * time.Millisecond, Loss: loss, MTU: mtu, Seed: 1},
			netsim.Profile{Rate: 4 * rate, Delay: 60 * time.Millisecond, Jitter: 20 * time.Millisecond, Loss: loss, MTU: mtu, Seed: 2}
	}
	var out []impairment
	for _, c := range []struct {
		name string
		rate int
		loss float64
		mtu  int
	}{
		{"clean loopback", 0, 0, 0},
		{"3 Mbps 60ms loss 1%", 3 * mbps, 0.01, 0},
		{"3 Mbps 60ms loss 3%", 3 * mbps, 0.03, 0},
		{"1 Mbps 60ms loss 1%", 1 * mbps, 0.01, 0},
		{"1 Mbps 60ms loss 3%", 1 * mbps, 0.03, 0},
		{"3 Mbps 60ms loss 1% mtu 1350", 3 * mbps, 0.01, 1350},
		{"3 Mbps 60ms loss 1% mtu 1280", 3 * mbps, 0.01, 1280},
	} {
		up, down := mobile(c.rate, c.loss, c.mtu)
		out = append(out, impairment{name: c.name, up: up, down: down})
	}
	up, down := mobile(3*mbps, 0.01, 0)
	out = append(out, impairment{name: "3 Mbps loss 1% outage 3s at 8s", up: up, down: down, cutAt: 8 * time.Second, cutFor: 3 * time.Second})
	return out
}

// TestImpairedReport measures a call over each condition and prints the numbers; set KOLO_MEET_IMPAIR=<seconds>.
func TestImpairedReport(t *testing.T) {
	v := os.Getenv("KOLO_MEET_IMPAIR")
	if v == "" {
		t.Skip("set KOLO_MEET_IMPAIR=<seconds> to measure")
	}
	dur, err := time.ParseDuration(v + "s")
	if err != nil {
		t.Fatal(err)
	}
	only := os.Getenv("KOLO_MEET_IMPAIR_ONLY")
	noAdapt = os.Getenv("KOLO_MEET_IMPAIR_BASELINE") != ""
	t.Cleanup(func() { noAdapt = false })
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Logf("engine %v, real-time limits %+v", vpx.Engine(), vpx.Capabilities())
	for _, im := range conditions() {
		if only != "" && !strings.Contains(im.name, only) {
			continue
		}
		t.Run(im.name, func(t *testing.T) {
			o := callThrough(t, im, dur)
			t.Log("\n" + o.report(im.name, dur) + "\nstate: " + o.state)
		})
	}
}

var _ = context.Background

// relayedPath is the path of the field test of 2026-11-20 between two countries through a connection relay: 470 ms
// round trip, up to 40 ms more each way, 1 Mbit/s in each direction and a queue of a second, since a relayed stream
// turns a lost packet into delay rather than loss.
func relayedPath() (up, down netsim.Profile) {
	p := netsim.Profile{Rate: 1 * mbps, Queue: 1 * mbps, Delay: 235 * time.Millisecond, Jitter: 40 * time.Millisecond, Seed: 1}
	q := p
	q.Seed = 2
	return p, q
}

// pathTick is one tick of a sender's adapter: what it decided, the height it encodes and the key frames written so far.
type pathTick struct {
	at   time.Duration
	d    decision
	h    int
	keys uint64
}

// sideTrace is what one person's sender did over a run, and the playout delay of the person who received it.
type sideTrace struct {
	ticks   []pathTick
	changes []ladderRung
	delays  []time.Duration
}

// pathRecorder follows, through the test seams, the adapter, the ladder and the receiving playout of both people of a
// call, by the role of the sender.
type pathRecorder struct {
	mu    sync.Mutex
	begin time.Time
	sides map[string]*sideTrace
}

func recordPaths(t *testing.T) *pathRecorder {
	r := &pathRecorder{begin: time.Now(), sides: map[string]*sideTrace{roleHost: {}, roleGuest: {}}}
	pathDecided = func(c *call, d decision) {
		h, keys := 0, uint64(0)
		if pub := c.publisher(); pub != nil {
			pub.mu.Lock()
			h, keys = pub.vh, pub.nkey
			pub.mu.Unlock()
		}
		r.mu.Lock()
		s := r.sides[c.role]
		s.ticks = append(s.ticks, pathTick{time.Since(r.begin), d, h, keys})
		r.mu.Unlock()
	}
	rungChanged = func(c *call, g rung, why string) {
		r.mu.Lock()
		s := r.sides[c.role]
		s.changes = append(s.changes, ladderRung{time.Since(r.begin), g.h, why})
		r.mu.Unlock()
	}
	watchSampled = func(c *call, p *participant, st kolomedia.WatchStats) {
		from := roleHost
		if c.role == roleHost {
			from = roleGuest
		}
		r.mu.Lock()
		s := r.sides[from]
		s.delays = append(s.delays, st.Playout.Delay)
		r.mu.Unlock()
	}
	t.Cleanup(func() { pathDecided, rungChanged, watchSampled = nil, nil, nil })
	return r
}

// firstEstimate is the first estimate of the path a sender's adapter made after its sessions counted, and when.
func (s *sideTrace) firstEstimate() (float64, time.Duration) {
	for _, x := range s.ticks {
		if x.d.est > 0 {
			return x.d.est, x.at
		}
	}
	return 0, -1
}

// flaps counts the steps up that were followed by a step down within trial.
func (s *sideTrace) flaps() (n int) {
	for i := 1; i < len(s.changes); i++ {
		if s.changes[i].h < s.changes[i-1].h && s.changes[i].at-s.changes[i-1].at < trial && (i == 1 || s.changes[i-1].h > s.changes[i-2].h) {
			n++
		}
	}
	return n
}

// keysPerMinute is the key frames a sender wrote a minute, from its first tick with media to its last.
func (s *sideTrace) keysPerMinute() float64 {
	first, last := -1, len(s.ticks)-1
	for i, x := range s.ticks {
		if x.keys > 0 {
			first = i
			break
		}
	}
	if first < 0 || last <= first {
		return 0
	}
	d := s.ticks[last].at - s.ticks[first].at
	return float64(s.ticks[last].keys-s.ticks[first].keys) / d.Minutes()
}

// atCap is the share of the playout's samples at or over cap.
func (s *sideTrace) atCap(cap time.Duration) float64 {
	if len(s.delays) == 0 {
		return 0
	}
	n := 0
	for _, d := range s.delays {
		if d >= cap {
			n++
		}
	}
	return float64(n) / float64(len(s.delays))
}

// slowWindows counts the windows of 5 s, from from to the end, in which fewer than 15 pictures a second were decoded.
func slowWindows(s []sample, from, to time.Duration) (n, of int) {
	for a := from; a+5*time.Second <= to; a += 5 * time.Second {
		of++
		if rate(s, a, a+5*time.Second, true) < 15 {
			n++
		}
	}
	return n, of
}

// report is the timeline of one sender, a line every 5 s, for the log.
func (s *sideTrace) report(name string, recv []sample, dur time.Duration) string {
	var b strings.Builder
	est, at := s.firstEstimate()
	maxDelay := time.Duration(0)
	for _, d := range s.delays {
		maxDelay = max(maxDelay, d)
	}
	n, of := slowWindows(recv, settle, dur)
	fmt.Fprintf(&b, "%s: first estimate %.0f kbit/s at %.1fs, %d rung changes, %d flaps, %.1f key frames a minute, playout delay max %v and at 400 ms or more %.0f%% of the time, %d of %d windows of 5 s under 15 pictures a second\n",
		name, est/1000, at.Seconds(), len(s.changes), s.flaps(), s.keysPerMinute(), maxDelay, 100*s.atCap(400*time.Millisecond), n, of)
	next := time.Duration(0)
	for _, x := range s.ticks {
		if x.at < next {
			continue
		}
		next = x.at + 5*time.Second
		fmt.Fprintf(&b, "  %5.1fs est %6.0f kbit/s scale %3.0f%% busy %-5v %4dp keys %d\n", x.at.Seconds(), x.d.est/1000, x.d.scale*100, x.d.busy, x.h, x.keys)
	}
	for _, c := range s.changes {
		fmt.Fprintf(&b, "  %5.1fs -> %dp (%s)\n", c.at.Seconds(), c.h, c.why)
	}
	return b.String()
}

// TestRelayedPathReport measures a call over the relayed path of the field test and prints, for each sender, the
// estimate of the path over time, the rung changes, the key frames a minute, the playout delay and the frame rate, with
// the owner's 1280x720 setting; set KOLO_MEET_RELAYED=<seconds>, KOLO_MEET_RELAYED_CC=reno,bbr to compare controllers and
// KOLO_MEET_RELAYED_SMALL=1 for the small pictures of the ladder tests.
func TestRelayedPathReport(t *testing.T) {
	v := os.Getenv("KOLO_MEET_RELAYED")
	if v == "" {
		t.Skip("set KOLO_MEET_RELAYED=<seconds> to measure")
	}
	dur, err := time.ParseDuration(v + "s")
	if err != nil {
		t.Fatal(err)
	}
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	ccs := splitList(os.Getenv("KOLO_MEET_RELAYED_CC"))
	if len(ccs) == 0 {
		ccs = []string{""}
	}
	for _, cc := range ccs {
		t.Run("cc="+cc, func(t *testing.T) {
			up, down := relayedPath()
			r := recordPaths(t)
			vid := hd
			if os.Getenv("KOLO_MEET_RELAYED_SMALL") != "" {
				vid = ladderVideo(t)
			}
			o := callThrough(t, impairment{name: "relayed 470ms", up: up, down: down, cc: cc, video: vid}, dur)
			r.mu.Lock()
			defer r.mu.Unlock()
			t.Log("\n" + o.report("relayed 470ms", dur) + "\n" + r.sides[roleHost].report("host", o.atGuest, dur) + r.sides[roleGuest].report("guest", o.atHost, dur))
		})
	}
}

// settle is how long a call has to find its rate before a test counts what it carries.
const settle = 6 * time.Second

// A call over a mobile path keeps going: with 1 Mbit/s up, 60 ms and 3% loss, and again with a 3 s outage, the audio
// of each side plays all the time and the video does not stop. The link's loss and jitter are seeded, so a run drops
// the same packets of the same sequence.
func TestCallOverAnImpairedLink(t *testing.T) {
	if testing.Short() {
		t.Skip("takes 20 s a path")
	}
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	seams(t)
	noAdapt = noAdapt || os.Getenv("KOLO_MEET_IMPAIR_BASELINE") != "" // to see the test fail without the adapter
	conds := conditions()
	pick := func(name string) impairment {
		for _, c := range conds {
			if c.name == name {
				return c
			}
		}
		t.Fatalf("no condition %q", name)
		return impairment{}
	}
	// A smaller picture at the same 1.9 Mbit/s keeps the encoders light enough for the race detector.
	small := smallVideo
	for _, tc := range []struct {
		im  impairment
		dur time.Duration
	}{
		{pick("1 Mbps 60ms loss 3%"), 16 * time.Second},
		{pick("3 Mbps loss 1% outage 3s at 8s"), 22 * time.Second},
	} {
		tc.im.video = small
		t.Run(tc.im.name, func(t *testing.T) {
			o := callThrough(t, tc.im, tc.dur)
			t.Log("\n" + o.report(tc.im.name, tc.dur))
			for _, who := range []struct {
				name string
				s    []sample
			}{{"host from guest", o.atHost}, {"guest from host", o.atGuest}} {
				from, to := tc.dur-settle, tc.dur
				if a := rate(who.s, from, to, false); a < 40 {
					t.Errorf("%s: %.1f audio frames a second in the last %v, want 40 of 50", who.name, a, settle)
				}
				if v := rate(who.s, from, to, true); v < 10 {
					t.Errorf("%s: %.1f pictures a second in the last %v, want 10 of 30", who.name, v, settle)
				}
				if d := firstStall(who.s, 4*time.Second, false); d >= 0 && tc.im.cutAt == 0 {
					t.Errorf("%s: the audio stopped for 4 s at %v", who.name, d)
				}
			}
		})
	}
}

// Over the relayed path of the field test the camera tries the rung the path cannot carry once and stays below it,
// instead of stepping up and down again after 4, 16 and 64 s, and the pictures keep coming at both ends.
func TestRelayedPathSettles(t *testing.T) {
	if testing.Short() {
		t.Skip("takes 50 s")
	}
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	const dur = 50 * time.Second
	up, down := relayedPath()
	r := recordPaths(t)
	o := callThrough(t, impairment{name: "relayed 470ms", up: up, down: down, video: ladderVideo(t)}, dur)
	r.mu.Lock()
	defer r.mu.Unlock()
	t.Log("\n" + o.report("relayed 470ms", dur) + "\n" + r.sides[roleHost].report("host", o.atGuest, dur) + r.sides[roleGuest].report("guest", o.atHost, dur))
	for _, who := range []struct {
		role string
		recv []sample
	}{{roleHost, o.atGuest}, {roleGuest, o.atHost}} {
		s := r.sides[who.role]
		if n := s.flaps(); n > 1 {
			t.Errorf("%s: %d steps up that failed within %v, want at most 1", who.role, n, trial)
		}
		if v := rate(who.recv, dur-2*settle, dur, true); v < 10 {
			t.Errorf("%s: %.1f pictures a second at the end, want 10 of 30", who.role, v)
		}
	}
}
