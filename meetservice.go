package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io/fs"
	"log"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	kolodht "github.com/ciranda-dev/kolo-dht"
	koloid "github.com/ciranda-dev/kolo-id"
	invite "github.com/ciranda-dev/kolo-invite"
	kolosecure "github.com/ciranda-dev/kolo-secure"
	koloswarm "github.com/ciranda-dev/kolo-swarm"
	"github.com/ciranda-dev/kolo-video/vpx"
)

// dialRetryMax is the longest wait before the swarm dials a peer again. kolo-swarm's default is 10 minutes, right for
// a background swarm; in a call a peer that was not reachable a moment ago must be tried again within seconds.
// Its range is RetryMin (1 s) to 1 h (kolo-swarm config.go:46-47).
const dialRetryMax = 10 * time.Second

// Network states.
const (
	netStarting = "starting"
	netReady    = "ready"
	netFailed   = "failed"
)

// Info is what Start returns.
type Info struct {
	DataDir string  `json:"dataDir"`
	Version string  `json:"version"`
	Network Network `json:"network"`
	// KeyID is the public key other people's apps know this one by, in hex. It is not a secret.
	KeyID string `json:"keyId"`
}

// Network is the payload of the "meet:network" event.
type Network struct {
	State   string     `json:"state"`
	Message string     `json:"message"`
	Error   *MeetError `json:"error"` // the reason of Message, to translate it; nil when Message is ""
	// Mode is the network in use: "public", "own" or "local".
	Mode       string `json:"mode"`
	EntryNodes int    `json:"entryNodes"`
	FirstNode  string `json:"firstNode"`
	// Relays is every connection relay of the settings, in their order, with its state.
	Relays []RelayStatus `json:"relays"`
	// Seq grows with every state sent, so the window keeps the newest of an event and the snapshot of Start or Info.
	Seq uint64 `json:"seq"`
}

// Lobby is the payload of the "meet:lobby" event: the devices before and between calls. A device that is off is
// closed.
type Lobby struct {
	Microphone MediaState `json:"microphone"`
	Camera     MediaState `json:"camera"`
	Speaker    MediaState `json:"speaker"`
}

// MeetService is the Go side of the window: it makes and joins calls. Every method is safe for concurrent use, and
// the window learns what happens through emit.
type MeetService struct {
	cfg Config
	// emit sends an event to the main window only: it carries invites.
	emit func(name string, data any)
	// emitNotch sends an event to the notch window only; nil: no notch window. It never gets an invite.
	emitNotch func(name string, data any)
	notch     notchWindow // nil: no notch window
	openApp   func()      // brings the main window to the front
	copyText  func(text string)
	readText  func() (string, bool) // the clipboard's text; nil: it cannot be read, so an invite is never cleared
	tray      trayUI                // nil: no tray icon
	hw        hardware
	vid       videoMode

	// Test hooks: an identity kept in memory instead of the identity file, and a hook that holds back the keys of
	// some participants, to play a peer whose keys never arrive.
	identity *koloid.Identity
	holdKeys func(ed25519.PublicKey) bool

	startMu sync.Mutex
	started bool
	set     settings
	id      *koloid.Identity
	hist    *history
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	media   *localMedia
	tiles   tileBudget     // the picture sizes of the window, shared by the self-view and every participant
	quality *qualityWriter // the call quality report; nil: off

	netMu     sync.Mutex // one start or stop of the network at a time
	netCancel context.CancelFunc
	netWG     sync.WaitGroup

	trayMu sync.Mutex // one tray update at a time, so they arrive in order

	micAskMu    sync.Mutex // one microphone permission prompt at a time
	micCancelMu sync.Mutex
	micCancel   context.CancelFunc // ends the prompt that waits, if any

	prefMu sync.RWMutex // guards saved and the device errors; held only briefly, never while calling out
	saved  savedSettings
	micErr *MeetError
	camErr *MeetError
	spkErr *MeetError
	spkOn  bool

	mu           sync.Mutex // guards the fields below
	net          Network
	sw           *koloswarm.Swarm
	node         *kolodht.Node
	call         *call
	lastEnded    *Call  // the call that ended last, for the notch
	copiedInvite string // the invite CopyInvite last put on the clipboard
	lastEndAt    time.Time
	notchTimer   *time.Timer
	lastTray     TrayState
	restarting   bool
	running      bool // between Start and ServiceShutdown: calls may start

	notchMu   sync.Mutex // one send to the notch window at a time, with the setting read as it sends
	notchHook func()     // test hook, run between reading the notch state and sending it
	// emitHook is a test hook run between a call's snapshot and its event. A test sets it during a call, while the
	// call's goroutines send changes, so it is atomic.
	emitHook atomic.Pointer[func()]
	// teardownHook is a test hook run at the start of a call's teardown.
	teardownHook func()
	// requestHook is a test hook that sees every request to join that reaches a host.
	requestHook func(invite.Event)
	// acceptHook is a test seam: when set, a swarm (swarmMember or swarmCallRelay) accepts a connection only from a
	// source address it allows.
	acceptHook func(swarm string, remote netip.AddrPort) bool
	// dialHook is a test seam: when set, the member swarm dials or admits a key only if it allows it.
	dialHook func(key ed25519.PublicKey, addr netip.AddrPort) bool

	netEmitMu   sync.Mutex // one "meet:network" at a time, built and sent together, so they arrive in order
	relayMu     sync.Mutex
	relayStates map[string]map[string]relayState // swarm, then relay key: what that swarm last said
	relayLogged map[string]bool                  // relay key and reason: logged once for the app's run
}

