//go:build finished

package main

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	invite "github.com/ciranda-dev/kolo-invite"
)

func TestFourPeopleCall(t *testing.T) {
	ps := network(t, 4)
	startCall(t, ps)
	ids := make([]string, len(ps))
	for i, p := range ps {
		ids[i] = idOf(t, p)
	}
	// Everyone sees and hears everyone else, and only them, with the brightness and the pitch each one makes.
	for i, p := range ps {
		for j := range ps {
			if i == j {
				continue
			}
			waitUntil(t, "picture", func() bool { return p.rec.frameCount(ids[j]) > 3 })
			want := float64(int(parties[j].luma)-16) * 255 / 219
			if got := p.rec.meanBrightness(t, ids[j]); got < want-30 || got > want+30 {
				t.Errorf("%s sees %s at brightness %.0f, want about %.0f", parties[i].name, parties[j].name, got, want)
			}
		}
		if n := p.rec.frameCount(ids[i]); n != 0 {
			t.Errorf("%s got %d pictures of itself from the network", parties[i].name, n)
		}
	}
	waitUntil(t, "everyone's voice", func() bool {
		for i, p := range ps {
			heard := p.hw.heard()
			for j := range ps {
				if i != j && tone(heard, parties[j].freq) < 0.03 {
					return false
				}
			}
		}
		return true
	})
	for i, p := range ps {
		if tone(p.hw.heard(), parties[i].freq) > 0.02 {
			t.Errorf("%s hears itself", parties[i].name)
		}
	}
	for _, p := range ps {
		c, _ := p.rec.lastCall()
		for j := range ps {
			waitUntil(t, "name", func() bool { c, _ = p.rec.lastCall(); return findPart(c, ids[j]).Name == parties[j].name })
		}
		if !strings.HasPrefix(c.Security.Header, "4 in call, end-to-end encrypted, through relay run by ") || c.Security.Waiting != 0 {
			t.Errorf("security %+v", c.Security)
		}
	}

	// Carla mutes and Davi turns the camera off: the others see it in the sealed catalog.
	if _, err := ps[2].SetMicrophone(false); err != nil {
		t.Fatal(err)
	}
	if _, err := ps[3].SetCamera(false); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*peer{ps[0], ps[1]} {
		p.rec.waitCall(t, "saw the mute and the camera off", func(c Call) bool {
			return !findPart(c, ids[2]).AudioOn && findPart(c, ids[2]).VideoOn && findPart(c, ids[3]).AudioOn && !findPart(c, ids[3]).VideoOn
		})
	}
	waitUntil(t, "silence from Carla", func() bool {
		return tone(ps[0].hw.heard(), parties[2].freq) < 0.01 && tone(ps[0].hw.heard(), parties[1].freq) > 0.03
	})
	// And back on.
	if _, err := ps[2].SetMicrophone(true); err != nil {
		t.Fatal(err)
	}
	if _, err := ps[3].SetCamera(true); err != nil {
		t.Fatal(err)
	}
	ps[0].rec.waitCall(t, "saw them back", func(c Call) bool { return findPart(c, ids[2]).AudioOn && findPart(c, ids[3]).VideoOn })
	waitUntil(t, "Carla's voice again", func() bool { return tone(ps[0].hw.heard(), parties[2].freq) > 0.03 })
	// The microphone and the camera of the device are closed while off.
	ps[2].SetMicrophone(false)
	if mic, _, _ := ps[2].mediaStates(); mic.State != "off" {
		t.Errorf("microphone %+v", mic)
	}

	// A guest leaves; the others go on with three.
	if err := ps[1].LeaveCall(); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 2, 3} {
		ps[i].rec.waitCall(t, "three left", func(c Call) bool {
			return c.State == callActive && len(c.Participants) == 3 && findPart(c, ids[1]).ID == ""
		})
	}
	// The host leaves: the call ends for everyone, saying why.
	if err := ps[0].LeaveCall(); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{2, 3} {
		c := ps[i].rec.waitCall(t, "ended", func(c Call) bool { return c.State == callEnded })
		if c.Error == nil || c.Error.Reason != "call-ended-host-left" {
			t.Errorf("%s: end %+v", parties[i].name, c.Error)
		}
		waitUntil(t, "devices closed", func() bool { return ps[i].hw.opened.Load() == 0 })
	}
}

