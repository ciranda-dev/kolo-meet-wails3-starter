package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"

	kolomoq "github.com/ciranda-dev/kolo-moq"
	koloswarm "github.com/ciranda-dev/kolo-swarm"
)

const (
	grantVersion = 1
	// maxGrantBytes: a grant is about 340 bytes, plus 67 for each of at most 16 connection relay keys (kolo-swarm
	// Config.Relays holds at most 16); anything larger is not one.
	maxGrantBytes = 2048
	maxRelayKeys  = 16
	topicLabel    = "kolo-meet/topic/v0"
)

var (
	hex16 = regexp.MustCompile(`^[0-9a-f]{32}$`)
	hex32 = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// grant is the payload of an accept: what a guest needs to enter the call. The invite carries it sealed for the
// guest alone. Seed derives the swarm topic of the call; Relay is the identity of the host's relay; Relays are the
// keys of the host's connection relays, so a guest can tell whether it lists one of them.
type grant struct {
	Version  int      `json:"v"`
	Room     string   `json:"room"`
	Seed     string   `json:"seed"`
	Relay    string   `json:"relay"`
	Host     string   `json:"host"`
	HostName string   `json:"hostName"`
	Max      int      `json:"max"`
	Relays   []string `json:"relays"`
}

// room is a grant after checking.
type room struct {
	id       string // opaque room ID, the first field of every namespace of the call
	topic    koloswarm.Topic
	relay    ed25519.PublicKey
	host     ed25519.PublicKey
	hostName string
	max      int
	// relayKeys are the keys of the host's connection relays.
	relayKeys [][]byte
}

// roomTopic is the swarm topic of a call: a hash of the seed, so the network never sees the seed.
func roomTopic(seed []byte) koloswarm.Topic {
	h := sha256.New()
	h.Write([]byte(topicLabel))
	h.Write(seed)
	return koloswarm.Topic(h.Sum(nil))
}

// memberNS is the namespace of a participant's broadcast: the room and the lowercase hex of its key.
func (r *room) memberNS(key ed25519.PublicKey) kolomoq.Namespace {
	return kolomoq.Namespace{r.id, hex.EncodeToString(key)}
}

// owns reports whether ns is the namespace of peer, the rule of kolo-media's Keyring and of the relay's RoomPolicy.
func (r *room) owns(peer ed25519.PublicKey, ns kolomoq.Namespace) bool {
	return len(peer) == ed25519.PublicKeySize && len(ns) == 2 && ns[0] == r.id && ns[1] == hex.EncodeToString(peer)
}

// encodeGrant is the payload the host gives every guest; relays are the hex keys of its connection relays.
func encodeGrant(id string, seed []byte, relay, host ed25519.PublicKey, hostName string, max int, relays []string) ([]byte, error) {
	return json.Marshal(grant{Version: grantVersion, Room: id, Seed: hex.EncodeToString(seed), Relay: hex.EncodeToString(relay),
		Host: hex.EncodeToString(host), HostName: hostName, Max: max, Relays: relays})
}

// parseGrant checks the payload of an accept. The invite is served under a key of its own, never the relay's, so that
// a connection that asks to join is not a media session. It never panics on bad input: anything wrong is ErrBadGrant.
func parseGrant(b []byte) (*room, error) {
	if len(b) > maxGrantBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrBadGrant, len(b))
	}
	var g grant
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&g); err != nil {
		return nil, fmt.Errorf("%w: not a grant", ErrBadGrant)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("%w: data after the grant", ErrBadGrant)
	}
	if g.Version != grantVersion {
		return nil, fmt.Errorf("%w: version %d", ErrBadGrant, g.Version)
	}
	if !hex16.MatchString(g.Room) || !hex32.MatchString(g.Seed) || !hex32.MatchString(g.Relay) || !hex32.MatchString(g.Host) {
		return nil, fmt.Errorf("%w: malformed field", ErrBadGrant)
	}
	if g.Max < 2 || g.Max > maxParticipantsCeiling {
		return nil, fmt.Errorf("%w: participant limit %d", ErrBadGrant, g.Max)
	}
	if len(g.Relays) > maxRelayKeys {
		return nil, fmt.Errorf("%w: %d connection relays", ErrBadGrant, len(g.Relays))
	}
	var relayKeys [][]byte
	for i, k := range g.Relays {
		if !hex32.MatchString(k) || slices.Contains(g.Relays[:i], k) {
			return nil, fmt.Errorf("%w: connection relay %d", ErrBadGrant, i+1)
		}
		b, _ := hex.DecodeString(k)
		relayKeys = append(relayKeys, b)
	}
	seed, _ := hex.DecodeString(g.Seed)
	relay, _ := hex.DecodeString(g.Relay)
	host, _ := hex.DecodeString(g.Host)
	if bytes.Equal(relay, host) {
		return nil, fmt.Errorf("%w: identities", ErrBadGrant)
	}
	return &room{id: g.Room, topic: roomTopic(seed), relay: relay, host: host, hostName: peerName(g.HostName, host), max: g.Max,
		relayKeys: relayKeys}, nil
}

// profile is the small data track every participant publishes: its display name, sealed like its media.
type profile struct {
	Version int    `json:"v"`
	Name    string `json:"name"`
}

const (
	profileVersion  = 1
	maxProfileBytes = 512 // 48 characters of up to 4 bytes, escaped, fit with room to spare
	profileTrack    = "profile"
)

// encodeProfile makes the profile object: JSON of the name padded with blanks to maxProfileBytes, so every profile
// has the same size and the relay does not learn the length of a name from the size of the sealed object.
func encodeProfile(name string) ([]byte, error) {
	b, err := json.Marshal(profile{Version: profileVersion, Name: name})
	if err != nil {
		return nil, err
	}
	if len(b) > maxProfileBytes {
		return nil, fmt.Errorf("%w: profile of %d bytes", ErrBadName, len(b))
	}
	return append(b, bytes.Repeat([]byte{' '}, maxProfileBytes-len(b))...), nil
}

// parseProfile reads the display name of a profile object from a peer. It never panics on bad input.
func parseProfile(b []byte, key ed25519.PublicKey) (string, error) {
	if len(b) > maxProfileBytes {
		return "", fmt.Errorf("%w: profile of %d bytes", ErrBadGrant, len(b))
	}
	var p profile
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil || p.Version != profileVersion {
		return "", fmt.Errorf("%w: not a profile", ErrBadGrant)
	}
	if _, err := dec.Token(); err != io.EOF {
		return "", fmt.Errorf("%w: data after the profile", ErrBadGrant)
	}
	return peerName(p.Name, key), nil
}