// NewMeetService returns a service that reads cfg when it starts and tells the main window what happens through
// emit. The notch window gets its own events, through emitNotch.
func NewMeetService(cfg Config, emit func(name string, data any)) *MeetService {
	return &MeetService{cfg: cfg, emit: emit, hw: realHardware{}, vid: defaultVideo}
}

// Start opens the data directory and the identity and starts the network in the background. It fails with a
// *MeetError of kind "config" for a bad setting. A second call returns the same Info.
func (s *MeetService) Start() (Info, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if !s.started {
		if err := s.start(); err != nil {
			return Info{}, configError(err)
		}
		s.started = true
		s.pushTray()
	}
	return s.Info(), nil
}

func configSetting(ce *ConfigError) *MeetError {
	cause := ce.Err.Error()
	return &MeetError{Kind: kindConfig, Reason: "config-setting", Detail: ce.Setting, Cause: cause, Message: sentence("config-setting", ce.Setting, cause), Err: ce}
}

func configError(err error) error {
	var ce *ConfigError
	var me *MeetError
	switch {
	case errors.As(err, &ce):
		return configSetting(ce)
	case errors.As(err, &me):
		return me
	}
	return meetErr("data-dir", err, "")
}

// codecs loads the video codec once per process. Its library is kept in the private data directory.
var codecs struct {
	once sync.Once
	err  error
}

func loadCodecs(dir string) error {
	codecs.once.Do(func() {
		_, codecs.err = vpx.Load(vpx.LoadConfig{CacheDir: filepath.Join(dir, "engine")})
		if codecs.err != nil && vpx.Engine() != vpx.EngineAuto {
			codecs.err = nil
		}
	})
	return codecs.err
}

