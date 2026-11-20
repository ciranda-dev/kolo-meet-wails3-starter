//go:build finished

package main

import (
	"testing"
	"time"

	kolomoq "github.com/ciranda-dev/kolo-moq"
)

// leftEvents is how many people the windows of ps were told left.
func leftEvents(ps ...*peer) int {
	n := 0
	for _, p := range ps {
		p.rec.mu.Lock()
		n += len(p.rec.left)
		p.rec.mu.Unlock()
	}
	return n
}

// moving waits until the media of the others reaches p again: more audio frames and pictures than it had when it asks.
func moving(t *testing.T, p *peer, from ...string) {
	t.Helper()
	c := p.activeCall()
	type seen struct{ a, v uint64 }
	before := map[string]seen{}
	get := func(id string) *participant {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.people[id]
	}
	for _, id := range from {
		x := get(id)
		if x == nil {
			t.Fatalf("%s is not in the call", id)
		}
		before[id] = seen{x.audioIn.Load(), x.videoIn.Load()}
	}
	waitUntil(t, "media from everyone again", func() bool {
		for id, b := range before {
			x := get(id)
			if x == nil || x.audioIn.Load() < b.a+50 || x.videoIn.Load() < b.v+10 {
				return false
			}
		}
		return true
	})
}

// A guest whose session to the relay is replaced, as the watchdog does, keeps the call: nobody leaves, the media of
// both comes back, and the host's list of people is the same.
func TestGuestResetKeepsTheCall(t *testing.T) {
	ps := network(t, 2)
	startCall(t, ps)
	host, guest := ps[0], ps[1]
	hid, gid := idOf(t, host), idOf(t, guest)
	moving(t, guest, hid)
	moving(t, host, gid)
	old := guest.activeCall().session()
	guest.activeCall().resetRelay(errNoData)
	waitUntil(t, "a new session", func() bool { s := guest.activeCall().session(); return s != old && s.Err() == nil })
	moving(t, guest, hid)
	moving(t, host, gid)
	if n := leftEvents(host, guest); n != 0 {
		t.Errorf("%d people left", n)
	}
	for _, p := range ps {
		if c, _ := p.rec.lastCall(); c.State != callActive || len(c.Participants) != 2 || c.Error != nil {
			t.Errorf("%s: %s", p.rec.diag(), describe(c))
		}
	}
}

// The host's own session to its relay is replaced the same way, and the guest does not see it leave.
func TestHostResetKeepsTheCall(t *testing.T) {
	ps := network(t, 2)
	startCall(t, ps)
	host, guest := ps[0], ps[1]
	hid, gid := idOf(t, host), idOf(t, guest)
	old := host.activeCall().session()
	host.activeCall().resetRelay(errNoData)
	waitUntil(t, "a new session", func() bool { s := host.activeCall().session(); return s != old && s.Err() == nil })
	moving(t, guest, hid)
	moving(t, host, gid)
	if n := leftEvents(host, guest); n != 0 {
		t.Errorf("%d people left", n)
	}
}

// A call that cannot reach its relay any more ends once the window is over, with the lost connection as the reason, and
// not before. The window is shortened for the test.
func TestReconnectGivesUp(t *testing.T) {
	old := reconnectWindow
	reconnectWindow = 4 * time.Second
	t.Cleanup(func() { reconnectWindow = old })
	ps := network(t, 2)
	startCall(t, ps)
	guest := ps[1]
	// A relay that cannot be reached: every connection handed over is closed.
	relayHook = func(c *call, conn kolomoq.Conn) kolomoq.Conn { conn.Close(); return conn }
	t.Cleanup(func() { relayHook = nil })
	start := time.Now()
	guest.activeCall().resetRelay(errNoData)
	end := guest.rec.waitCall(t, "ended", func(c Call) bool { return c.State == callEnded })
	if end.Error == nil || end.Error.Reason != "call-ended-lost" {
		t.Fatalf("ended with %+v", end.Error)
	}
	if d := time.Since(start); d < 3*time.Second {
		t.Fatalf("gave up after %v, before its window", d)
	}
}
