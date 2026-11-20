package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"log"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	invite "github.com/ciranda-dev/kolo-invite"
	kolomedia "github.com/ciranda-dev/kolo-media"
	kolomoq "github.com/ciranda-dev/kolo-moq"
	"github.com/ciranda-dev/kolo-moq/secobj"
	kolosecure "github.com/ciranda-dev/kolo-secure"
	koloswarm "github.com/ciranda-dev/kolo-swarm"
)

// Call states. A call only moves along these edges; see canMove.
const (
	callCreating   = "creating"   // host: starting the relay and the invite
	callJoining    = "joining"    // guest: finding the host and asking to join
	callConnecting = "connecting" // guest: reaching the relay of the call
	callActive     = "active"
	callEnded      = "ended"
)

// Phases of a joining or connecting call, for the sentence under the spinner.
const (
	phaseFindingHost = "finding-host"
	phaseWaitingHost = "waiting-for-host"
	phaseRelay       = "relay"
)

// Roles in a call.
const (
	roleHost  = "host"
	roleGuest = "guest"
)

// Participant states.
const (
	partWaitingKeys = "waiting-keys" // the media is there but this person's key has not arrived: nothing of it plays
	partLive        = "live"
	partStopped     = "media-stopped" // the broadcast ended; the person may still be in the call
)

// transitions lists the edges of the call state machine: from -> to.
var transitions = map[string][]string{
	"":             {callCreating, callJoining},
	callCreating:   {callActive, callEnded},
	callJoining:    {callConnecting, callEnded},
	callConnecting: {callActive, callEnded},
	callActive:     {callEnded},
}

func canMove(from, to string) bool { return slices.Contains(transitions[from], to) }

// endTimeout is the most LeaveCall waits for the teardown. A variable so a test can shorten it.
var endTimeout = 15 * time.Second

// Timings. They are the app's choices, except where a source is named.
const (
	// inviteTTL and maxInviteUses follow the chat app's invites (invite.go: defaultInviteTTL, maxInviteUses).
	inviteTTL     = 24 * time.Hour
	maxInviteUses = 100
	// admitGrace is how long a guest the host let in may take to appear in the call before its place is free again.
	admitGrace = 2 * time.Minute
	// pendingTTL is how long a connection from someone not yet seen in the call is kept: the guest's announcement
	// at the relay may trail its connection by a moment.
	pendingTTL = 30 * time.Second
	// retryEvery is the wait before asking again for a key, a catalog or a profile that was not there yet.
	retryEvery = 500 * time.Millisecond
	// pollCatalog is how often the on/off state of a participant's tracks is read from its catalog.
	pollCatalog = 250 * time.Millisecond
	// barredPerPlace sizes the host's list of people who left or were removed: 4 for each place of the call (32 for
	// the default 8), so people who come back with new invites and leave again still fit.
	barredPerPlace = 4
	// closeShutOut is the code the call's relay closes a connection with when the host does not let that key in: a
	// guest it removed, or one that left and comes back. It is outside kolo-swarm's codes (0 to 5) and kolo-moq's
	// session codes (0x0 to 0x1B), so nothing else closes with it.
	closeShutOut = 0x6b6d
)

// shutWait is the most a guest waits for its relay connection to close after its session failed, to read why. A
// variable so a test can shorten it.
var shutWait = 2 * time.Second

// Participant is one person in the call, as the window shows it.
type Participant struct {
	ID       string `json:"id"` // the lowercase hex of the person's kolo-id key
	Name     string `json:"name"`
	Self     bool   `json:"self"`
	Host     bool   `json:"host"`
	State    string `json:"state"`
	AudioOn  bool   `json:"audioOn"`
	VideoOn  bool   `json:"videoOn"`
	Speaking bool   `json:"speaking"`
	Joined   int64  `json:"joined"` // ms since epoch
	// Link is how this app reaches that person for the keys: "direct", "relayed" (through the connection relay
	// ConnectionRelay, its address) or "" (no connection yet). Media goes through the call's relay either way.
	Link            string `json:"link"`
	ConnectionRelay string `json:"connectionRelay"`
}