func (s *MeetService) start() error {
	set, err := s.cfg.resolve()
	if err != nil {
		return err
	}
	if err := prepareDataDir(set.DataDir); err != nil {
		return err
	}
	saved, err := loadSaved(set.DataDir)
	if err != nil {
		return err
	}
	s.set = set
	s.prefMu.Lock()
	s.saved = saved
	s.prefMu.Unlock()
	if s.id = s.identity; s.id == nil {
		if s.id, err = loadIdentity(s.set.DataDir); err != nil {
			return err
		}
	}
	if s.hist, err = openHistory(s.set.DataDir); err != nil {
		return err
	}
	if err := loadCodecs(s.set.DataDir); err != nil {
		return meetErr("codec-unavailable", err, "")
	}
	if set.QualityReport == qualityOn {
		s.quality = newQualityWriter(set.DataDir, qualityMaxBytes)
		log.Print(s.quality.notice())
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	if s.set.video != (videoMode{}) {
		s.vid = s.set.video
	}
	s.tiles.setRule(tileRuleFor(s.vid))
	s.tiles.setMode(s.set.DisplayFPS)
	s.media = newLocalMedia(s.hw, s.vid, func(ev FrameEvent) { s.emit("meet:frame", ev) }, s.mediaLost, s.lobbyChanged)
	s.media.tile.b = &s.tiles
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	s.wg.Go(s.levelLoop)
	s.startNetwork()
	return nil
}

func loadIdentity(dir string) (*koloid.Identity, error) {
	path := filepath.Join(dir, "identity.key")
	id, err := koloid.Load(path)
	if !errors.Is(err, fs.ErrNotExist) {
		return id, err
	}
	if id, err = koloid.Generate(); err != nil {
		return nil, err
	}
	if err = id.SaveNew(path); err != nil && !errors.Is(err, koloid.ErrSavedNotSynced) {
		return nil, err
	}
	return id, nil
}

// ServiceShutdown ends the call, closes the devices and stops the network. Wails calls it when the app quits.
func (s *MeetService) ServiceShutdown() error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if !s.started {
		return nil
	}
	s.mu.Lock()
	s.running = false // no call starts from now on
	c := s.call
	s.mu.Unlock()
	if c != nil {
		c.end(meetErr("call-ended-closed", ErrClosed, ""))
		select {
		case <-c.done:
		case <-time.After(endTimeout):
			log.Print("call ", c.id, " was still ending when the app closed")
		}
	}
	s.cancel()
	s.wg.Wait()
	s.media.closeAll()
	s.stopNetwork()
	s.quality.close()
	s.mu.Lock()
	if s.notchTimer != nil {
		s.notchTimer.Stop()
		s.notchTimer = nil
	}
	s.mu.Unlock()
	s.started = false
	return nil
}

// Info returns the data directory, the version and the network state.
func (s *MeetService) Info() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := ""
	if s.id != nil {
		key = s.id.PublicKeyString()
	}
	n := s.net
	n.Relays = orEmpty(n.Relays) // nil before Start
	return Info{DataDir: s.set.DataDir, Version: version, Network: n, KeyID: key}
}

func (s *MeetService) settingsCopy() settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set
}

func (s *MeetService) maxParticipants() int { return s.settingsCopy().MaxParticipants }

func (s *MeetService) prefs() savedSettings {
	s.prefMu.RLock()
	defer s.prefMu.RUnlock()
	return s.saved
}

func (s *MeetService) displayName() string { return s.prefs().DisplayName }

// Settings returns the settings the app runs with.
func (s *MeetService) Settings() Settings {
	set := s.settingsCopy()
	p := s.prefs()
	first := ""
	if set.first.IsValid() {
		first = set.first.String()
	}
	return Settings{
		NetworkMode: set.mode, EntryNodes: set.nodesText, EntryNodesFrom: set.nodesFrom, ConnectionRelays: set.relaysText, ConnectionRelaysFrom: set.relaysFrom, FirstNode: first, DataDir: set.DataDir, Listen: set.Listen,
		MaxParticipants: set.MaxParticipants, ShowInNotch: p.showInNotch(), DisplayName: p.DisplayName,
		CameraID: p.CameraID, MicrophoneID: p.MicrophoneID, SpeakerID: p.SpeakerID,
	}
}

// saveSaved changes the saved settings with f and writes them.
func (s *MeetService) saveSaved(f func(*savedSettings)) error {
	s.prefMu.Lock()
	defer s.prefMu.Unlock()
	next := s.saved
	f(&next)
	if err := saveSettings(s.settingsCopy().DataDir, next); err != nil {
		return err
	}
	s.saved = next
	return nil
}

