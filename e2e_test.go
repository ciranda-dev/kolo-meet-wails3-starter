//go:build finished

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image/jpeg"
	"net"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	koloid "github.com/ciranda-dev/kolo-id"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const waitFor = 60 * time.Second

// testVideo is small and slow so that four people on one machine, with the race detector, keep up.
var testVideo = videoMode{w: 160, h: 120, fps: 10}

// recorder keeps what a MeetService tells the main window and the notch.
type recorder struct {
	mu       sync.Mutex
	calls    []Call
	networks []Network
	lobbies  []Lobby
	frames   map[string]int
	lastJPEG map[string][]byte
	lastSize map[string][2]int
	left     []ParticipantEvent
	history  [][]HistoryItem
	levels   []LevelsEvent
	all      []string // every event of the main window as name and JSON
	notch    []NotchState
	notchRaw []string
	links    func() string
}

func newRecorder() *recorder {
	return &recorder{frames: map[string]int{}, lastJPEG: map[string][]byte{}, lastSize: map[string][2]int{}}
}

func (r *recorder) emit(name string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch v := data.(type) {
	case Call:
		r.calls = append(r.calls, v)
	case Network:
		r.networks = append(r.networks, v)
	case Lobby:
		r.lobbies = append(r.lobbies, v)
	case FrameEvent:
		r.frames[v.ID]++
		r.lastJPEG[v.ID] = v.JPEG
		r.lastSize[v.ID] = [2]int{v.Width, v.Height}
	case ParticipantEvent:
		r.left = append(r.left, v)
	case []HistoryItem:
		r.history = append(r.history, v)
	case LevelsEvent:
		r.levels = append(r.levels, v)
		return // too many to keep as text
	}
	if name == "meet:frame" {
		return
	}
	b, _ := json.Marshal(data)
	r.all = append(r.all, name+" "+string(b))
}

func (r *recorder) emitNotch(name string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := data.(NotchState); ok {
		r.notch = append(r.notch, v)
	}
	b, _ := json.Marshal(data)
	r.notchRaw = append(r.notchRaw, name+" "+string(b))
}

// events returns a copy of every event the main window got, as name and JSON.
func (r *recorder) events() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.all...)
}

func (r *recorder) lastCall() (Call, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return Call{}, false
	}
	return r.calls[len(r.calls)-1], true
}

// waitCall polls until the newest call state passes ok.
func (r *recorder) waitCall(t *testing.T, what string, ok func(Call) bool) Call {
	t.Helper()
	deadline := time.Now().Add(waitFor)
	for time.Now().Before(deadline) {
		if c, found := r.lastCall(); found && ok(c) {
			return c
		}
		time.Sleep(20 * time.Millisecond)
	}
	c, _ := r.lastCall()
	t.Fatalf("never %s; last call: %s%s", what, describe(c), r.diag())
	return Call{}
}

// diag is filled by a test with a function that says what the links of the call look like.
func (r *recorder) diag() string {
	if r.links == nil {
		return ""
	}
	return "\n" + r.links()
}

func describe(c Call) string {
	var b strings.Builder
	b.WriteString(c.State + "/" + c.Phase)
	for _, p := range c.Participants {
		b.WriteString(" [" + p.Name + " " + p.State)
		if p.AudioOn {
			b.WriteString(" audio")
		}
		if p.VideoOn {
			b.WriteString(" video")
		}
		b.WriteString("]")
	}
	if c.Error != nil {
		b.WriteString(" error=" + c.Error.Reason)
	}
	return b.String()
}

func (r *recorder) waitNetwork(t *testing.T, state string) {
	t.Helper()
	deadline := time.Now().Add(waitFor)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		n := len(r.networks)
		last := Network{}
		if n > 0 {
			last = r.networks[n-1]
		}
		r.mu.Unlock()
		if last.State == state {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t.Fatalf("network never %s: %+v", state, r.networks)
}

func (r *recorder) frameCount(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.frames[id]
}

// meanBrightness is the average of the red channel of the last picture of id.
func (r *recorder) meanBrightness(t *testing.T, id string) float64 {
	t.Helper()
	r.mu.Lock()
	b := r.lastJPEG[id]
	r.mu.Unlock()
	img, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("picture of %s: %v", id, err)
	}
	var sum, n float64
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y += 4 {
		for x := bounds.Min.X; x < bounds.Max.X; x += 4 {
			r, _, _, _ := img.At(x, y).RGBA()
			sum += float64(r >> 8)
			n++
		}
	}
	return sum / n
}

