//go:build finished

package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	invite "github.com/ciranda-dev/kolo-invite"
	kolomoq "github.com/ciranda-dev/kolo-moq"
	"github.com/ciranda-dev/kolo-moq/secobj"
	kolotransport "github.com/ciranda-dev/kolo-transport"
)

// A person who left or was removed is shut out of every invite that existed then; a newer invite lets them in again.
// The list is bounded: barredPerPlace for each place of the call, the oldest dropped first.
func TestBarredGuests(t *testing.T) {
	c := bareCall(t, roleHost, 8)
	g1, g2 := bytes.Repeat([]byte{4}, 32), bytes.Repeat([]byte{5}, 32)
	old, other, fresh := invite.ID{1}, invite.ID{2}, invite.ID{3}
	if !c.letIn(g1, old) {
		t.Fatal("a stranger with an invite and a free place is turned away")
	}
	c.admit(g1)
	c.people[hex.EncodeToString(g1)] = &participant{key: g1}
	if !c.isMember(g1) || !c.approved(g1) {
		t.Fatal("an admitted guest is not a member")
	}
	delete(c.people, hex.EncodeToString(g1))
	c.shut(g1, []invite.ID{old, other})
	if c.isMember(g1) || c.approved(g1) || !c.isBarred(g1) {
		t.Error("a guest that left keeps its admission")
	}
	for _, id := range []invite.ID{old, other} {
		if c.letIn(g1, id) {
			t.Errorf("invite %x lets a guest that left back in", id[:1])
		}
	}
	if !c.letIn(g1, fresh) || !c.letIn(g2, old) {
		t.Error("a newer invite, or someone else, is turned away")
	}
	c.admit(g1) // the host let it in with the newer invite
	if c.isBarred(g1) || !c.approved(g1) || !c.letIn(g1, old) {
		t.Error("a guest let in again is still shut out")
	}
	if c.letIn(c.me, fresh) {
		t.Error("the host is let in as a guest")
	}

	limit := barredPerPlace * c.room.max
	keys := make([][]byte, limit+3)
	for i := range keys {
		keys[i] = append(bytes.Repeat([]byte{9}, 30), byte(i>>8), byte(i))
		c.shut(keys[i], []invite.ID{old})
	}
	c.mu.Lock()
	n, order := len(c.barred), len(c.barOrder)
	c.mu.Unlock()
	if n != limit || order != limit {
		t.Fatalf("%d barred, %d in order; want %d", n, order, limit)
	}
	for i, k := range keys {
		if want := i >= 3; c.isBarred(k) != want {
			t.Errorf("key %d barred %v, want %v", i, !want, want)
		}
	}
	// Shutting out the same key again keeps one entry, now the newest.
	c.shut(keys[3], []invite.ID{old})
	c.shut(bytes.Repeat([]byte{7}, 32), []invite.ID{old})
	if !c.isBarred(keys[3]) || c.isBarred(keys[4]) {
		t.Error("a key shut out again was not moved to the newest end")
	}
}

// closedConn is a connection the call's relay closed with code.
type closedConn struct {
	kolomoq.Conn
	ctx context.Context
}

func (c closedConn) Context() context.Context { return c.ctx }

func connClosedWith(code uint64) kolomoq.Conn {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(&kolotransport.Error{Err: kolotransport.ErrPeerClosed, Code: code})
	return closedConn{ctx: ctx}
}

// A guest whose relay connection was closed with the shut-out code hears that the host removed it (in the call) or
// does not let it back in (while joining); any other close keeps the old reasons.
func TestShutOutReasons(t *testing.T) {
	old := shutWait
	shutWait = 50 * time.Millisecond
	t.Cleanup(func() { shutWait = old })
	cases := []struct {
		state string
		code  uint64
		want  string
	}{
		{callActive, closeShutOut, "call-ended-removed"},
		{callConnecting, closeShutOut, "rejoin-refused"},
		{callActive, 0, "call-ended-host-left"},
		{callActive, 2, "call-ended-host-left"}, // kolo-swarm's duplicate code is not a removal
		{callConnecting, 0, "call-full"},
	}
	for _, tc := range cases {
		c := bareCall(t, roleGuest, 8)
		c.ending, c.state = false, tc.state
		c.svc.net = Network{State: netReady}
		c.relayConn = connClosedWith(tc.code)
		c.relayGone(nil)
		if c.endErr == nil || c.endErr.Reason != tc.want {
			t.Errorf("%+v: %+v", tc, c.endErr)
		}
	}
	// A host's own relay never shuts it out.
	c := bareCall(t, roleHost, 8)
	c.ending = false
	c.relayConn = connClosedWith(closeShutOut)
	c.relayGone(nil)
	if c.endErr == nil || c.endErr.Reason != "call-ended-lost" {
		t.Errorf("host: %+v", c.endErr)
	}
	// A refused join keeps its reason through explainJoin.
	if got := explainJoin(meetErr("rejoin-refused", &kolomoq.SessionError{Remote: true}, "")); got.Reason != "rejoin-refused" {
		t.Errorf("explainJoin: %+v", got)
	}
	if got := shutOut(connClosedWith(closeShutOut)); !got {
		t.Error("shutOut misses the code")
	}
	if shutOut(nil) {
		t.Error("shutOut of no connection")
	}
}