// Call is the payload of the "meet:call" event and the answer of Call(): the call as the window shows it. Invite is
// the secret to give to the people to invite; only the main window gets it.
type Call struct {
	ID           string        `json:"id"`
	Role         string        `json:"role"`
	State        string        `json:"state"`
	Phase        string        `json:"phase"`
	Invite       string        `json:"invite"`
	Self         string        `json:"self"`
	Host         string        `json:"host"` // the ID of the host
	Participants []Participant `json:"participants"`
	Max          int           `json:"max"`
	Microphone   MediaState    `json:"microphone"`
	Camera       MediaState    `json:"camera"`
	Speaker      MediaState    `json:"speaker"`
	Security     Security      `json:"security"`
	// Seq grows with every event of the call. A window drops an event of the same call with a lower Seq than the
	// newest it has: events are sent in order, and nothing follows "ended".
	Seq      uint64     `json:"seq"`
	Started  int64      `json:"started"`  // ms since epoch, when the call became active; 0 before
	Finished int64      `json:"finished"` // ms since epoch, 0 while it runs
	Error    *MeetError `json:"error"`    // set in "ended" when the call did not end because the person left
	// Warning is a problem the call runs with: "connection-relay-none-shared" for a guest whose connection relays
	// share none with the host's. Nil when there is none.
	Warning *MeetError `json:"warning"`
	// RelayLink is how a guest reaches the call's relay: "direct", "relayed" (through the connection relay
	// RelayVia) or "" (not yet, or the host, whose relay runs in its own app).
	RelayLink string `json:"relayLink"`
	RelayVia  string `json:"relayVia"`
}

// participant is the call's record of another person.
type participant struct {
	key    ed25519.PublicKey
	id     string
	ns     kolomoq.Namespace
	cancel context.CancelFunc
	ctx    context.Context

	// guarded by call.mu
	name     string
	host     bool
	state    string
	audioOn  bool
	videoOn  bool
	joined   time.Time
	link     *link
	keyErr   error // the last Keyring.Receive on the link, for diagnosis
	keyTries int
	watchErr error // why the last Watch failed

	level     atomic.Uint64 // math.Float64bits of the newest decoded frame's level in dBFS
	speaking  atomic.Bool
	lastAudio atomic.Int64  // unix nanoseconds of the newest decoded audio frame
	audioIn   atomic.Uint64 // audio frames decoded since the watch began
	audioRecv atomic.Uint64 // audio frames that reached the watch's playout, played or not: it restarts with each watch
	videoRecv atomic.Uint64 // the same for video
	missed    atomic.Uint64 // audio frames that did not arrive: the gaps between the frames decoded
	concealed atomic.Uint64 // audio frames made up for them
	videoIn   atomic.Uint64 // video pictures decoded since the watch began

	// for the call quality report
	person     int           // 1 for the first person of the call, then 2, in the order they were first seen: the report names no one
	videoShown atomic.Uint64 // pictures given to the window
	audioBytes atomic.Uint64 // payload bytes of the audio frames decoded
	videoBytes atomic.Uint64
	videoSize  atomic.Uint64 // the newest decoded picture: width in the high 32 bits, height in the low
	decodeNs   atomic.Int64  // mean time to decode a video frame
	freeze     freezeWatch
}

// swarmPeer is what the call uses of a kolo-swarm peer; *koloswarm.Peer satisfies it, and tests have their own.
type swarmPeer interface {
	PublicKey() ed25519.PublicKey
	Topics() []koloswarm.Topic
	Conn() koloswarm.Conn
}

// link is the media session of a pair of participants, on their own connection. It carries keys and nothing else.
type link struct {
	peer   swarmPeer
	sess   *kolomoq.Session
	cancel context.CancelFunc
	kind   string // linkDirect or linkRelayed
	via    string // the connection relay's address when relayed
}

// admission is a guest the host let in: when, and whether it has been seen in the call since.
type admission struct {
	at   time.Time
	seen bool
}

type pendingPeer struct {
	peer swarmPeer
	at   time.Time
}