// Reconnect starts the network again with the same settings, after it failed. It refuses during a call.
func (s *MeetService) Reconnect() error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if !s.started {
		return explain(ErrNotReady)
	}
	if !s.beginRestart() {
		return meetErr("busy-reconnect", ErrBusy, "")
	}
	defer s.endRestart()
	s.stopNetwork()
	s.startNetwork()
	return nil
}

// beginRestart marks the network as restarting when no call runs, in one step, so no call starts on a network that
// is about to close.
func (s *MeetService) beginRestart() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.call != nil || s.restarting {
		return false
	}
	s.restarting = true
	return true
}

func (s *MeetService) endRestart() {
	s.mu.Lock()
	s.restarting = false
	s.mu.Unlock()
}

// SetDisplayName checks and saves the name other people see in the call.
func (s *MeetService) SetDisplayName(name string) (Settings, error) {
	clean, err := cleanName(name)
	if err != nil {
		return Settings{}, err
	}
	if err := s.saveSaved(func(p *savedSettings) { p.DisplayName = clean }); err != nil {
		return Settings{}, meetErr("internal", err, "")
	}
	if c := s.activeCall(); c != nil {
		c.changed()
	}
	return s.Settings(), nil
}

// History returns the last finished calls, newest first.
func (s *MeetService) History() []HistoryItem {
	if s.hist == nil {
		return []HistoryItem{}
	}
	return s.hist.list()
}

// ClearHistory empties the history.
func (s *MeetService) ClearHistory() error {
	if s.hist == nil {
		return nil
	}
	if err := s.hist.clear(); err != nil {
		return meetErr("internal", err, "")
	}
	s.emit("meet:history", []HistoryItem{})
	return nil
}

// startNetwork starts the first entry node, if configured, and the swarm, in the background.
func (s *MeetService) startNetwork() {
	s.netMu.Lock()
	defer s.netMu.Unlock()
	ctx, cancel := context.WithCancel(s.ctx)
	s.netCancel = cancel
	s.setNetwork(netStarting, nil)
	s.netWG.Go(func() { s.runNetwork(ctx) })
	s.checkRelays(ctx)
}

func (s *MeetService) stopNetwork() {
	s.netMu.Lock()
	defer s.netMu.Unlock()
	if s.netCancel != nil {
		s.netCancel()
	}
	s.mu.Lock()
	sw, node := s.sw, s.node
	s.sw, s.node = nil, nil
	s.mu.Unlock()
	if sw != nil {
		sw.Close()
	}
	s.netWG.Wait()
	if node != nil {
		node.Close()
	}
	s.relayMu.Lock()
	delete(s.relayStates, swarmMember)
	delete(s.relayStates, relayCheck)
	s.relayMu.Unlock()
}

// allowPeer keeps the host's app from dialing the relay of its own call over the network: the relay is in this
// process.
func (s *MeetService) allowPeer(key ed25519.PublicKey, addr netip.AddrPort) bool {
	if h := s.dialHook; h != nil && !h(key, addr) {
		return false
	}
	s.mu.Lock()
	c := s.call
	s.mu.Unlock()
	if c == nil || c.role != roleHost {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.relID) == 0 || string(key) != string(c.relID)
}