// RemoveParticipant is for the host only, never for the host itself, and only for someone in the call.
func TestRemoveRefusals(t *testing.T) {
	reason := func(err error) string {
		var me *MeetError
		if !errors.As(err, &me) {
			return ""
		}
		return me.Reason
	}
	s := &MeetService{emit: func(string, any) {}}
	if r := reason(s.RemoveParticipant("ab")); r != "no-call" {
		t.Errorf("without a call: %q", r)
	}
	guest := bareCall(t, roleGuest, 8)
	other := strings.Repeat("cd", 32)
	guest.people[other] = &participant{key: bytes.Repeat([]byte{0xcd}, 32), id: other}
	guest.svc.call = guest
	if r := reason(guest.svc.RemoveParticipant(other)); r != "not-host" {
		t.Errorf("a guest removes: %q", r)
	}
	host := bareCall(t, roleHost, 8)
	host.svc.call = host
	for key, want := range map[string]string{host.selfID: "remove-self", other: "participant-unknown", "": "participant-unknown", "xyz": "participant-unknown",
		strings.ToUpper(other): "participant-unknown"} {
		if r := reason(host.svc.RemoveParticipant(key)); r != want {
			t.Errorf("RemoveParticipant(%.8q): %q, want %q", key, r, want)
		}
	}
	host.state = callCreating
	if r := reason(host.svc.RemoveParticipant(other)); r != "no-call" {
		t.Errorf("before the call is active: %q", r)
	}
}

// readSealed returns the next normal object of sub.
func readSealed(t *testing.T, ctx context.Context, sub *kolomoq.Subscription) kolomoq.Object {
	t.Helper()
	for {
		obj, err := sub.ReadObject(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if obj.Status == kolomoq.StatusNormal {
			return obj
		}
	}
}

// opens reports whether keys open obj, a profile object of ns that came through sess, and fails on anything but a
// missing key.
func opens(t *testing.T, keys *secobj.Keyring, sess *kolomoq.Session, ns kolomoq.Namespace, obj kolomoq.Object) bool {
	t.Helper()
	op, err := keys.Opener(sess, ns, profileTrack)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = op.Open(obj)
	if err != nil && !isNoKey(err) {
		t.Fatalf("open: %v", err)
	}
	return err == nil
}

// waitRotated waits for a profile object of ns, through the owner-side session sess, that keys opens and the leaver's
// keys do not: the owner of ns rotated and gave the new key to everyone but the leaver.
func waitRotated(t *testing.T, sess *kolomoq.Session, keys, leaver *secobj.Keyring, ns kolomoq.Namespace) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		sub, err := sess.Subscribe(ctx, ns, profileTrack, &kolomoq.SubscribeOptions{Join: kolomoq.JoinCurrentGroup})
		if err != nil {
			t.Fatal(err)
		}
		obj := readSealed(t, ctx, sub)
		sub.Close()
		if opens(t, keys, sess, ns, obj) && !opens(t, leaver, sess, ns, obj) {
			return
		}
		time.Sleep(retryEvery)
	}
	t.Fatalf("%s never sealed under a key the removed guest lacks", ns[1][:8])
}