// fakeWindow is the calling window Wails puts in the context of a bound call.
type fakeWindow struct{ name string }

func (w fakeWindow) Name() string { return w.name }

// mainCtx is the context of a call made by the main window, and otherCtx of one made by the notch.
func mainCtx() context.Context {
	return context.WithValue(context.Background(), application.WindowKey, fakeWindow{mainWindowName})
}
func otherCtx() context.Context {
	return context.WithValue(context.Background(), application.WindowKey, fakeWindow{notchWindowName})
}

// freePort is a UDP port nothing holds on 127.0.0.1 right now.
func freePort(t *testing.T) uint16 {
	t.Helper()
	ap, err := netip.ParseAddrPort(freeUDP(t))
	if err != nil {
		t.Fatal(err)
	}
	return ap.Port()
}

func freeUDP(t *testing.T) string {
	t.Helper()
	c, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().String()
}

type peer struct {
	*MeetService
	rec  *recorder
	hw   *fakeHW
	data string
	copy string
	mu   sync.Mutex
}

// clip returns what the service put on the clipboard last.
func (p *peer) clip() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.copy
}

// startPeer starts a service with its own data directory, an identity made in memory, fake devices and a display
// name, on 127.0.0.1.
func startPeer(t *testing.T, name string, cfg Config, hw *fakeHW, setup func(*MeetService)) *peer {
	t.Helper()
	rec := newRecorder()
	t.Setenv("HOME", t.TempDir())
	if cfg.DataDir == "" {
		cfg.DataDir = filepath.Join(t.TempDir(), "data")
	}
	cfg.Listen = "127.0.0.1:0"
	if cfg.Local && cfg.source == nil {
		cfg.source = loopbackSource(freePort(t)) // never the app's default, which a running app may hold
	}
	// No test sends a packet to a public address: every peer starts an own network or -local.
	if set, err := cfg.resolve(); err == nil && set.mode == modePublic {
		t.Fatal("this config would start on the public network; give it First, Bootstrap or Local")
	}
	if cfg.LookupInterval == 0 {
		cfg.LookupInterval = time.Second
	}
	if cfg.FindTimeout == 0 {
		cfg.FindTimeout = 30 * time.Second
	}
	id, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	s := NewMeetService(cfg, rec.emit)
	s.identity, s.hw, s.vid = id, hw, testVideo
	s.emitNotch = rec.emitNotch
	p := &peer{MeetService: s, rec: rec, hw: hw, data: cfg.DataDir}
	s.copyText = func(text string) { p.mu.Lock(); p.copy = text; p.mu.Unlock() }
	s.readText = func() (string, bool) { return p.clip(), true }
	rec.links = func() string { return s.linkSummary() }
	if setup != nil {
		setup(s)
	}
	if _, err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.ServiceShutdown() })
	if name != "" {
		if _, err := s.SetDisplayName(name); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// party is a person in a test call and how to tell its media from the others'.
type party struct {
	name string
	luma byte
	freq float64
}

var parties = []party{{"Ana", 60, 440}, {"Bruno", 130, 660}, {"Carla", 200, 880}, {"Davi", 100, 1100}}

// network starts n people on one private network: the first runs the entry node.
func network(t *testing.T, n int) []*peer {
	t.Helper()
	first := freeUDP(t)
	var out []*peer
	for i := range n {
		cfg := Config{Bootstrap: first}
		if i == 0 {
			cfg = Config{First: first}
		}
		p := startPeer(t, parties[i].name, cfg, newFakeHW(parties[i].luma, parties[i].freq), nil)
		p.rec.waitNetwork(t, netReady)
		out = append(out, p)
	}
	return out
}

// turnOn puts a person's microphone and camera on, as the lobby does.
func (p *peer) turnOn(t *testing.T) {
	t.Helper()
	if _, err := p.SetMicrophone(true); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SetCamera(true); err != nil {
		t.Fatal(err)
	}
}

// startCall has out[0] create a call and the others join with its invite, and waits until all see everyone live.
func startCall(t *testing.T, ps []*peer) string {
	t.Helper()
	host := ps[0]
	host.turnOn(t)
	if _, err := host.CreateCall(); err != nil {
		t.Fatal(err)
	}
	c := host.rec.waitCall(t, "had an invite", func(c Call) bool { return c.Invite != "" && c.State == callActive })
	for _, g := range ps[1:] {
		g.turnOn(t)
		if _, err := g.JoinCall(c.Invite); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range ps {
		p.rec.waitCall(t, "had everyone live", func(c Call) bool {
			if c.State != callActive || len(c.Participants) != len(ps) {
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
	return c.Invite
}

func idOf(t *testing.T, p *peer) string {
	t.Helper()
	return p.Info().KeyID
}

func TestTwoPeopleCall(t *testing.T) {
	ps := network(t, 2)
	invite := startCall(t, ps)
	if !strings.HasPrefix(invite, "koloinv0") {
		t.Fatalf("invite %q", invite)
	}
	a, b := ps[0], ps[1]
	idA, idB := idOf(t, a), idOf(t, b)

	// Each sees the other's picture, with the brightness the other's camera makes, and hears its pitch.
	waitUntil(t, "pictures", func() bool { return a.rec.frameCount(idB) > 3 && b.rec.frameCount(idA) > 3 })
	if got := a.rec.meanBrightness(t, idB); got < 100 || got > 150 {
		t.Errorf("Ana sees Bruno at brightness %.0f, want about %.0f", got, float64(130-16)*255/219)
	}
	waitUntil(t, "sound", func() bool {
		return tone(a.hw.heard(), 660) > 0.05 && tone(b.hw.heard(), 440) > 0.05
	})
	if tone(a.hw.heard(), 440) > 0.02 {
		t.Error("Ana hears herself")
	}
	// Names and roles come through the sealed profile track.
	ca, _ := a.rec.lastCall()
	waitUntil(t, "names", func() bool {
		ca, _ = a.rec.lastCall()
		cb, _ := b.rec.lastCall()
		return findPart(ca, idB).Name == "Bruno" && findPart(cb, idA).Name == "Ana"
	})
	if !findPart(ca, idA).Host || findPart(ca, idB).Host || ca.Host != idA {
		t.Errorf("hosts: %+v", ca.Participants)
	}
	if !strings.Contains(ca.Security.Header, "2 in call, end-to-end encrypted, through relay run by you") {
		t.Errorf("host header %q", ca.Security.Header)
	}
	cb, _ := b.rec.lastCall()
	if !strings.Contains(cb.Security.Header, "through relay run by Ana") || cb.Invite != "" {
		t.Errorf("guest sees header %q invite %q", cb.Security.Header, cb.Invite)
	}
	// Leaving ends the guest's call, and the host stays alone.
	if err := b.LeaveCall(); err != nil {
		t.Fatal(err)
	}
	end := b.rec.waitCall(t, "ended", func(c Call) bool { return c.State == callEnded })
	if end.Error != nil {
		t.Errorf("a guest that left has an error: %+v", end.Error)
	}
	a.rec.waitCall(t, "alone", func(c Call) bool { return c.State == callActive && len(c.Participants) == 1 })
	a.rec.mu.Lock()
	left := slices.Clone(a.rec.left)
	a.rec.mu.Unlock()
	if len(left) == 0 || left[len(left)-1].Message != "Bruno no longer receives your media." {
		t.Errorf("notice: %+v", left)
	}
	// The host leaving ends the call: devices closed, history written, no call left.
	if err := a.LeaveCall(); err != nil {
		t.Fatal(err)
	}
	a.rec.waitCall(t, "ended", func(c Call) bool { return c.State == callEnded })
	if n := a.hw.opened.Load(); n != 0 {
		t.Errorf("%d devices still open after the call", n)
	}
	if h := a.History(); len(h) != 1 || h[0].Role != "host" || h[0].Result != "done" || h[0].Participants != 2 {
		t.Errorf("history %+v", h)
	}
	if a.activeCall() != nil {
		t.Error("a call is left")
	}
}

func findPart(c Call, id string) Participant {
	for _, p := range c.Participants {
		if p.ID == id {
			return p
		}
	}
	return Participant{}
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitFor)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never %s", what)
}

var _ = context.Background