func (s *MeetService) runNetwork(ctx context.Context) {
	set := s.settingsCopy()
	var node *kolodht.Node
	host := set.first
	if set.local.IsValid() {
		host = set.local
	}
	if host.IsValid() {
		nid, err := koloid.Generate()
		if err == nil {
			node, err = kolodht.New(ctx, host.String(), nid, &kolodht.Config{Server: true})
		}
		switch {
		case err != nil && set.local.IsValid() && addrInUse(err):
			node = nil // the window of this computer that opened first runs the entry node: join it
		case err != nil:
			reason := "first-node-failed"
			if addrInUse(err) {
				reason = "first-node-in-use"
			}
			s.setNetwork(netFailed, meetErr(reason, err, host.String()))
			return
		}
	}
	sw, err := koloswarm.New(ctx, set.Listen, s.id, &koloswarm.Config{
		BootstrapNodes: set.nodes,
		LookupInterval: set.LookupInterval,
		RetryMax:       dialRetryMax,
		DHT:            &kolodht.Config{Secure: &kolosecure.Config{Transport: s.swarmTransport(swarmMember)}},
		Allow:          s.allowPeer,
		Relays:         set.relays,
	})
	if err == nil {
		if err = sw.Bootstrap(ctx, nil); err != nil {
			sw.Close()
		}
	}
	if err != nil {
		if node != nil {
			node.Close()
		}
		if ctx.Err() == nil {
			s.setNetwork(netFailed, bootstrapFailure(set, err, node != nil))
		}
		return
	}
	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		sw.Close()
		if node != nil {
			node.Close()
		}
		return
	}
	s.sw, s.node = sw, node
	s.mu.Unlock()
	log.Print("kolo-id ", s.id.PublicKeyString())
	s.setNetwork(netReady, nil)
	s.swarmLoop(ctx, sw)
}

// bootstrapFailure says why the network could not start, by the network it was asked to use. hosted is whether this
// app runs the entry node.
func bootstrapFailure(set settings, err error, hosted bool) *MeetError {
	switch {
	case set.mode == modePublic:
		return meetErr("public-unreachable", err, "")
	case set.mode == modeLocal && !hosted && errors.Is(err, kolodht.ErrNoNodes):
		return meetErr("local-port-taken", err, set.local.String()) // held by a program that is not this app
	}
	return meetErr("bootstrap-failed", err, set.nodesText)
}

func (s *MeetService) setNetwork(state string, why *MeetError) {
	s.netEmitMu.Lock()
	defer s.netEmitMu.Unlock()
	set := s.settingsCopy()
	first := ""
	if set.first.IsValid() {
		first = set.first.String()
	}
	n := Network{State: state, Mode: set.mode, EntryNodes: set.entryNodeCount(), FirstNode: first, Error: why, Relays: s.relayStatuses()}
	if why != nil {
		n.Message = why.Message
	}
	s.mu.Lock()
	n.Seq = s.net.Seq + 1
	s.net = n
	s.mu.Unlock()
	s.emit("meet:network", n)
}

// pushRelays sends the network again when the relays' states changed.
func (s *MeetService) pushRelays() {
	s.netEmitMu.Lock()
	defer s.netEmitMu.Unlock()
	relays := s.relayStatuses()
	s.mu.Lock()
	if sameRelays(relays, s.net.Relays) {
		s.mu.Unlock()
		return
	}
	s.net.Relays, s.net.Seq = relays, s.net.Seq+1
	n := s.net
	s.mu.Unlock()
	s.emit("meet:network", n)
}

// networkReady reports whether the network is up.
func (s *MeetService) networkReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.net.State == netReady
}

func (s *MeetService) swarm() *koloswarm.Swarm {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.net.State != netReady {
		return nil
	}
	return s.sw
}

// leaveTopic leaves a swarm topic if the swarm is still up.
func (s *MeetService) leaveTopic(t koloswarm.Topic) {
	s.mu.Lock()
	sw := s.sw
	s.mu.Unlock()
	if sw != nil {
		sw.Leave(t)
	}
}

// swarmLoop hands every connection the swarm makes to the call that wants it; a connection nobody wants is closed.
func (s *MeetService) swarmLoop(ctx context.Context, sw *koloswarm.Swarm) {
	for {
		ev, err := sw.Next(ctx)
		if err != nil {
			return
		}
		switch ev.Kind {
		case koloswarm.Connected:
			if c := s.activeCall(); c == nil || !c.takePeer(ev.Peer) {
				ev.Peer.Conn().Close()
			}
		case koloswarm.Lost:
			if c := s.activeCall(); c != nil && ev.Peer != nil {
				c.peerLost(ev.Peer)
			}
		case koloswarm.Unreachable:
			if c := s.activeCall(); c != nil {
				c.unreachable(ev.PublicKey)
			}
		case koloswarm.RelayChanged:
			s.relayChanged(swarmMember, ev)
		}
	}
}