// The host removes one guest: that guest's call ends with "removed" and its devices close, the others go on under new
// keys it does not have, and the same invite does not let it back in; a new invite does. A guest that leaves is
// shut out of the invite it used too. A guest cannot remove anyone, and nobody removes the host.
func TestHostRemovesAGuest(t *testing.T) {
	ps := network(t, 3)
	host, a, b := ps[0], ps[1], ps[2]
	// The removed guest's teardown waits, so its keyring is still there: a modified app that kept its keys.
	hold := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(hold) }) }
	a.teardownHook = func() { <-hold }
	t.Cleanup(release)
	code := startCall(t, ps)
	idHost, idA, idB := idOf(t, host), idOf(t, a), idOf(t, b)

	hc, ac, bc := host.activeCall(), a.activeCall(), b.activeCall()
	nsHost, nsB := hc.room.memberNS(hc.me), hc.room.memberNS(bc.me)
	keysA := ac.keys
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Before the removal the guest opens what the others seal.
	sub, err := hc.relaySess.Subscribe(ctx, nsB, profileTrack, &kolomoq.SubscribeOptions{Join: kolomoq.JoinCurrentGroup})
	if err != nil {
		t.Fatal(err)
	}
	if obj := readSealed(t, ctx, sub); !opens(t, keysA, hc.relaySess, nsB, obj) {
		t.Fatal("the guest cannot open the other guest's profile before the removal")
	}
	sub.Close()

	var me *MeetError
	if err := b.RemoveParticipant(idA); !errors.As(err, &me) || me.Reason != "not-host" {
		t.Errorf("a guest removes: %v", err)
	}
	if err := host.RemoveParticipant(idHost); !errors.As(err, &me) || me.Reason != "remove-self" {
		t.Errorf("the host removes itself: %v", err)
	}
	if err := host.RemoveParticipant(strings.Repeat("ab", 32)); !errors.As(err, &me) || me.Reason != "participant-unknown" {
		t.Errorf("an unknown key: %v", err)
	}
	if n := len(mustCall(t, host).Participants); n != 3 {
		t.Fatalf("a refused removal changed the call: %d people", n)
	}

	framesB := host.rec.frameCount(idB)
	framesHost := b.rec.frameCount(idHost)
	if err := host.RemoveParticipant(idA); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*peer{host, b} {
		p.rec.waitCall(t, "the removed guest gone", func(c Call) bool {
			return c.State == callActive && len(c.Participants) == 2 && findPart(c, idA).ID == ""
		})
	}
	// Each one left rotated: the removed guest's keys open nothing they seal now.
	waitRotated(t, hc.relaySess, hc.keys, keysA, nsB)
	waitRotated(t, bc.relaySess, bc.keys, keysA, nsHost)
	release()

	end := a.rec.waitCall(t, "removed", func(c Call) bool { return c.State == callEnded })
	if end.Error == nil || end.Error.Reason != "call-ended-removed" || end.Error.Kind != kindEnded {
		t.Fatalf("the removed guest ends with %+v", end.Error)
	}
	removed := end.ID
	waitUntil(t, "the removed guest's devices closed", func() bool { return a.hw.opened.Load() == 0 && a.activeCall() == nil })
	waitUntil(t, "the others keep seeing each other", func() bool {
		return host.rec.frameCount(idB) > framesB+3 && b.rec.frameCount(idHost) > framesHost+3
	})
	b.rec.mu.Lock()
	left := slices.Clone(b.rec.left)
	b.rec.mu.Unlock()
	if len(left) == 0 || left[len(left)-1].ID != idA {
		t.Errorf("the other guest was not told: %+v", left)
	}

	// The same invite does not let the removed guest back in.
	if _, err := a.JoinCall(code); err != nil {
		t.Fatal(err)
	}
	end = a.rec.waitCall(t, "refused", func(c Call) bool { return c.State == callEnded && c.ID != removed })
	if end.Error == nil || end.Error.Reason != "rejoin-refused" {
		t.Fatalf("rejoin with the same invite: %+v", end.Error)
	}
	if n := len(mustCall(t, host).Participants); n != 2 {
		t.Fatalf("the host sees %d people after the refused rejoin", n)
	}

	// A new invite from the host lets it in again.
	fresh, err := host.NewInvite(mainCtx())
	if err != nil {
		t.Fatal(err)
	}
	a.turnOn(t)
	if _, err := a.JoinCall(fresh); err != nil {
		t.Fatal(err)
	}
	back := a.rec.waitCall(t, "back in", func(c Call) bool { return c.State == callActive && len(c.Participants) == 3 })

	// Leaving shuts it out of that invite as well.
	if err := a.LeaveCall(); err != nil {
		t.Fatal(err)
	}
	host.rec.waitCall(t, "the guest left", func(c Call) bool { return len(c.Participants) == 2 })
	if _, err := a.JoinCall(fresh); err != nil {
		t.Fatal(err)
	}
	end = a.rec.waitCall(t, "refused after leaving", func(c Call) bool { return c.State == callEnded && c.ID != back.ID })
	if end.Error == nil || end.Error.Reason != "rejoin-refused" {
		t.Fatalf("rejoin after leaving: %+v", end.Error)
	}
}
