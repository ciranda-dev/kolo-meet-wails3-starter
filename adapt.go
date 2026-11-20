package main

import (
	"math"
	"time"

	kolomoq "github.com/ciranda-dev/kolo-moq"
)

// What the sender watches and how it answers. The delays are the app's choices: the receivers play a frame up to
// 450 ms after it was made (kolo-media's PlayoutConfig MaxDelay and MaxBehind), so a queue older than that has already
// lost its frames.
const (
	adaptEvery = 250 * time.Millisecond
	// congestedDelay is how long the oldest unsent object may wait before the path counts as full.
	congestedDelay = 250 * time.Millisecond
	// skipDelay is how long it may wait before the camera's frames are no longer encoded, so the queue empties and
	// its length stays bounded.
	skipDelay = time.Second
	// cutEvery spaces the cuts of the bitrate: a queue empties slowly after one, and cutting on every tick would
	// drive the rate to its floor.
	cutEvery = time.Second
	// clearFor is how long the path must stay clear before the bitrate grows again, and growStep how much it grows
	// each adaptEvery: 5% of the full rate a second.
	clearFor = 3 * time.Second
	growStep = 0.0125
	// minScale is the smallest share of a size's full bitrate; at 1280x720 it is about 100 kbit/s.
	minScale = 0.05
	// keepShare is the part of the measured throughput the video takes; the rest is for the audio and for what
	// other streams send.
	keepShare = 0.75
	// stallAfter is how long a guest's session may carry nothing, with media waiting, before the host stops counting it:
	// the camera is then sized for the guests that are receiving, and the one that is not hears the audio when it
	// can. A guest counts again, with a new keyframe for it, when its session carries bytes.
	stallAfter = 5 * time.Second
	// audioShare is what the microphone's stream needs on top of its payload: Opus's constant bitrate plus the
	// headers and encryption of 50 packets a second.
	audioShare = 2 * opusBitrate
)

// pathSample is what the sender sees of its path at one moment: the counters of the sessions that carry its media.
type pathSample struct {
	sent  uint64        // bytes handed to the transport, over all sessions
	delay time.Duration // the oldest unsent object, the worst of the sessions
	drops uint64        // groups reset or expired, over all sessions
}

// decision is what the publisher does until the next sample.
type decision struct {
	scale float64 // the share of the full bitrate of the size, minScale to 1
	key   bool    // start a new group with a key frame now: a group was lost
	skip  bool    // encode no video frame: the queue is too old
	// What the best session showed, for the ladder: its queue is older than congestedDelay, the loss of the tick in
	// percent (the groups it gave up against audioFrames a second), and the throughput measured while its queue was
	// full, at estAt, 0 when it never was. counted is how many sessions the host counts.
	busy    bool
	loss    float64
	est     float64
	estAt   time.Time
	counted int
}

// adapter holds the controller of the video bitrate: the throughput is measured while the path is full, the bitrate
// follows it, and it grows again slowly while the path stays clear.
type adapter struct {
	scale   float64
	bps     float64 // bits a second the path carried, measured while it was full
	bpsAt   time.Time
	last    pathSample
	at      time.Time
	cut     time.Time
	clearAt time.Time
}

func newAdapter() *adapter { return &adapter{scale: 1} }

// step takes a sample made at now and the full bitrate of the size being sent, in bits a second, and says what to do.
func (a *adapter) step(now time.Time, s pathSample, full float64) decision {
	if a.at.IsZero() {
		a.last, a.at, a.clearAt = s, now, now
		return decision{scale: a.scale}
	}
	dt := now.Sub(a.at).Seconds()
	full = max(full, 1)
	rate := float64(s.sent-a.last.sent) * 8 / max(dt, 0.001)
	lost := s.drops > a.last.drops
	busy := s.delay > congestedDelay
	if busy {
		if a.bps == 0 {
			a.bps = rate
		} else {
			a.bps += (rate - a.bps) / 2
		}
		a.bpsAt = now
	}
	loss := min(100, 100*float64(s.drops-a.last.drops)/(audioFrames*max(dt, 0.001)))
	a.last, a.at = s, now
	d := decision{skip: s.delay > skipDelay, key: lost, busy: busy, loss: loss, est: a.bps, estAt: a.bpsAt}
	switch {
	case (busy || lost) && now.Sub(a.cut) >= cutEvery:
		a.cut, a.clearAt = now, now
		next := a.scale * 0.8
		if busy && a.bps > 0 {
			next = min(a.scale, (keepShare*a.bps-audioShare)/full)
		}
		a.scale = max(minScale, next)
	case busy || lost:
		a.clearAt = now
	case now.Sub(a.clearAt) >= clearFor:
		a.scale = min(1, a.scale+growStep)
	}
	d.scale = a.scale
	return d
}

