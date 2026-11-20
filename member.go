package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	kolomoq "github.com/ciranda-dev/kolo-moq"
	"github.com/ciranda-dev/kolo-moq/secobj"
	kolotransport "github.com/ciranda-dev/kolo-transport"
)

// spawn runs f in a goroutine of the call unless the call is ending, so the teardown never waits for a goroutine
// that started after it began.
func (c *call) spawn(f func()) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ending {
		return false
	}
	c.wg.Go(f)
	return true
}

// sleep waits d or until ctx ends, and reports whether ctx is still alive.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// enter makes this person a participant of the call. Step 3 of the guide writes it.
func (c *call) enter(conn kolomoq.Conn) error { return notInStarter(3) }

// attach makes the session to the relay on conn and announces our namespace on it. It is the start of the call and
// every reconnection: the origin, with its tracks, stays and the relay asks it for them again.
func (c *call) attach(conn kolomoq.Conn) error {
	sctx, cancel := context.WithTimeout(c.ctx, c.svc.set.FindTimeout)
	defer cancel()
	relayOnly := func(peer ed25519.PublicKey, _ kolomoq.RequestKind, _ kolomoq.Namespace, _ string) error {
		if bytes.Equal(peer, c.room.relay) {
			return nil
		}
		return errors.New("kolo-meet: only the relay may read a broadcast")
	}
	c.relayMu.Lock()
	c.relayConn = conn
	c.relayMu.Unlock()
	sess, err := kolomoq.NewClient(sctx, conn, &kolomoq.Config{Origin: c.origin, Authorize: relayOnly})
	if err != nil {
		return c.relayErr(err)
	}
	if _, err = sess.PublishNamespace(sctx, c.room.memberNS(c.me)); err != nil {
		sess.Close()
		return c.relayErr(err)
	}
	c.relayMu.Lock()
	old := c.relaySess
	c.relaySess = sess
	close(c.relayNew)
	c.relayNew = make(chan struct{})
	c.relayMu.Unlock()
	if old != nil {
		old.Close()
	}
	return nil
}

// session is the session to the relay now.
func (c *call) session() *kolomoq.Session {
	c.relayMu.RLock()
	defer c.relayMu.RUnlock()
	return c.relaySess
}

// connection is the connection under the session to the relay.
func (c *call) connection() kolomoq.Conn {
	c.relayMu.RLock()
	defer c.relayMu.RUnlock()
	return c.relayConn
}

// nextSession waits until the session to the relay is not old, and returns the new one; false when the call ends first.
func (c *call) nextSession(old *kolomoq.Session) (*kolomoq.Session, bool) {
	for {
		c.relayMu.RLock()
		cur, ch := c.relaySess, c.relayNew
		c.relayMu.RUnlock()
		if cur != old {
			return cur, true
		}
		select {
		case <-ch:
		case <-c.ctx.Done():
			return nil, false
		}
	}
}

// keyOwner is the Keyring's rule for who may give a key. Step 5 of the guide writes it; until then it refuses every key.
func (c *call) keyOwner(peer ed25519.PublicKey, ns kolomoq.Namespace) error { return notInStarter(5) }

