package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"log"
	"net/netip"
	"slices"
	"time"

	koloid "github.com/ciranda-dev/kolo-id"
	kolorelay "github.com/ciranda-dev/kolo-relay"
	kolosecure "github.com/ciranda-dev/kolo-secure"
	koloswarm "github.com/ciranda-dev/kolo-swarm"
	kolotransport "github.com/ciranda-dev/kolo-transport"
)

// A "connection relay" is a kolo-relay from the settings that carries a connection when a direct one fails (decision
// 0013). The word "relay" alone is the host's kolo-moq relay of a call.

// The swarms of the app: every participant's, and the two a host's call runs, one that serves the invite to anyone
// and one that serves the call's relay to the people let in; and the check of each relay's key at start.
const (
	swarmMember     = "member"
	swarmCallInvite = "call-invite"
	swarmCallRelay  = "call-relay"
	relayCheck      = "check"
)

// relayCheckTimeout bounds the check of one relay's key at start.
const relayCheckTimeout = 30 * time.Second

// States of a connection relay, as the window shows them.
const (
	relayIdle        = "idle"         // no call: the app holds a place at the relays only while it is in a call
	relayReserved    = "reserved"     // people who cannot connect to you directly can reach you through it
	relayNotReserved = "not-reserved" // in a call, but the relay gave no place; Error says why
)

// Links between two apps, as the window shows them.
const (
	linkDirect  = "direct"
	linkRelayed = "relayed"
)

// RelayStatus is one connection relay in the "meet:network" event.
type RelayStatus struct {
	Addr  string     `json:"addr"`
	Key   string     `json:"key"` // the relay's kolo-id key in hex
	State string     `json:"state"`
	Error *MeetError `json:"error"` // why it is not reserved; nil otherwise
}

// relayState is what one swarm last said about one relay.
type relayState struct {
	state string
	err   *MeetError
}

// swarmTransport is the kolo-transport setting of one of the app's swarms, with the congestion controller of
// -congestion: each end paces what it sends, so every swarm that carries a call carries it. The host's invite swarm
// takes the streams of a stranger, the others those of a person in the call. acceptHook, a test seam, can refuse
// connections to it by source address.
func (s *MeetService) swarmTransport(which string) *kolotransport.Config {
	c := transportConfig()
	if which == swarmCallInvite {
		c = inviteTransport()
	}
	c.CongestionControl = s.settingsCopy().Congestion
	if h := s.acceptHook; h != nil {
		c.AcceptFunc = func(a netip.AddrPort) bool { return h(which, a) }
	}
	return c
}

// relayChanged records a RelayChanged event of one swarm, logs an error the first time it happens with that relay
// and tells the window when a state changed.
func (s *MeetService) relayChanged(which string, ev koloswarm.Event) {
	if errors.Is(ev.Err, kolorelay.ErrReservationEnded) {
		return // a place ends with the relay's lifetime and kolo-swarm takes a new one: not a failure
	}
	addr := s.relayAddr(ev.PublicKey)
	st := relayState{state: relayReserved}
	switch {
	case ev.Err == nil:
	case errors.Is(ev.Err, koloswarm.ErrClosed):
		st.state = relayIdle
	default:
		st = relayState{state: relayNotReserved, err: relayError(ev.Err, addr, which == swarmCallRelay || which == swarmCallInvite)}
	}
	s.relayMu.Lock()
	if s.relayStates == nil {
		s.relayStates, s.relayLogged = map[string]map[string]relayState{}, map[string]bool{}
	}
	if s.relayStates[which] == nil {
		s.relayStates[which] = map[string]relayState{}
	}
	s.relayStates[which][string(ev.PublicKey)] = st
	logIt := false
	if st.err != nil {
		seen := string(ev.PublicKey) + st.err.Reason
		logIt, s.relayLogged[seen] = !s.relayLogged[seen], true
	}
	s.relayMu.Unlock()
	if logIt {
		log.Print("connection relay ", addr, ": ", st.err.Message)
	}
	s.pushRelays()
}

// forgetRelays drops what a swarm said, when it closes.
func (s *MeetService) forgetRelays(which string) {
	s.relayMu.Lock()
	delete(s.relayStates, which)
	s.relayMu.Unlock()
	s.pushRelays()
}

// checkRelays proves each relay's key with one handshake, as a stranger with a throwaway key, and reports a relay
// that answers with another key. kolo-swarm reports that only for a place, which the app holds only in a call, so a
// guest would otherwise learn it after the whole find timeout. It runs whenever the network starts: at start and at
// each Reconnect.
func (s *MeetService) checkRelays(ctx context.Context) {
	for _, r := range s.settingsCopy().relays {
		s.netWG.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, relayCheckTimeout)
			defer cancel()
			if err := checkRelay(ctx, r); errors.Is(err, kolosecure.ErrUnexpectedPeer) {
				s.relayChanged(relayCheck, koloswarm.Event{Kind: koloswarm.RelayChanged, PublicKey: r.PublicKey, Err: err})
			}
		})
	}
}

// checkRelay proves the relay's key and asks it for nothing: anything past the handshake is the relay's doing, not
// the key the person typed.
func checkRelay(ctx context.Context, r koloswarm.Relay) error {
	me, err := koloid.Generate()
	if err != nil {
		return err
	}
	conn, err := kolosecure.Dial(ctx, r.Addr, me, r.PublicKey, nil)
	if err == nil {
		conn.Close()
	}
	return err
}

