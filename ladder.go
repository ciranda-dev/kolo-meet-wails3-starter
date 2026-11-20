package main

import (
	"fmt"
	"time"
)

// The quality ladder. The camera is sent at one of a few sizes, the rungs; the adapter's bitrate share moves within the
// one in use, and the ladder moves between them. It steps down at once when the path or the processor cannot carry the
// rung, and up slowly, after a hold that grows after a step that did not last. The rules are those of a ladder of
// renditions picked from a delivery estimate: a rung fits when the estimate is 1.25 times its bitrate; a loss of 10%
// steps down, 20% goes to the lowest; a step up waits 4 s, and 4 times as long after each step that failed, up to 120 s.
const (
	// upHold is how long the path must be clear, after a change of rung, before the next rung is tried.
	upHold = 4 * time.Second
	// holdBackoff multiplies the hold after a step up that was followed by a step down within probeWindow, up to
	// maxBackoff times the hold: 120 s. A step up that lasts probeWindow resets the hold.
	holdBackoff = 4
	maxBackoff  = 30
	probeWindow = 10 * time.Second
	// fitFactor is how many times a rung's bitrate, with the audio's share, the delivery estimate must be for the rung
	// to fit.
	fitFactor = 1.25
	// estimateLife is how long the path's estimate, measured while its queue was full, rules out a step up.
	estimateLife = 20 * time.Second
	// lossStep and lossEmergency are the loss, in percent smoothed over lossWindow, that steps down one rung and goes to
	// the lowest. downWalk is the least time between two steps down for loss or an old queue.
	lossStep      = 10.0
	lossEmergency = 20.0
	lossWindow    = time.Second
	downWalk      = 500 * time.Millisecond
	// cpuShare is the part of a frame interval an encode may take for its rung to be chosen; costWarm is the delta
	// frames the cost of a pixel needs before it rules a rung out; costWeight is the weight of each new frame in it.
	cpuShare   = 0.6
	costWarm   = 15
	costWeight = 0.1
)

// ladder is the controller of the rung. It is not safe for concurrent use.
type ladder struct {
	backoff int       // the hold is upHold times this
	changed time.Time // when the rung last changed, or the ladder began
	bad     time.Time // when the path last showed trouble
	upAt    time.Time // when the last step up was, zero after a step down
	walk    time.Time // when the last step down for loss or an old queue was
	at      time.Time // the time of the last step
	loss    float64   // loss in percent, smoothed
}

// reset forgets what was learned of the path: a new path begins.
func (l *ladder) reset(now time.Time) { *l = ladder{backoff: 1, changed: now, bad: now, at: now} }

// step takes what the path shows at now (the decision of the best session), the rungs, the one being sent and the
// highest the processor allows, and returns the rung to send and why it changed, empty when it did not.
func (l *ladder) step(now time.Time, rs []rung, cur, top int, d decision) (int, string) {
	if len(rs) == 0 {
		return cur, ""
	}
	if l.backoff == 0 {
		l.reset(now)
	}
	dt := now.Sub(l.at)
	l.at = now
	l.loss += (d.loss - l.loss) * min(1, dt.Seconds()/lossWindow.Seconds())
	if d.busy || d.skip || d.loss > 0 {
		l.bad = now
	}
	if !l.upAt.IsZero() && now.Sub(l.upAt) >= probeWindow {
		l.backoff, l.upAt = 1, time.Time{}
	}
	next, why := cur, ""
	if cur > top {
		next, why = top, "the encoder cannot keep up"
	}
	lower := func(to int, reason string) {
		if to = max(to, 0); to < next {
			next, why = to, reason
		}
	}
	if d.counted > 0 {
		switch {
		case l.loss >= lossEmergency:
			lower(0, fmt.Sprintf("loss %.0f%%", l.loss))
		case l.loss >= lossStep && now.Sub(l.walk) >= downWalk:
			lower(cur-1, fmt.Sprintf("loss %.0f%%", l.loss))
		}
		if d.busy && d.est > 0 {
			fit := 0
			for i := cur; i > 0; i-- {
				if d.est >= fitFactor*(rs[i].rate+audioShare) {
					fit = i
					break
				}
			}
			lower(fit, fmt.Sprintf("the path carries %.0f kbit/s", d.est/1000))
		}
		if d.skip && now.Sub(l.walk) >= downWalk {
			lower(cur-1, "the queue is over a second old")
		}
	}
	if next < cur {
		if !l.upAt.IsZero() && now.Sub(l.upAt) < probeWindow {
			l.backoff = min(l.backoff*holdBackoff, maxBackoff)
		}
		l.upAt, l.changed, l.walk, l.loss = time.Time{}, now, now, 0
		return next, why
	}
	hold := upHold * time.Duration(l.backoff)
	since := l.changed
	if l.bad.After(since) {
		since = l.bad
	}
	if d.counted == 0 || cur >= top || d.scale < 1 || now.Sub(since) < hold {
		return cur, ""
	}
	if d.est > 0 && now.Sub(d.estAt) < estimateLife && d.est < fitFactor*(rs[cur+1].rate+audioShare) {
		return cur, ""
	}
	l.upAt, l.changed = now, now
	return cur + 1, fmt.Sprintf("the path was clear for %v", hold)
}