// call is one call, from its start to its end. A MeetService has at most one.
type call struct {
	svc    *MeetService
	id     string
	role   string
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{} // closed when the teardown is over
	setup  chan struct{} // closed when the goroutine that builds the call (runHost or runGuest) returned
	wg     sync.WaitGroup
	me     ed25519.PublicKey
	selfID string

	began   time.Time // when the call was made, the zero of the quality report's times
	q       callQuality
	persons int // people given a number in the quality report, guarded by mu

	mu          sync.Mutex
	state       string
	phase       string
	inviteStr   string
	room        *room
	people      map[string]*participant
	order       []string
	pending     map[string]pendingPeer
	admitted    map[string]*admission  // host: keys let in
	barred      map[string][]invite.ID // host: keys that left or were removed, with the invites that no longer let them in
	barOrder    []string               // host: the barred keys, oldest first
	peak        int
	started     time.Time
	finished    time.Time
	endErr      *MeetError
	ending      bool
	lastSpeakID string // "self", a participant ID or ""
	notchShown  bool
	endedSnap   *Call
	seq         uint64     // the Seq of the last event of the call, guarded by mu
	emitMu      sync.Mutex // one event of the call at a time, snapshot and send together
	emitted     bool       // the final event was sent: no other follows

	// guest: the invite being used and the peers it waits for
	code      invite.Code
	joinPeer  chan swarmPeer
	relayPeer chan swarmPeer
	// guest, guarded by mu: the swarm said the inviter or the call's relay is unreachable; the warning about the
	// connection relays; how the call's relay is reached
	noInviter  bool
	noRelay    bool
	warning    *MeetError
	relayLink  string
	relayVia   string
	relayShare bool

	// the participant's own side
	origin     *kolomoq.Origin
	sender     *secobj.Sender
	bc         *kolomedia.Broadcast
	keys       *secobj.Keyring
	relayMu    sync.RWMutex // guards relaySess, relayConn and relayNew
	relaySess  *kolomoq.Session
	relayConn  kolomoq.Conn  // the connection under relaySess
	relayNew   chan struct{} // closed when relaySess is replaced
	relayReset chan error    // the watchdogs ask for a new session to the relay, with the reason
	pub        *publisher
	mix        *mixer
	profile    *kolomoq.TrackWriter
	memberMu   sync.Mutex   // one SetMembers at a time, in the order of the roster
	others     atomic.Int32 // people in the call but us, as last given to SetMembers: the publisher sizes video by it

	// host: the relay and the invite
	grant         []byte
	relaySessions map[string]relayPeer // host: the sessions of the guests, by key in hex
	relay         *kolomoq.Relay
	relID         ed25519.PublicKey
	ln            *kolosecure.Listener
	rsw           *koloswarm.Swarm // the relay, for the people let in
	isw           *koloswarm.Swarm // the invite, for anyone
	inv           *invite.Inviter
	invDB         interface{ Close() error }
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func newCall(s *MeetService, role string) *call {
	ctx, cancel := context.WithCancel(s.ctx)
	c := &call{svc: s, id: newID(), role: role, began: time.Now(), ctx: ctx, cancel: cancel, done: make(chan struct{}), setup: make(chan struct{}), me: s.id.PublicKey(),
		people: map[string]*participant{}, pending: map[string]pendingPeer{}, admitted: map[string]*admission{},
		joinPeer: make(chan swarmPeer, 1), relayPeer: make(chan swarmPeer, 1), relayNew: make(chan struct{}), relayReset: make(chan error, 1)}
	c.selfID = hex.EncodeToString(c.me)
	return c
}

// move changes the state if the edge exists, and reports whether it did.
func (c *call) move(to, phase string) bool {
	c.mu.Lock()
	ok := canMove(c.state, to)
	if ok {
		c.state, c.phase = to, phase
		if to == callActive {
			c.started = time.Now()
		}
	}
	c.mu.Unlock()
	if !ok {
		log.Printf("call %s: no edge from %q to %q", c.id, c.state, to)
		return false
	}
	c.changed()
	return true
}

// setPhase changes the phase without leaving the state.
func (c *call) setPhase(phase string) {
	c.mu.Lock()
	changed := c.phase != phase
	c.phase = phase
	c.mu.Unlock()
	if changed {
		c.changed()
	}
}

// changed tells the service, which tells the windows.
func (c *call) changed() { c.svc.callChanged(c) }

// end ends the call once, with err as the reason (nil: the person left). It does not wait; the supervisor tears the
// call down. It is safe to call from any goroutine of the call.
func (c *call) end(err *MeetError) {
	c.mu.Lock()
	first := !c.ending
	if first {
		c.ending, c.endErr = true, err
	}
	c.mu.Unlock()
	if first && err != nil {
		c.logf("ends: %s (%s)", err.Reason, err.Cause)
	}
	c.cancel()
}

// supervise waits for the end and tears the call down.
func (c *call) supervise() {
	<-c.ctx.Done()
	c.teardown()
}

// snapshot is the call as the window shows it; main says whether this window may have the invite.
func (c *call) snapshot(main bool) Call {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked(main)
}

func (c *call) snapshotLocked(main bool) Call {
	if c.endedSnap != nil {
		out := *c.endedSnap
		if !main {
			out.Invite = ""
		}
		return out
	}
	out := Call{Seq: c.seq, ID: c.id, Role: c.role, State: c.state, Phase: c.phase, Self: c.selfID, Error: c.endErr, Max: c.svc.maxParticipants(),
		Warning: c.warning, RelayLink: c.relayLink, RelayVia: c.relayVia}
	if c.room != nil {
		out.Max = c.room.max
	}
	if main {
		out.Invite = c.inviteStr
	}
	if !c.started.IsZero() {
		out.Started = c.started.UnixMilli()
	}
	if !c.finished.IsZero() {
		out.Finished = c.finished.UnixMilli()
	}
	s := c.svc
	out.Microphone, out.Camera, out.Speaker = s.mediaStates()
	self := Participant{ID: c.selfID, Name: s.displayName(), Self: true, Host: c.role == roleHost, State: partLive,
		AudioOn: out.Microphone.State == "on", VideoOn: out.Camera.State == "on", Speaking: s.media.speaking.Load()}
	if c.room != nil {
		out.Host = hex.EncodeToString(c.room.host)
	} else if c.role == roleHost {
		out.Host = c.selfID
	}
	self.Joined = c.joinedMs()
	out.Participants = []Participant{self}
	waiting := 0
	for _, id := range c.order {
		p := c.people[id]
		if p == nil {
			continue
		}
		if p.state == partWaitingKeys {
			waiting++
		}
		part := Participant{ID: p.id, Name: p.name, Host: p.host, State: p.state,
			AudioOn: p.audioOn, VideoOn: p.videoOn, Speaking: p.speaking.Load(), Joined: p.joined.UnixMilli()}
		if p.link != nil {
			part.Link, part.ConnectionRelay = p.link.kind, p.link.via
		}
		out.Participants = append(out.Participants, part)
	}
	if out.State == callActive || out.State == callEnded {
		operator, you := c.operatorLocked()
		out.Security = securityOf(len(out.Participants), waiting, operator, you, len(s.settingsCopy().relays))
	}
	return out
}

// joinedMs is when this person entered the call, for ordering.
func (c *call) joinedMs() int64 {
	if !c.started.IsZero() {
		return c.started.UnixMilli()
	}
	return 0
}

// operatorLocked is the display name of the person who runs the relay, and whether that is this person.
func (c *call) operatorLocked() (string, bool) {
	if c.role == roleHost {
		return c.svc.displayName(), true
	}
	if c.room != nil {
		if p := c.people[hex.EncodeToString(c.room.host)]; p != nil {
			return p.name, false
		}
		return c.room.hostName, false
	}
	return "", false
}

// teardown stops everything the call started, in the order that keeps a leaver's keys from being used again, and
// reports the end.
func (c *call) teardown() {
	if h := c.svc.teardownHook; h != nil {
		h()
	}
	<-c.setup // the fields the setup fills are read below: let it finish; it stops at once, everything in it takes c.ctx
	c.svc.media.setSink(nil)
	if c.inv != nil {
		if info, err := c.inv.List(); err == nil {
			for _, i := range info {
				if i.State == invite.Active {
					c.inv.Revoke(i.ID)
				}
			}
		}
	}
	if sess := c.session(); sess != nil {
		sess.Close()
	}
	c.mu.Lock()
	links := make([]*link, 0, len(c.people))
	for _, p := range c.people {
		if p.link != nil {
			links = append(links, p.link)
		}
		p.cancel()
	}
	c.mu.Unlock()
	for _, l := range links {
		l.sess.Close()
	}
	c.eraseKeys()
	if c.pub != nil {
		c.pub.close()
	}
	if c.profile != nil {
		c.profile.Close()
	}
	if c.bc != nil {
		c.bc.Close()
	}
	if c.sender != nil {
		c.sender.Close()
	}
	if c.room != nil {
		c.svc.leaveTopic(c.room.topic)
	}
	if c.inv != nil {
		c.inv.Close()
	}
	if c.invDB != nil {
		c.invDB.Close()
	}
	if c.rsw != nil {
		c.rsw.Close()
	}
	if c.isw != nil {
		c.isw.Close()
	}
	if c.ln != nil {
		c.ln.Close()
	}
	c.wg.Wait()
	c.eraseKeys() // again: a key message that was in flight when the links closed
	if c.rsw != nil {
		c.svc.forgetRelays(swarmCallRelay) // after the relay loop ended, so nothing it read comes later
	}
	if c.isw != nil {
		c.svc.forgetRelays(swarmCallInvite)
	}

	c.mu.Lock()
	c.state, c.phase, c.finished = callEnded, "", time.Now()
	snap := c.snapshotLocked(true)
	c.endedSnap = &snap
	c.mu.Unlock()
	c.svc.callEnded(c, snap)
	close(c.done)
}

// eraseKeys makes the keyring forget the keys of every participant: they are overwritten, as far as Go allows, and an
// object sealed under them no longer opens.
func (c *call) eraseKeys() {
	if c.keys == nil {
		return
	}
	c.mu.Lock()
	nss := make([]kolomoq.Namespace, 0, len(c.people))
	for _, p := range c.people {
		nss = append(nss, p.ns)
	}
	c.mu.Unlock()
	for _, ns := range nss {
		c.keys.Remove(ns)
	}
}

// admit records that the host let key in, which also lifts a bar: only a newer invite gets here for a barred key.
func (c *call) admit(key ed25519.PublicKey) {
	c.mu.Lock()
	c.admitted[string(key)] = &admission{at: time.Now()}
	c.unbarLocked(string(key))
	c.mu.Unlock()
}

// shut takes key's admission away and bars it from invites, those that exist now. An invite answers a guest it
// accepted before without asking the host, so the relay is what keeps that guest out. The newest barredPerPlace keys
// per place are kept.
func (c *call) shut(key ed25519.PublicKey, invites []invite.ID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := string(key)
	delete(c.admitted, k)
	c.unbarLocked(k)
	if c.barred == nil {
		c.barred = map[string][]invite.ID{}
	}
	c.barred[k] = invites
	c.barOrder = append(c.barOrder, k)
	for len(c.barOrder) > barredPerPlace*c.room.max {
		delete(c.barred, c.barOrder[0])
		c.barOrder = c.barOrder[1:]
	}
}

func (c *call) unbarLocked(k string) {
	if _, ok := c.barred[k]; ok {
		delete(c.barred, k)
		c.barOrder = slices.DeleteFunc(c.barOrder, func(x string) bool { return x == k })
	}
}

// isBarred reports whether key left or was removed and has not been let in again.
func (c *call) isBarred(key ed25519.PublicKey) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.barred[string(key)]
	return ok
}

