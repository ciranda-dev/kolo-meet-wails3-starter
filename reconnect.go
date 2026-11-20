package main

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	kolomoq "github.com/ciranda-dev/kolo-moq"
	kolosecure "github.com/ciranda-dev/kolo-secure"
	kolotransport "github.com/ciranda-dev/kolo-transport"
)

// How a call comes back from a lost session to its relay. They are the app's choices, not a library's.
const (
	// reconnectMin and reconnectMax bound the wait between two attempts: it doubles from the first up to the second,
	// with up to a quarter more or less so that guests of one call do not all knock at once.
	reconnectMin = 500 * time.Millisecond
	reconnectMax = 4 * time.Second
	// attemptWait is the most one attempt waits for the swarm to hand over a new connection to the relay.
	attemptWait = 15 * time.Second
	// rosterSettle is how long after a reconnection the relay's list of people is read before the people it does not
	// name are removed.
	rosterSettle = 5 * time.Second
	// sessionEndWait is how long a failed read waits for its session to say it ended, before it counts as a refusal.
	sessionEndWait = time.Second
)

// reconnectWindow is how long a call tries to get its session to the relay back before it ends. The host's relay holds
// the place of a guest whose connection was lost for the same time (kolomoq.RelayConfig.Resume). A variable so a test
// can shorten it.
var reconnectWindow = 60 * time.Second

// noReconnect is a test seam: a test sets it to see a call that ends when its session to the relay is lost.
var noReconnect bool

// errNoData is why a session is replaced when the watchdogs found its path dead.
var errNoData = errors.New("kolo-meet: no media on the path to the relay")

// backoff is the wait before attempt n (0 is the first retry): reconnectMin doubled n times up to reconnectMax, with
// jitter, a number from 0 to 1, moving it by up to a quarter either way.
func backoff(n int, jitter float64) time.Duration {
	d := reconnectMax
	if n < 6 {
		d = min(reconnectMin<<n, reconnectMax)
	}
	return time.Duration(float64(d) * (0.75 + 0.5*jitter))
}

// resumable reports whether the end of a session to the relay is a lost connection, which a reconnection mends, and
// not a close by the relay (the host left or removed us) or a failure of ours.
func (c *call) resumable(err error) bool {
	var se *kolomoq.SessionError
	switch {
	case noReconnect:
		return false
	case c.role == roleHost:
		return true // the relay runs in this app: only this call's own teardown ends its session for good
	case errors.As(err, &se):
		return se.Code == kolomoq.CodeSuspend
	}
	return errors.Is(err, kolotransport.ErrTimeout) || errors.Is(err, kolotransport.ErrTransport)
}

// resetRelay asks for a new session to the relay because the path carries nothing: the supervisor suspends the old
// one, so the relay keeps our place, and connects again. Only the first request waiting counts.
func (c *call) resetRelay(why error) {
	select {
	case c.relayReset <- why:
	default:
	}
}

// superviseRelay keeps the call's session to the relay: when it ends it makes a new one, unless the relay closed it,
// which is the host leaving or removing us, and the call ends then.
func (c *call) superviseRelay() {
	for {
		sess := c.session()
		var cause error
		select {
		case <-c.ctx.Done():
			return
		case <-sess.Done():
			cause = sess.Err()
			if !c.resumable(cause) {
				c.relayGone(cause)
				return
			}
		case cause = <-c.relayReset:
			sess.Suspend()
		}
		if c.ctx.Err() != nil || !c.reconnect(cause) {
			return
		}
	}
}

// reconnect makes a new session to the relay, trying with growing waits for up to reconnectWindow. It reports false
// when the call ended, the window included.
func (c *call) reconnect(cause error) bool {
	c.logf("relay: session lost (%v), reconnecting for up to %v", cause, reconnectWindow)
	started := time.Now()
	for attempt := 0; ; attempt++ {
		if attempt > 0 && !sleep(c.ctx, backoff(attempt-1, rand.Float64())) {
			return false
		}
		left := reconnectWindow - time.Since(started)
		if left <= 0 {
			c.end(meetErr("call-ended-lost", cause, ""))
			return false
		}
		conn, err := c.nextRelayConn(min(left, attemptWait))
		if err == nil {
			if err = c.attach(conn); err != nil {
				conn.Close()
			}
		}
		if err != nil {
			if c.ctx.Err() != nil {
				return false
			}
			c.logf("relay: attempt %d: %v", attempt+1, err)
			var me *MeetError
			if errors.As(err, &me) && me.Reason == "rejoin-refused" { // the host turned this key away
				c.end(meetErr("call-ended-removed", err, ""))
				return false
			}
			continue
		}
		c.logf("relay: session back after %v (%d attempts)", time.Since(started).Round(10*time.Millisecond), attempt+1)
		c.q.noteReconnect()
		c.resumed()
		return true
	}
}

// nextRelayConn gets a connection to the relay: the host dials its own relay again, a guest takes the connection the
// swarm makes to it, waiting up to d. The old connection is closed first, so the swarm makes a new one.
func (c *call) nextRelayConn(d time.Duration) (kolomoq.Conn, error) {
	if c.role == roleHost {
		ctx, cancel := context.WithTimeout(c.ctx, d)
		defer cancel()
		return kolosecure.Dial(ctx, c.ln.Addr().String(), c.svc.id, c.relID, &kolosecure.Config{Transport: transportConfig()})
	}
	if old := c.connection(); old != nil {
		old.Close()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	for {
		select {
		case peer := <-c.relayPeer:
			if peer.Conn().Context().Err() != nil {
				continue // one that ended while it waited
			}
			conn := peer.Conn()
			if h := relayHook; h != nil {
				conn = h(c, conn)
			}
			return conn, nil
		case <-t.C:
			return nil, ErrNotFound
		case <-c.ctx.Done():
			return nil, c.ctx.Err()
		}
	}
}

// resumed is what the call does once its session to the relay is back: the watchers find the new session by
// themselves; our video starts a new group, so everyone gets a picture at once.
func (c *call) resumed() {
	if p := c.publisher(); p != nil {
		p.setPath(decision{scale: p.rateScale(), key: true})
	}
}