func (s *MeetService) activeCall() *call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.call
}

// startCall registers a new call as the only one and runs it in the background.
func (s *MeetService) startCall(role string, code invite.Code) (Call, error) {
	if _, err := cleanName(s.displayName()); err != nil {
		return Call{}, err
	}
	s.mu.Lock()
	var err error
	switch {
	case s.ctx == nil:
		err = explain(ErrNotReady)
	case !s.running:
		err = meetErr("app-closed", ErrClosed, "")
	case s.restarting:
		err = meetErr("busy-restarting", ErrBusy, "")
	case s.call != nil:
		err = meetErr("busy-call", ErrBusy, "")
	case s.net.State != netReady:
		err = explain(ErrNotReady)
	}
	if err != nil {
		s.mu.Unlock()
		return Call{}, err
	}
	c := newCall(s, role)
	if role == roleHost {
		c.state, c.phase = callCreating, ""
	} else {
		c.state, c.phase, c.code = callJoining, phaseFindingHost, code
	}
	s.call = c
	s.lastEnded = nil
	s.mu.Unlock()
	go c.supervise()
	go func() {
		defer close(c.setup)
		if role == roleHost {
			c.runHost()
		} else {
			c.runGuest()
		}
	}()
	c.changed()
	return c.snapshot(true), nil
}

// CreateCall starts a call and returns it: its invite arrives in the first "meet:call" events, once the relay runs.
func (s *MeetService) CreateCall() (Call, error) { return s.startCall(roleHost, invite.Code{}) }

// JoinCall joins the call of an invite. Spaces and line breaks in the pasted text are ignored. It returns at once;
// the call moves on in "meet:call" events and ends with an error if the host cannot be reached or says no.
func (s *MeetService) JoinCall(text string) (Call, error) {
	code, err := parseInviteText(text)
	if err != nil {
		return Call{}, meetErr("invalid-invite", err, "")
	}
	return s.startCall(roleGuest, code)
}

// maxCodeText bounds the pasted text JoinCall looks at: an invite code is far shorter, and the window sends what was
// pasted.
const maxCodeText = 1024

func parseInviteText(text string) (invite.Code, error) {
	if len(text) > maxCodeText {
		return invite.Code{}, invite.ErrInvalidCode
	}
	return invite.Parse(strings.Join(strings.Fields(text), ""))
}

// NewInvite makes another invite for the call you host and returns it to the main window (another window gets "");
// it is also the one "meet:call" shows from now on. The earlier invites keep working. A person the host turned away because the call was full needs it: an
// invite remembers that it said no.
func (s *MeetService) NewInvite(ctx context.Context) (string, error) {
	c := s.activeCall()
	if c == nil || c.role != roleHost || c.snapshot(false).State != callActive { // the invite exists once the call is active
		return "", meetErr("no-call", ErrNoCall, "")
	}
	code, err := c.newInvite()
	if err != nil {
		return "", explain(err)
	}
	if !fromMainWindow(ctx) {
		return "", nil // only the main window holds an invite
	}
	return code, nil
}

// LeaveCall leaves the call, or stops joining one, and waits for it to end: the keys are erased, the devices closed
// and the relay of a host call stopped, so the call ends for everyone.
func (s *MeetService) LeaveCall() error {
	c := s.activeCall()
	if c == nil {
		return meetErr("no-call", ErrNoCall, "")
	}
	c.end(nil)
	select {
	case <-c.done:
		return nil
	case <-time.After(endTimeout):
		return meetErr("call-still-ending", ErrBusy, "")
	}
}

// RemoveParticipant takes the person with key, a Participant ID, out of the call you host. Everyone left rotates their
// key, and the invites that exist now no longer let that person in; a new invite does. It refuses with not-host for a
// guest, remove-self for the host's own key and participant-unknown for anyone not in the call.
func (s *MeetService) RemoveParticipant(key string) error {
	c := s.activeCall()
	if c == nil {
		return meetErr("no-call", ErrNoCall, "")
	}
	c.mu.Lock()
	st := c.state
	c.mu.Unlock()
	switch {
	case st != callActive:
		return meetErr("no-call", ErrNoCall, "")
	case c.role != roleHost:
		return meetErr("not-host", ErrNotHost, "")
	case key == c.selfID:
		return meetErr("remove-self", nil, "")
	}
	return c.remove(key)
}