func TestCallIsFullAtMax(t *testing.T) {
	first := freeUDP(t)
	var ps []*peer
	for i := range 4 {
		cfg := Config{Bootstrap: first, MaxParticipants: 3}
		if i == 0 {
			cfg = Config{First: first, MaxParticipants: 3}
		}
		p := startPeer(t, parties[i].name, cfg, newFakeHW(parties[i].luma, parties[i].freq), nil)
		p.rec.waitNetwork(t, netReady)
		ps = append(ps, p)
	}
	startCall(t, ps[:3])
	c, _ := ps[0].rec.lastCall()
	if _, err := ps[3].JoinCall(c.Invite); err != nil {
		t.Fatal(err)
	}
	end := ps[3].rec.waitCall(t, "refused", func(c Call) bool { return c.State == callEnded })
	if end.Error == nil || end.Error.Reason != "join-denied" || !errors.Is(end.Error, invite.ErrDenied) {
		t.Fatalf("fourth guest: %+v", end.Error)
	}
	if n := len(mustCall(t, ps[0]).Participants); n != 3 {
		t.Errorf("host sees %d people", n)
	}
	// When one leaves, the place is free again. The fourth guest was told no by that invite, which remembers it;
	// a new invite lets it in. The guest that left is shut out of the first invite.
	if err := ps[2].LeaveCall(); err != nil {
		t.Fatal(err)
	}
	ps[0].rec.waitCall(t, "two left", func(c Call) bool { return len(c.Participants) == 2 })
	code, err := ps[0].NewInvite(mainCtx())
	if err != nil || code == c.Invite {
		t.Fatalf("NewInvite: %q %v", code, err)
	}
	ps[3].turnOn(t)
	if _, err := ps[3].JoinCall(code); err != nil {
		t.Fatal(err)
	}
	ps[3].rec.waitCall(t, "joined", func(c Call) bool { return c.State == callActive && len(c.Participants) == 3 })
	// Carla, who left, is at the door with the invite she used: it no longer lets her in.
	ps[2].turnOn(t)
	started := time.Now()
	if _, err := ps[2].JoinCall(c.Invite); err != nil {
		t.Fatal(err)
	}
	end = ps[2].rec.waitCall(t, "shut out", func(c Call) bool { return c.State == callEnded })
	if end.Error == nil {
		t.Error("a guest that left got back in with the same invite")
	} else if end.Error.Reason != "rejoin-refused" {
		t.Errorf("turned away with %+v, want rejoin-refused", end.Error)
	} else {
		t.Logf("turned away after %v: %+v", time.Since(started), end.Error)
	}
	if n := len(mustCall(t, ps[0]).Participants); n != 3 {
		t.Errorf("host sees %d people", n)
	}
}

func mustCall(t *testing.T, p *peer) Call {
	t.Helper()
	c := p.Call(context.Background())
	if c == nil {
		t.Fatal("no call")
	}
	return *c
}

