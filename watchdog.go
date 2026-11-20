package main

import (
	"time"
)

// The no-data watchdog. The transport notices a path that is gone (it times out) and a connection that moves, but not a
// path that carries small datagrams and drops large ones, nor one whose far end changed its port while ours did not
// follow: the connection stays up and nothing arrives. The call looks at the media instead.
const (
	// noDataAfter is how long a person who sends media, as its catalog says, may deliver none before the path to the
	// relay counts as dead and the call makes a new session. Each reset that did not bring media back doubles the
	// wait, up to noDataMaxDoublings times, so a sender that is itself stuck does not make the others reconnect
	// every few seconds.
	noDataAfter        = 6 * time.Second
	noDataMaxDoublings = 3
	// watchdogEvery is how often the counters are read.
	watchdogEvery = 500 * time.Millisecond
	// sendProgress is the bytes the session must carry within noDataAfter while its queue is old, or the path does
	// not carry what we send: 2 KiB is 20 audio packets.
	sendProgress = 2048
)

// heard is what a call knows of one person at one moment: what the catalog says they send, and the frames that reached
// the playout so far, played or not: a frame that arrived is the path working, whatever the speaker does with it.
type heard struct {
	id               string
	audioOn, videoOn bool
	audioIn, videoIn uint64
}

// quiet follows one kind of media from one person.
type quiet struct {
	count uint64
	since time.Time // when the count last changed, or the person began to be expected
}

// watchdog decides when no media arrives: every person who sends a kind is silent for it, or what we send does not
// leave. It is not safe for concurrent use.
type watchdog struct {
	after     time.Duration
	audio     map[string]*quiet
	video     map[string]*quiet
	strikes   int
	sentBase  uint64
	sentSince time.Time
}

func newWatchdog() *watchdog {
	return &watchdog{after: noDataAfter, audio: map[string]*quiet{}, video: map[string]*quiet{}}
}

func (w *watchdog) threshold() time.Duration { return w.after << min(w.strikes, noDataMaxDoublings) }

// follow updates the quiet of one kind and reports whether somebody is expected, whether anybody has been heard
// since the last look, and whether every expected person has been silent for the threshold.
func (w *watchdog) follow(m map[string]*quiet, now time.Time, id string, on bool, count uint64) (expected, heard, silent bool) {
	q := m[id]
	if !on {
		delete(m, id)
		return false, false, true
	}
	if q == nil {
		m[id] = &quiet{count: count, since: now}
		return true, false, false
	}
	if count != q.count {
		q.count, q.since = count, now
		return true, true, false
	}
	return true, false, now.Sub(q.since) >= w.threshold()
}

// receiving takes what is heard at now and reports that a kind of media is dead: it is expected from at least one
// person and none delivered any for the threshold.
func (w *watchdog) receiving(now time.Time, people []heard) bool {
	seen := map[string]bool{}
	var aExp, vExp, aSilent, vSilent = 0, 0, 0, 0
	var anyHeard bool
	for _, p := range people {
		seen[p.id] = true
		e, h, s := w.follow(w.audio, now, p.id, p.audioOn, p.audioIn)
		if e {
			aExp++
			if s {
				aSilent++
			}
		}
		anyHeard = anyHeard || h
		e, h, s = w.follow(w.video, now, p.id, p.videoOn, p.videoIn)
		if e {
			vExp++
			if s {
				vSilent++
			}
		}
		anyHeard = anyHeard || h
	}
	for _, m := range []map[string]*quiet{w.audio, w.video} {
		for id := range m {
			if !seen[id] {
				delete(m, id)
			}
		}
	}
	if anyHeard {
		w.strikes = 0
	}
	return (aExp > 0 && aSilent == aExp) || (vExp > 0 && vSilent == vExp)
}

// sending takes the bytes the transport took from the session so far and whether its queue is old, and reports that
// what we send does not leave: the queue stayed old for the threshold while less than sendProgress bytes went.
func (w *watchdog) sending(now time.Time, sent uint64, queueOld bool) bool {
	if !queueOld || sent-w.sentBase >= sendProgress {
		w.sentBase, w.sentSince = sent, time.Time{}
		return false
	}
	if w.sentSince.IsZero() {
		w.sentSince = now
	}
	return now.Sub(w.sentSince) >= w.threshold()
}

// fired starts the grace after a reset: nothing counts as silent for another threshold, doubled if this was not the first.
func (w *watchdog) fired(now time.Time) {
	w.strikes++
	for _, m := range []map[string]*quiet{w.audio, w.video} {
		for _, q := range m {
			q.since = now
		}
	}
	w.sentSince, w.sentBase = time.Time{}, 0
}

// restart forgets what was seen, keeping the strikes: the next look begins a new wait.
func (w *watchdog) restart() {
	clear(w.audio)
	clear(w.video)
	w.sentSince, w.sentBase = time.Time{}, 0
}

// heardNow lists the people whose media should be arriving: those live in the call.
func (c *call) heardNow() []heard {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]heard, 0, len(c.people))
	for _, id := range c.order {
		if p := c.people[id]; p != nil && p.state == partLive {
			out = append(out, heard{id: id, audioOn: p.audioOn, videoOn: p.videoOn, audioIn: p.audioRecv.Load(), videoIn: p.videoRecv.Load()})
		}
	}
	return out
}

// noDataOff is a test seam: a test sets it to see a call without the watchdog.
var noDataOff bool

// watchdogLoop asks for a new session to the relay when the media stops arriving or leaving, until the call ends. Only a
// guest does: the host's own session is on this computer, and a guest's path is the guest's to mend.
func (c *call) watchdogLoop() {
	if noDataOff || c.role != roleGuest {
		return
	}
	w := newWatchdog()
	t := time.NewTicker(watchdogEvery)
	defer t.Stop()
	gen := c.session()
	for {
		select {
		case <-c.ctx.Done():
			return
		case now := <-t.C:
			if cur := c.session(); cur != gen { // a new session gets a whole threshold to bring media
				gen = cur
				w.restart()
			}
			dead := w.receiving(now, c.heardNow())
			why := "no media from anyone who sends it"
			if !dead {
				var sent uint64
				var old bool
				for _, s := range c.mediaSessions() {
					st := s.Stats()
					sent += st.SentBytes
					old = old || st.QueueDelay > congestedDelay
				}
				dead, why = w.sending(now, sent, old), "what we send does not leave"
			}
			if dead {
				c.logf("watchdog: %s for %v, making a new session to the relay", why, w.threshold())
				w.fired(now)
				c.q.noteWatchdog()
				c.resetRelay(errNoData)
			}
		}
	}
}