// What the sender takes for loss on its path, to size the Opus forward error correction. Opus's own advice is a loss
// percentage in steps; the bounds are the app's.
const (
	// lossHold is how long the expected loss stays after the last group the path lost.
	lossHold = 10 * time.Second
	// minLoss and maxLoss bound the loss the encoder is told to expect, in percent. Below 5 the redundancy costs more
	// than it recovers, and above 20 it takes the bits the voice needs from a constant 32 kbit/s.
	minLoss = 5
	maxLoss = 20
	// audioFrames is the groups a second the microphone makes: one for each 20 ms frame.
	audioFrames = 50
)

// forceAudioLoss is a test seam: a test sets a loss, in percent, for the encoder whatever the path shows.
var forceAudioLoss int

// lossWatch estimates the loss on the path from the groups the sender's sessions gave up (reset or expired): a frame
// that arrives too late for the receiver counts as lost. It is a floor, not a measure: a packet the transport sends
// again in time is not seen.
type lossWatch struct {
	last  uint64
	at    time.Time
	seen  time.Time
	pct   int
	begun bool
}

// step takes the groups given up so far, at now, and returns the loss to expect, 0 when none was seen for lossHold.
func (l *lossWatch) step(now time.Time, drops uint64) int {
	if !l.begun {
		l.last, l.at, l.begun = drops, now, true
		return 0
	}
	dt := max(now.Sub(l.at).Seconds(), 0.001)
	if d := drops - l.last; d > 0 {
		l.seen = now
		l.pct = min(maxLoss, max(minLoss, int(math.Ceil(100*float64(d)/(audioFrames*dt)))))
	} else if now.Sub(l.seen) > lossHold {
		l.pct = 0
	}
	l.last, l.at = drops, now
	return l.pct
}

// sessionSample is what the sender sees of one of its sessions.
type sessionSample struct {
	id     any
	sample pathSample
	queued bool // media waits in the session's queue
}

// share is the state of one session in the fairness rule: its own controller, and whether it moves.
type share struct {
	a     *adapter
	sent  uint64
	moved time.Time
	out   bool // not counted: it has carried nothing for stallAfter
}

// fair sizes the camera for all the sessions that carry it, the guests of a host. Each session has its own
// controller; the camera follows the best of them, so a guest on a thin or a dead path does not lower the picture of the
// others, and a session that carries nothing for stallAfter is not counted until it does again. It is not safe for
// concurrent use.
type fair struct {
	per map[any]*share
	out int // how many sessions were not counted at the last step, for the log
}

func newFair() *fair { return &fair{per: map[any]*share{}} }

// step takes the samples made at now and the full bitrate of the size being sent, and says what to do. cur is the
// decision to keep while no session counts. It also reports how many sessions are not counted.
func (f *fair) step(now time.Time, ss []sessionSample, full float64, cur decision) (decision, int) {
	var best decision
	counted, out, back := 0, 0, false
	seen := map[any]bool{}
	var keys []decision
	for _, x := range ss {
		seen[x.id] = true
		sh := f.per[x.id]
		if sh == nil {
			sh = &share{a: newAdapter(), sent: x.sample.sent, moved: now}
			f.per[x.id] = sh
		}
		if x.sample.sent-sh.sent >= sendProgress || !x.queued {
			sh.sent, sh.moved = x.sample.sent, now
		}
		stalled := now.Sub(sh.moved) >= stallAfter
		returned := sh.out && !stalled
		if returned {
			sh.a = newAdapter() // what it measured before it stalled says nothing of the path now
		}
		sh.out = stalled
		if stalled {
			out++
			continue
		}
		d := sh.a.step(now, x.sample, full)
		back = back || returned
		keys = append(keys, d)
		counted++
		if counted == 1 || d.scale > best.scale {
			best = d
		}
	}
	for id := range f.per {
		if !seen[id] {
			delete(f.per, id)
		}
	}
	if counted == 0 {
		return cur, out
	}
	best.skip, best.key, best.counted = true, back, counted
	for _, d := range keys {
		best.skip = best.skip && d.skip
		best.key = best.key || (d.key && d.scale >= best.scale/2)
	}
	return best, out
}

