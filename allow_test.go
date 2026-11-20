package main

import (
	"bytes"
	"crypto/ed25519"
	"net/netip"
	"testing"

	invite "github.com/ciranda-dev/kolo-invite"
)

func TestSwarmTransportStreams(t *testing.T) {
	s := NewMeetService(Config{}, nil)
	for which, streams := range map[string]int{swarmMember: maxStreams, swarmCallRelay: maxStreams, swarmCallInvite: inviteStreams} {
		if got := s.swarmTransport(which).MaxIncomingStreams; got != streams {
			t.Errorf("%s: %d incoming streams, want %d", which, got, streams)
		}
	}
}

func TestRelayAllowAdmitsOnlyPeopleLetIn(t *testing.T) {
	me, in, barred, stranger := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32)
	c := &call{me: me, admitted: map[string]*admission{string(in): {}}, barred: map[string][]invite.ID{string(barred): nil}}
	for name, tc := range map[string]struct {
		key         ed25519.PublicKey
		relay, invi bool
	}{
		"the host's own app": {me, false, false},
		"let in":             {in, true, true},
		"barred":             {barred, true, true},
		"stranger":           {stranger, false, true},
	} {
		if got := c.relayAllow(tc.key, netip.AddrPort{}); got != tc.relay {
			t.Errorf("%s: relay swarm admits %v, want %v", name, got, tc.relay)
		}
		if got := c.inviteAllow(tc.key, netip.AddrPort{}); got != tc.invi {
			t.Errorf("%s: invite swarm admits %v, want %v", name, got, tc.invi)
		}
	}
}