// pairAuthorize is the rule for the sessions between two participants: members read the key track made for them,
// nothing else. Media goes through the relay only.
func (c *call) pairAuthorize(peer ed25519.PublicKey, kind kolomoq.RequestKind, ns kolomoq.Namespace, _ string) error {
	switch kind {
	case kolomoq.KindSubscribe, kolomoq.KindFetch, kolomoq.KindTrackStatus:
	default:
		return errors.New("kolo-meet: refused")
	}
	if len(ns) == 0 || ns[0] != kolomoq.KeyNamespaceField {
		return errors.New("kolo-meet: refused")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.people[hex.EncodeToString(peer)] == nil {
		return errors.New("kolo-meet: not in the call")
	}
	return nil
}

// relayErr explains a failed start of the session to the relay. A refused request (no place) is not a closed
// connection, so it is not waited for.
func (c *call) relayErr(err error) *MeetError {
	var re *kolomoq.RequestError
	if c.role == roleGuest && !errors.As(err, &re) && shutOut(c.connection()) {
		return meetErr("rejoin-refused", err, "")
	}
	return meetErr("relay-unreachable", err, "")
}

// shutOut reports whether the call's relay closed conn with closeShutOut: the host does not let this key in.
func shutOut(conn kolomoq.Conn) bool {
	if conn == nil {
		return false
	}
	select {
	case <-conn.Context().Done():
	case <-time.After(shutWait):
		return false
	}
	var te *kolotransport.Error
	return errors.As(context.Cause(conn.Context()), &te) && errors.Is(te, kolotransport.ErrPeerClosed) && te.Code == closeShutOut
}

// relayGone ends the call because the relay is gone: for a guest the host left or lost the connection.
func (c *call) relayGone(err error) {
	c.mu.Lock()
	joining := c.state == callConnecting
	c.mu.Unlock()
	switch {
	case c.role == roleGuest && shutOut(c.connection()):
		if joining {
			c.end(meetErr("rejoin-refused", err, ""))
		} else {
			c.end(meetErr("call-ended-removed", err, ""))
		}
	case c.role == roleHost || !c.svc.networkReady():
		c.end(meetErr("call-ended-lost", err, ""))
	case joining:
		c.end(meetErr("call-full", err, ""))
	default:
		c.end(meetErr("call-ended-host-left", err, ""))
	}
}

// rosterLoop follows the namespaces announced at the relay under the room: they are the people in the call. Only a
// participant the host let in can announce its own namespace, so the list is the host's view of the call. It follows
// the call from one session to the relay to the next: a new session lists the namespaces again, and whoever is not in
// the list has left.
func (c *call) rosterLoop() {
	sess := c.session()
	for first := true; ; first = false {
		sub, err := sess.SubscribeNamespace(c.ctx, kolomoq.Namespace{c.room.id})
		if err != nil {
			if c.ctx.Err() != nil {
				return
			}
			if sess.Err() == nil {
				if first {
					c.roomRefused(err)
					return
				}
				c.resetRelay(err) // a session that came back on a path still bad: make another
			}
		} else if !c.readRoster(sess, sub, first) {
			return
		}
		var ok bool
		if sess, ok = c.nextSession(sess); !ok {
			return
		}
	}
}

// roomRefused ends the call because the relay refused the roster, or could not be reached.
func (c *call) roomRefused(err error) {
	switch {
	case c.role == roleGuest && shutOut(c.connection()):
		c.relayGone(err)
	case c.role == roleGuest:
		c.end(relayRefusal(err))
	default:
		c.end(meetErr("relay-unreachable", err, ""))
	}
}

// readRoster applies the events of sub until its session ends; false means the call ended. After a reconnection the
// first events are the whole list again, and the people it does not name are removed once it is over.
func (c *call) readRoster(sess *kolomoq.Session, sub *kolomoq.NamespaceSubscription, first bool) bool {
	var mu sync.Mutex
	var listed map[string]bool
	if !first {
		listed = map[string]bool{}
		t := time.AfterFunc(rosterSettle, func() {
			mu.Lock()
			l := listed
			listed = nil
			mu.Unlock()
			c.dropUnlisted(l)
		})
		defer t.Stop()
	}
	for {
		ev, err := sub.Next(c.ctx)
		if err != nil {
			switch {
			case c.ctx.Err() != nil:
				return false
			case sess.Err() != nil:
				return true
			}
			select { // the connection may have ended a moment before the session says so
			case <-sess.Done():
				return true
			case <-time.After(sessionEndWait):
			}
			if !first {
				c.resetRelay(err)
				return true
			}
			c.relayGone(err)
			return false
		}
		if len(ev.Namespace) != 2 {
			continue
		}
		key, err := hex.DecodeString(ev.Namespace[1])
		if err != nil || len(key) != ed25519.PublicKeySize || hex.EncodeToString(key) != ev.Namespace[1] {
			continue
		}
		mu.Lock()
		if listed != nil && ev.Active {
			listed[ev.Namespace[1]] = true
		}
		mu.Unlock()
		c.rosterChange(key, ev.Active)
	}
}

// dropUnlisted removes the people the relay's list did not name after a reconnection: they left while we were away.
func (c *call) dropUnlisted(listed map[string]bool) {
	if listed == nil {
		return
	}
	c.mu.Lock()
	var gone []ed25519.PublicKey
	for id, p := range c.people {
		if !listed[id] {
			gone = append(gone, p.key)
		}
	}
	c.mu.Unlock()
	for _, k := range gone {
		c.rosterChange(k, false)
	}
}

// rosterChange adds or removes a participant, gives its key to the new set and tells the window.
func (c *call) rosterChange(key ed25519.PublicKey, active bool) {
	id := hex.EncodeToString(key)
	if bytes.Equal(key, c.me) {
		c.markActive()
		return
	}
	c.mu.Lock()
	p := c.people[id]
	if active {
		if p != nil {
			c.mu.Unlock()
			return
		}
		if len(c.people)+1 >= c.room.max {
			// The call is full: a guest that got past the places (the host closes its session), or a relay that lists
			// more people than the call holds (a guest only refuses them). Nothing grows without bound.
			rp, ok := c.relaySessions[id]
			c.mu.Unlock()
			if ok {
				rp.sess.Close()
			}
			c.logf("roster: %s refused, the call holds %d", id[:8], c.room.max)
			return
		}
		ctx, cancel := context.WithCancel(c.ctx)
		p = &participant{key: key, id: id, ns: c.room.memberNS(key), ctx: ctx, cancel: cancel, name: peerName("", key),
			host: bytes.Equal(key, c.room.host), state: partWaitingKeys, joined: time.Now()}
		c.persons++
		p.person = c.persons
		c.people[id] = p
		c.order = append(c.order, id)
		c.peak = max(c.peak, len(c.people)+1)
		if a := c.admitted[string(key)]; a != nil {
			a.seen = true
		}
		pend, havePeer := c.pending[string(key)]
		delete(c.pending, string(key))
		c.mu.Unlock()
		c.spawn(func() { c.watchLoop(p) })
		c.spawn(func() { c.profileLoop(p) })
		if havePeer {
			c.startLink(p, pend.peer)
		}
	} else {
		if p == nil {
			c.mu.Unlock()
			return
		}
		delete(c.people, id)
		delete(c.relaySessions, id)
		c.order = slices.DeleteFunc(c.order, func(x string) bool { return x == id })
		l := p.link
		p.link = nil
		name := p.name
		c.mu.Unlock()
		p.cancel()
		if c.role == roleHost {
			c.shut(key, c.inviteIDs()) // the host cannot tell leaving from a lost connection: both need a new invite
		}
		if l != nil {
			l.cancel()
			l.sess.Close()
		}
		if c.keys != nil {
			c.keys.Remove(p.ns)
		}
		if c.mix != nil {
			c.mix.drop(id)
		}
		c.svc.emit("meet:participant", ParticipantEvent{Kind: "left", ID: id, Name: name, Message: leftSentence(name)})
	}
	c.updateMembers()
	c.changed()
}

// markActive moves the call to active when our own announcement came back: the relay has us.
func (c *call) markActive() {
	c.mu.Lock()
	st := c.state
	c.mu.Unlock()
	if st == callCreating || st == callConnecting {
		c.move(callActive, "")
	}
}

// ParticipantEvent is the payload of the "meet:participant" event: someone left the call. Message is the sentence
// to show, in English.
type ParticipantEvent struct {
	Kind    string `json:"kind"` // "left"
	ID      string `json:"id"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

// updateMembers gives our key to everyone in the call. Step 5 of the guide writes it; until then nobody gets it.
func (c *call) updateMembers() {}

// takePeer decides what a new swarm connection is for. It reports whether the call took the connection; the caller
// closes it otherwise.
func (c *call) takePeer(peer swarmPeer) bool {
	key := peer.PublicKey()
	c.mu.Lock()
	if c.ending {
		c.mu.Unlock()
		return false
	}
	if c.role == roleGuest && c.state == callJoining && c.code.Matches(key) {
		c.mu.Unlock()
		select {
		case c.joinPeer <- peer:
		default:
		}
		return true
	}
	if c.room == nil || bytes.Equal(key, c.me) {
		c.mu.Unlock()
		return false
	}
	if c.role == roleGuest && bytes.Equal(key, c.room.relay) {
		topic := c.room.topic
		c.mu.Unlock()
		// Only a connection made for the call's topic is a media session. One that shares only the invite topic is the
		// invite's connection (or a dial of it that was in flight when the invite topic was left): the relay serves
		// invites on it, not media, so a session started on it would wait for a SETUP that never comes.
		if !slices.Contains(peer.Topics(), topic) {
			return false
		}
		for {
			select {
			case c.relayPeer <- peer:
				return true
			case old := <-c.relayPeer:
				old.Conn().Close() // a connection nobody took is replaced by the newer one
			}
		}
	}
	if p := c.people[hex.EncodeToString(key)]; p != nil {
		c.mu.Unlock()
		c.startLink(p, peer)
		return true
	}
	if len(c.pending) >= 2*maxParticipantsCeiling {
		c.mu.Unlock()
		return false
	}
	c.pending[string(key)] = pendingPeer{peer: peer, at: time.Now()}
	c.mu.Unlock()
	return true
}

// expirePendingLoop closes the connections of people who never showed up in the call.
func (c *call) expirePendingLoop() {
	for sleep(c.ctx, pendingTTL/3) {
		var old []swarmPeer
		c.mu.Lock()
		for k, pp := range c.pending {
			if time.Since(pp.at) > pendingTTL {
				old = append(old, pp.peer)
				delete(c.pending, k)
			}
		}
		c.mu.Unlock()
		for _, p := range old {
			p.Conn().Close()
		}
	}
}

// peerLost forgets a connection that ended.
func (c *call) peerLost(peer swarmPeer) {
	key := peer.PublicKey()
	c.mu.Lock()
	if pp, ok := c.pending[string(key)]; ok && pp.peer == peer {
		delete(c.pending, string(key))
	}
	p := c.people[hex.EncodeToString(key)]
	c.mu.Unlock()
	if p != nil {
		if l := c.setLink(p, nil, peer); l != nil {
			l.stop()
		}
	}
}

// setLink makes l the link to p and tells the window, which shows how each person is reached; it returns the link
// it replaced. With only set, it replaces only a link on that connection, and does nothing otherwise.
func (c *call) setLink(p *participant, l *link, only swarmPeer) *link {
	c.mu.Lock()
	old := p.link
	if only != nil && (old == nil || old.peer != only) {
		c.mu.Unlock()
		return nil
	}
	p.link = l
	c.mu.Unlock()
	c.changed()
	return old
}

// stop ends the link's key session.
func (l *link) stop() {
	l.cancel()
	if l.sess != nil {
		l.sess.Close()
	}
}

// startLink fetches p's key on the connection between us. Step 5 of the guide writes it.
func (c *call) startLink(p *participant, peer swarmPeer) {}

func (c *call) setPState(p *participant, state string) {
	c.mu.Lock()
	changed := p.state != state
	p.state = state
	c.mu.Unlock()
	if changed {
		c.changed()
	}
}

// watchLoop plays p's broadcast. Step 4 of the guide writes it; until then nothing of p plays.
func (c *call) watchLoop(p *participant) {}

// profileLoop reads p's display name from its profile track.
func (c *call) profileLoop(p *participant) {
	for p.ctx.Err() == nil {
		c.readProfile(p)
		if !sleep(p.ctx, retryEvery) {
			return
		}
	}
}

func (c *call) readProfile(p *participant) {
	sess := c.session()
	sub, err := sess.Subscribe(p.ctx, p.ns, profileTrack, &kolomoq.SubscribeOptions{Priority: kolomoq.DefaultPriority, Join: kolomoq.JoinCurrentGroup})
	if err != nil {
		return
	}
	defer sub.Close()
	op, err := c.keys.Opener(sess, p.ns, profileTrack)
	if err != nil {
		return
	}
	for {
		obj, err := sub.ReadObject(p.ctx)
		if err != nil {
			return
		}
		if obj.Status != kolomoq.StatusNormal {
			continue
		}
		pt, _, err := op.Open(obj)
		for tries := 0; errors.Is(err, secobj.ErrNoKey) && tries < 10; tries++ {
			// the key may be on its way; wait for the keyring to change
			ch := c.keys.Changed()
			select {
			case <-ch:
			case <-time.After(retryEvery):
			case <-p.ctx.Done():
				return
			}
			pt, _, err = op.Open(obj)
		}
		if err != nil {
			continue
		}
		name, err := parseProfile(pt, p.key)
		if err != nil {
			continue
		}
		c.mu.Lock()
		changed := p.name != name
		p.name = name
		c.mu.Unlock()
		if changed {
			c.changed()
		}
	}
}

// publishProfiles publishes our display name now and again under every new key, so someone who joins reads it.
func (c *call) publishProfiles() {
	for {
		changed := c.sender.Changed()
		payload, err := encodeProfile(c.svc.displayName())
		if err == nil {
			if g, err := c.profile.NewGroup(); err == nil {
				c.sender.Write(g, profileTrack, payload)
				g.Close()
			}
		}
		select {
		case <-changed:
		case <-c.ctx.Done():
			return
		}
	}
}

// runSpeaker opens the speaker and mixes the other people's audio into it until the call ends.
func (c *call) runSpeaker() {
	out, err := c.svc.openSpeaker()
	if err == nil {
		c.mix.out = out
	}
	c.mix.run(c.ctx, func(err error) { c.svc.speakerLost(err) })
}

func (c *call) String() string { return fmt.Sprintf("call %s (%s)", c.id, c.role) }

// publisher returns the publisher once the call has one, else nil.
func (c *call) publisher() *publisher {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pub
}