// rebase gives every session's controller the full rate of a new rung: its share was of the one before.
func (f *fair) rebase(now time.Time) {
	for _, sh := range f.per {
		sh.a.scale, sh.a.clearAt = 1, now
	}
}

// mediaSessions are the sessions that carry this person's media away: the guest's session to the relay, and for the
// host the relay's session with each guest, where the queue of what it sends builds up.
func (c *call) mediaSessions() []*kolomoq.Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*kolomoq.Session
	if sess := c.session(); c.role == roleGuest && sess != nil {
		out = append(out, sess)
	}
	for _, rp := range c.relaySessions {
		if rp.sess.Err() == nil {
			out = append(out, rp.sess)
		}
	}
	return out
}

// noAdapt is a test seam: a test sets it to measure a call without the adapter, whose camera is sent at the largest
// rung the engine and the receivers allow.
var noAdapt bool

// noFair is a test seam: a test sets it to size the camera from the sum of every session, as one controller, so a
// guest on a thin path lowers it for all.
var noFair bool

// rungChanged is a test seam: it is told each time the ladder of a call changes the rung, with the new rung and the reason.
var rungChanged func(c *call, r rung, why string)

// adaptLoop keeps the publisher's video within what the path carries until the call ends.
func (c *call) adaptLoop() {
	if noAdapt {
		return
	}
	f, lw, lad := newFair(), &lossWatch{}, &ladder{}
	counted := 0
	t := time.NewTicker(adaptEvery)
	defer t.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case now := <-t.C:
			pub := c.publisher()
			if pub == nil {
				continue
			}
			sessions := c.mediaSessions()
			ss := make([]sessionSample, 0, len(sessions))
			for _, sess := range sessions {
				st := sess.Stats()
				ss = append(ss, sessionSample{id: sess, queued: st.QueuedBytes > 0,
					sample: pathSample{sent: st.SentBytes, delay: st.QueueDelay, drops: st.ResetGroups + st.ExpiredGroups}})
			}
			if noFair {
				ss = mergeSamples(ss)
			}
			var drops uint64
			for _, x := range ss {
				drops += x.sample.drops
			}
			pub.setAudioLoss(max(lw.step(now, drops), forceAudioLoss))
			d, out := f.step(now, ss, pub.fullRate(), decision{scale: pub.rateScale(), skip: pub.skipping()})
			if d.scale < pub.rateScale() || d.key || d.skip != pub.skipping() || out != f.out {
				c.logf("path: %d sessions, %d not counted, video at %.0f%% of its rate%s", len(ss), out, d.scale*100, skipNote(d))
			}
			f.out = out
			if counted == 0 && d.counted > 0 {
				lad.reset(now)
			}
			counted = d.counted
			rs, cur, top := pub.ladder()
			if next, why := lad.step(now, rs, cur, top, d); next != cur {
				from := rs[cur]
				pub.setRung(next)
				f.rebase(now)
				d.scale = 1
				c.q.noteRung(why)
				c.logf("video: %dx%d to %dx%d: %s", from.w, from.h, rs[next].w, rs[next].h, why)
				if rungChanged != nil {
					rungChanged(c, rs[next], why)
				}
			}
			pub.setPath(d)
		}
	}
}

// mergeSamples makes one sample of many: the sum of the bytes and drops and the longest delay, as a single controller
// for the whole call sees them.
func mergeSamples(ss []sessionSample) []sessionSample {
	if len(ss) == 0 {
		return ss
	}
	m := sessionSample{id: "all"}
	for _, x := range ss {
		m.sample.sent += x.sample.sent
		m.sample.delay = max(m.sample.delay, x.sample.delay)
		m.sample.drops += x.sample.drops
		m.queued = m.queued || x.queued
	}
	return []sessionSample{m}
}

func skipNote(d decision) string {
	switch {
	case d.skip:
		return ", frames skipped"
	case d.key:
		return ", new key frame"
	}
	return ""
}