// letIn is the host's answer to a request to join with invite id: never itself, nobody barred from that invite, and
// otherwise a guest already let in, or anyone while there is a place.
func (c *call) letIn(key ed25519.PublicKey, id invite.ID) bool {
	if bytes.Equal(key, c.me) {
		return false
	}
	c.mu.Lock()
	_, again := c.admitted[string(key)]
	barred := slices.Contains(c.barred[string(key)], id)
	c.mu.Unlock()
	return !barred && (again || c.freeSlots() > 0)
}

func (c *call) forget(key ed25519.PublicKey) {
	c.mu.Lock()
	delete(c.admitted, string(key))
	c.mu.Unlock()
}

// isMember reports whether key may use the relay: the host, and the guests it let in while the call has a place for
// them. A guest already in the call keeps its place.
func (c *call) isMember(key ed25519.PublicKey) bool {
	if bytes.Equal(key, c.me) {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.admitted[string(key)]; !ok {
		return false
	}
	if c.people[hex.EncodeToString(key)] != nil {
		return true
	}
	return c.seatsLocked(key) > 0
}

// freeSlots is how many more people the host can let in.
func (c *call) freeSlots() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seatsLocked(nil)
}

// seatsLocked counts the places left in a call of room.max people, the host included, not counting a guest that
// is in the call, nor skip: a place is taken by every guest in the call and by every guest let in less than
// admitGrace ago that has not appeared yet.
func (c *call) seatsLocked(skip ed25519.PublicKey) int {
	taken := 1 + len(c.people)
	for k, a := range c.admitted {
		if !a.seen && k != string(skip) && time.Since(a.at) < admitGrace {
			taken++
		}
	}
	return c.room.max - taken
}

// roomRelays lists the relay identities a Keyring accepts.
func (c *call) roomRelays() []ed25519.PublicKey { return []ed25519.PublicKey{c.room.relay} }

// logf logs without keys or invites: only the call's own ID and a message.
func (c *call) logf(format string, args ...any) {
	log.Printf("call "+c.id+": "+format, args...)
}