// Call returns the call now, or nil. Only the main window gets the invite.
func (s *MeetService) Call(ctx context.Context) *Call {
	c := s.activeCall()
	if c == nil {
		return nil
	}
	snap := c.snapshot(fromMainWindow(ctx))
	return &snap
}

// CopyInvite puts the invite of the call on the clipboard, here in Go, so a window other than the main one never
// holds it. It does nothing when there is none.
func (s *MeetService) CopyInvite() {
	c := s.activeCall()
	if c == nil {
		return
	}
	c.mu.Lock()
	code := c.inviteStr
	c.mu.Unlock()
	if code != "" && s.copyText != nil {
		s.copyText(code)
		s.mu.Lock()
		s.copiedInvite = code
		s.mu.Unlock()
	}
}

// clearInviteCopy empties the clipboard when it still holds the invite CopyInvite put there, so a call that ended does
// not leave a code that still lets someone ask to join. A clipboard the person changed is left alone.
func (s *MeetService) clearInviteCopy() {
	s.mu.Lock()
	code := s.copiedInvite
	s.copiedInvite = ""
	s.mu.Unlock()
	if code == "" || s.readText == nil || s.copyText == nil {
		return
	}
	if text, ok := s.readText(); ok && text == code {
		s.copyText("")
	}
}

// callChanged tells the windows that a call changed.
func (s *MeetService) callChanged(c *call) {
	c.emitMu.Lock()
	if c.emitted { // the call ended: the window has its last word
		c.emitMu.Unlock()
		return
	}
	c.mu.Lock()
	if c.endedSnap != nil { // the teardown has its final event to send: no other follows
		c.mu.Unlock()
		c.emitMu.Unlock()
		return
	}
	c.seq++
	c.mu.Unlock()
	snap := c.snapshot(true)
	if hook := s.emitHook.Load(); hook != nil {
		(*hook)()
	}
	s.emit("meet:call", snap)
	c.emitMu.Unlock()
	s.sendNotch()
	s.pushTray()
}

// callEnded runs once, at the end of the teardown: it forgets the call, closes the devices, records the call and
// tells the windows.
func (s *MeetService) callEnded(c *call, snap Call) {
	s.media.closeAll()
	s.clearDeviceErrors()
	s.clearInviteCopy()
	s.mu.Lock()
	if s.call == c {
		s.call = nil
	}
	end := snap
	s.lastEnded, s.lastEndAt = &end, time.Now()
	hist := s.hist
	s.mu.Unlock()
	c.emitMu.Lock()
	c.mu.Lock()
	c.seq++
	snap.Seq = c.seq
	c.endedSnap.Seq = c.seq
	c.mu.Unlock()
	c.emitted = true
	s.emit("meet:call", snap)
	c.emitMu.Unlock()
	if hist != nil && (snap.Started != 0 || snap.Error != nil) {
		it := HistoryItem{ID: c.id, Role: c.role, Result: "done", Participants: c.peak, At: snap.Finished, Error: snap.Error}
		if it.Participants == 0 {
			it.Participants = 1
		}
		if snap.Started != 0 {
			it.Seconds = (snap.Finished - snap.Started) / 1000
		}
		if snap.Error != nil && snap.Error.Kind != kindEnded {
			it.Result, it.Message = "failed", snap.Error.Message
		} else if snap.Error != nil {
			it.Message = snap.Error.Message
		}
		list, err := hist.add(it)
		if err != nil {
			log.Print("history: ", err)
		}
		s.emit("meet:history", list)
	}
	s.emit("meet:lobby", s.Lobby())
	s.notchEnded()
	s.pushTray()
}
