//go:build finished

package main

import (
	"sync"
	"testing"
	"time"

	"github.com/ciranda-dev/kolo-meet-wails3-starter/internal/netsim"
	kolomoq "github.com/ciranda-dev/kolo-moq"
	kolosecure "github.com/ciranda-dev/kolo-secure"
)

// fleet is a host and guests on one machine, each guest reaching the call's relay through a link of its own: the path of
// each person is impaired alone.
type fleet struct {
	t      *testing.T
	host   *peer
	guests []*peer
	hc     *call
	invite string

	mu    sync.Mutex
	links map[string]*netsim.Link // by the guest's key
}

// newFleet starts a host with the camera and microphone on, in a call, and n guests that have not joined.
func newFleet(t *testing.T, vid videoMode, n int) *fleet {
	t.Helper()
	first := freeUDP(t)
	mk := func(i int, cfg Config) *peer {
		hw := newFakeHW(parties[i].luma, parties[i].freq)
		hw.texture = true
		p := startPeer(t, parties[i].name, cfg, hw, func(s *MeetService) { s.vid = vid })
		p.rec.waitNetwork(t, netReady)
		return p
	}
	f := &fleet{t: t, links: map[string]*netsim.Link{}}
	f.host = mk(0, Config{First: first})
	for i := 1; i <= n; i++ {
		f.guests = append(f.guests, mk(i, Config{Bootstrap: first}))
	}
	f.host.turnOn(t)
	if _, err := f.host.CreateCall(); err != nil {
		t.Fatal(err)
	}
	c := f.host.rec.waitCall(t, "had an invite", func(c Call) bool { return c.Invite != "" && c.State == callActive })
	f.invite, f.hc = c.Invite, f.host.activeCall()
	relayHook = func(c *call, conn kolomoq.Conn) kolomoq.Conn {
		f.mu.Lock()
		link := f.links[idOfService(c.svc)]
		f.mu.Unlock()
		if link == nil {
			return conn
		}
		conn.Close()
		d, err := kolosecure.Dial(c.ctx, link.Addr(), c.svc.id, f.hc.relID, &kolosecure.Config{Transport: transportConfig()})
		if err != nil {
			t.Logf("dial through the link: %v", err)
			return conn
		}
		return d
	}
	t.Cleanup(func() { relayHook = nil })
	return f
}

func idOfService(s *MeetService) string { return s.Info().KeyID }

// join has guest i, whose path is up and down, join the call and waits until everyone sees everyone live. A zero up and
// down is a direct connection with no link.
func (f *fleet) join(i int, up, down netsim.Profile) *netsim.Link {
	f.t.Helper()
	g := f.guests[i]
	var link *netsim.Link
	if up != (netsim.Profile{}) || down != (netsim.Profile{}) {
		var err error
		if link, err = netsim.New(f.hc.ln.Addr().String(), up, down); err != nil {
			f.t.Fatal(err)
		}
		f.t.Cleanup(func() { link.Close() })
		f.mu.Lock()
		f.links[idOf(f.t, g)] = link
		f.mu.Unlock()
	}
	g.turnOn(f.t)
	if _, err := g.JoinCall(f.invite); err != nil {
		f.t.Fatal(err)
	}
	return link
}

// waitLive waits until every peer sees all of the first n guests and the host live.
func (f *fleet) waitLive(n int) {
	f.t.Helper()
	for _, p := range append([]*peer{f.host}, f.guests[:n]...) {
		p.rec.waitCall(f.t, "had everyone live", func(c Call) bool {
			if c.State != callActive || len(c.Participants) != n+1 {
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
}

// from is the record the call of p has of the person with key id.
func (f *fleet) from(p *peer, id string) *participant {
	cl := p.activeCall()
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.people[id]
}

// watch samples the audio and video of from every 50 ms for dur, with the time since the call of the sampling began.
func watchFor(dur time.Duration, p *participant, each func(el time.Duration)) []sample {
	var out []sample
	start := time.Now()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for now := range tick.C {
		el := now.Sub(start)
		if each != nil {
			each(el)
		}
		out = append(out, sample{at: el, audio: p.audioIn.Load(), video: p.videoIn.Load(), conc: p.concealed.Load()})
		if el >= dur {
			return out
		}
	}
	return out
}

// maxGap is the longest time between two pictures (or audio frames) in the samples from a time to another, a count that
// does not move being a gap that goes on to the end.
func maxGap(s []sample, from, to time.Duration, video bool) time.Duration {
	var last, worst time.Duration
	var prev uint64
	started := false
	for _, x := range s {
		if x.at < from || x.at > to {
			continue
		}
		v := x.audio
		if video {
			v = x.video
		}
		if !started {
			started, last, prev = true, x.at, v
			continue
		}
		if v != prev {
			worst = max(worst, x.at-last)
			last, prev = x.at, v
		}
	}
	if started {
		worst = max(worst, to-last)
	}
	return worst
}