func TestJoinErrors(t *testing.T) {
	ps := network(t, 2)
	a, b := ps[0], ps[1]
	b.mu.Lock()
	b.set.FindTimeout = 5 * time.Second // the case below that needs a host nobody runs
	b.mu.Unlock()
	for _, bad := range []string{"", "hello", "koloinv0", strings.Repeat("x", 5000)} {
		if _, err := b.JoinCall(bad); !errors.Is(err, ErrInvalidInvite) && !errors.Is(err, invite.ErrInvalidCode) {
			t.Errorf("JoinCall(%.20q): %v", bad, err)
		}
		var me *MeetError
		if _, err := b.JoinCall(bad); !errors.As(err, &me) || me.Reason != "invalid-invite" {
			t.Errorf("JoinCall(%.20q) reason: %v", bad, err)
		}
	}
	if _, err := b.JoinCall("koloinv0xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"); err == nil {
		t.Error("a short code joined")
	}
	if err := b.LeaveCall(); err == nil {
		t.Error("LeaveCall without a call")
	}
	// An invite the host revoked by ending its call.
	if _, err := a.CreateCall(); err != nil {
		t.Fatal(err)
	}
	c := a.rec.waitCall(t, "invite", func(c Call) bool { return c.Invite != "" && c.State == callActive })
	if err := a.LeaveCall(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.JoinCall(c.Invite); err != nil {
		t.Fatal(err)
	}
	end := b.rec.waitCall(t, "failed", func(c Call) bool { return c.State == callEnded })
	if end.Error == nil || end.Error.Kind != kindNotFound && end.Error.Kind != kindExpired {
		t.Errorf("a finished call's invite: %+v", end.Error)
	}
	// A name is required to call.
	nameless := startPeer(t, "", Config{Bootstrap: a.Info().Network.FirstNode}, newFakeHW(1, 1), nil)
	if _, err := nameless.CreateCall(); err == nil || err.(*MeetError).Reason != "name-empty" {
		t.Errorf("CreateCall without a name: %v", err)
	}
}

func TestOneCallAtATime(t *testing.T) {
	ps := network(t, 2)
	a := ps[0]
	if _, err := a.CreateCall(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateCall(); err == nil || err.(*MeetError).Reason != "busy-call" {
		t.Errorf("a second call: %v", err)
	}
	// A real invite of another person's call: the busy check, not the invite check, refuses it.
	if _, err := ps[1].CreateCall(); err != nil {
		t.Fatal(err)
	}
	other := ps[1].rec.waitCall(t, "invite", func(c Call) bool { return c.Invite != "" && c.State == callActive })
	if _, err := a.JoinCall(other.Invite); err == nil || err.(*MeetError).Reason != "busy-call" {
		t.Errorf("joined while hosting: %v", err)
	}
	if err := a.Reconnect(); err == nil || err.(*MeetError).Reason != "busy-reconnect" {
		t.Errorf("Reconnect in a call: %v", err)
	}
	a.rec.waitCall(t, "active", func(c Call) bool { return c.State == callActive })
	if err := a.LeaveCall(); err != nil {
		t.Fatal(err)
	}
	// Leaving twice at once is safe, and a new call can start at once.
	done := make(chan error, 2)
	if _, err := a.CreateCall(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		go func() { done <- a.LeaveCall() }()
	}
	for range 2 {
		<-done
	}
	if _, err := a.CreateCall(); err != nil {
		t.Fatal(err)
	}
	a.rec.waitCall(t, "active again", func(c Call) bool { return c.State == callActive })
}

func TestShutdownEndsTheCall(t *testing.T) {
	ps := network(t, 2)
	startCall(t, ps)
	if err := ps[1].ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	end := ps[1].rec.waitCall(t, "ended", func(c Call) bool { return c.State == callEnded })
	if end.Error == nil || end.Error.Reason != "call-ended-closed" {
		t.Errorf("shutdown: %+v", end.Error)
	}
	if n := ps[1].hw.opened.Load(); n != 0 {
		t.Errorf("%d devices open after shutdown", n)
	}
	ps[0].rec.waitCall(t, "alone", func(c Call) bool { return len(c.Participants) == 1 })
	if _, err := ps[1].CreateCall(); err == nil {
		t.Error("a call started after shutdown")
	}
	time.Sleep(10 * time.Millisecond)
}

// settledGoroutines returns the number of goroutines once it has stayed the same for 300 ms (at most 10 s), so a
// goroutine of an earlier test that is still ending is not counted as the baseline.
func settledGoroutines() int {
	last, same := -1, 0
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		runtime.GC()
		if n := runtime.NumGoroutine(); n == last {
			if same++; same == 3 {
				return n
			}
		} else {
			last, same = n, 0
		}
	}
	return runtime.NumGoroutine()
}

// After the calls end and the services stop, no goroutine of a call or of the media is left.
func TestNoGoroutineLeak(t *testing.T) {
	before := settledGoroutines()
	func() {
		ps := network(t, 3)
		startCall(t, ps)
		ps[1].SetCamera(false)
		ps[1].SetMicrophone(false)
		ps[2].LeaveCall()
		ps[0].LeaveCall()
		for _, p := range ps {
			p.ServiceShutdown()
		}
	}()
	var after int
	for range 100 {
		runtime.GC()
		if after = runtime.NumGoroutine(); after <= before+4 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	buf := make([]byte, 1<<20)
	t.Fatalf("%d goroutines before, %d after:\n%s", before, after, buf[:runtime.Stack(buf, true)])
}