// relayError is the reason a relay holds no place for this app. The call relay's key is made for its call, so no
// allow list names it.
func relayError(err error, addr string, callRelay bool) *MeetError {
	switch {
	case errors.Is(err, kolorelay.ErrNotAllowed) && callRelay:
		return meetErr("connection-relay-call-denied", err, addr)
	case errors.Is(err, kolorelay.ErrNotAllowed):
		return meetErr("connection-relay-not-allowed", err, addr)
	case errors.Is(err, kolorelay.ErrLimit):
		return meetErr("connection-relay-full", err, addr)
	case errors.Is(err, kolosecure.ErrUnexpectedPeer):
		return meetErr("connection-relay-wrong-key", err, addr)
	case errors.Is(err, koloswarm.ErrInvalidConfig):
		return meetErr("connection-relay-bad-key", err, addr)
	}
	return meetErr("connection-relay-unreachable", err, addr)
}

// relayStatuses is every listed relay with the worst state any running swarm, or the key check, reported for it: a
// host in a call holds a place for itself, one for its invite and one for its call relay, and all are needed.
func (s *MeetService) relayStatuses() []RelayStatus {
	relays := s.settingsCopy().relays
	s.relayMu.Lock()
	defer s.relayMu.Unlock()
	out := make([]RelayStatus, 0, len(relays))
	for _, r := range relays {
		st := RelayStatus{Addr: r.Addr, Key: hex.EncodeToString(r.PublicKey), State: relayIdle}
		for _, which := range []string{swarmMember, swarmCallInvite, swarmCallRelay, relayCheck} {
			x, ok := s.relayStates[which][string(r.PublicKey)]
			switch {
			case !ok || x.state == relayIdle:
			case x.state == relayNotReserved:
				st.State, st.Error = relayNotReserved, x.err
			case st.State == relayIdle:
				st.State = relayReserved
			}
		}
		out = append(out, st)
	}
	return out
}

// relayAddr is the address of the listed relay with key, or "" if none is listed with it.
func (s *MeetService) relayAddr(key ed25519.PublicKey) string {
	for _, r := range s.settingsCopy().relays {
		if bytes.Equal(r.PublicKey, key) {
			return r.Addr
		}
	}
	return ""
}

// relayKeys is the hex keys of the listed relays, for the grant.
func relayKeys(relays []koloswarm.Relay) []string {
	out := make([]string, len(relays))
	for i, r := range relays {
		out[i] = hex.EncodeToString(r.PublicKey)
	}
	return out
}

// shareRelay reports whether a relay of mine has a key in theirs.
func shareRelay(mine []koloswarm.Relay, theirs [][]byte) bool {
	for _, r := range mine {
		for _, k := range theirs {
			if bytes.Equal(r.PublicKey, k) {
				return true
			}
		}
	}
	return false
}

// relayMismatch is the warning of a guest whose relays share no key with the host's: a pair that cannot connect
// directly can only meet through a relay both list. Nobody listing a relay is the default and gets no warning.
func relayMismatch(mine []koloswarm.Relay, host [][]byte) *MeetError {
	if (len(mine) == 0 && len(host) == 0) || shareRelay(mine, host) {
		return nil
	}
	return meetErr("connection-relay-none-shared", nil, "")
}

// linkOf says how a connection reaches the other app: directly, or through the connection relay at via.
func (s *MeetService) linkOf(c koloswarm.Conn) (kind, via string) {
	if r, ok := c.(interface{ RelayPublicKey() ed25519.PublicKey }); ok {
		return linkRelayed, s.relayAddr(r.RelayPublicKey())
	}
	return linkDirect, ""
}

// sameRelays reports whether the window already shows these states.
func sameRelays(a, b []RelayStatus) bool {
	return slices.EqualFunc(a, b, func(x, y RelayStatus) bool {
		return x.Addr == y.Addr && x.State == y.State && (x.Error == nil) == (y.Error == nil) && (x.Error == nil || x.Error.Reason == y.Error.Reason)
	})
}

// hostUnreachable is why a guest could not connect to the host's app; with connection relays, they failed too.
func hostUnreachable(relays bool) *MeetError {
	if relays {
		return meetErr("host-unreachable-relays", ErrNotFound, "")
	}
	return meetErr("host-unreachable", ErrNotFound, "")
}

// relayWaitReason is why a guest got no connection to the call's relay in time. unreachable says the swarm reported
// it unreachable (directly and through the connection relays); shared says this guest and the host list a common
// connection relay.
func relayWaitReason(unreachable, shared bool) *MeetError {
	switch {
	case !unreachable:
		return meetErr("relay-unreachable", ErrNotFound, "")
	case !shared:
		return meetErr("relay-no-common", ErrNotFound, "")
	}
	return hostUnreachable(true)
}

// unreachable records that the swarm could not connect to key, directly or through a connection relay, if it is the
// inviter or the call's relay this guest waits for.
func (c *call) unreachable(key ed25519.PublicKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.role != roleGuest {
		return
	}
	switch {
	case c.state == callJoining && c.code.Matches(key):
		c.noInviter = true
	case c.state == callConnecting && c.room != nil && bytes.Equal(key, c.room.relay):
		c.noRelay = true
	}
}
