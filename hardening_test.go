//go:build finished

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ciranda-dev/kolo-audio/opus"
	invite "github.com/ciranda-dev/kolo-invite"
	kolomedia "github.com/ciranda-dev/kolo-media"
	kolomoq "github.com/ciranda-dev/kolo-moq"
	"github.com/ciranda-dev/kolo-moq/secobj"
	koloswarm "github.com/ciranda-dev/kolo-swarm"
	kolovideo "github.com/ciranda-dev/kolo-video"
)

// A participant whose key never arrives shows "waiting for keys" and plays nothing of that person: no picture, no
// sound. The others, who have the key, see and hear it.
func TestNoKeyPlaysNothing(t *testing.T) {
	ps := network(t, 3)
	a, b, c := ps[0], ps[1], ps[2]
	idA := idOf(t, a)
	keyA, _ := hex.DecodeString(idA)
	b.holdKeys = func(k ed25519.PublicKey) bool { return string(k) == string(keyA) }

	a.turnOn(t)
	if _, err := a.CreateCall(); err != nil {
		t.Fatal(err)
	}
	inv := a.rec.waitCall(t, "invite", func(c Call) bool { return c.Invite != "" && c.State == callActive }).Invite
	for _, g := range []*peer{b, c} {
		g.turnOn(t)
		if _, err := g.JoinCall(inv); err != nil {
			t.Fatal(err)
		}
	}
	c.rec.waitCall(t, "live", func(call Call) bool { return findPart(call, idA).State == partLive })
	waitUntil(t, "Carla sees Ana", func() bool { return c.rec.frameCount(idA) > 3 && tone(c.hw.heard(), parties[0].freq) > 0.03 })
	// Bruno, who has Carla's key and Ana's media on the relay, still has no key of Ana's.
	end := time.Now().Add(4 * time.Second)
	for time.Now().Before(end) {
		if n := b.rec.frameCount(idA); n != 0 {
			t.Fatalf("Bruno got %d pictures of Ana without her key", n)
		}
		// Ana's tone has a strength of about 0.24; another tone seen through a short window leaks a little into this
		// pitch, so look only at a window of half a second or more, with the threshold the positive checks use.
		if h := b.hw.heard(); len(h) >= sampleRate/2 && tone(h, parties[0].freq) > 0.03 {
			t.Fatalf("Bruno hears Ana without her key (strength %.3f)", tone(h, parties[0].freq))
		}
		time.Sleep(50 * time.Millisecond)
	}
	call, _ := b.rec.lastCall()
	if p := findPart(call, idA); p.State != partWaitingKeys || p.AudioOn || p.VideoOn {
		t.Errorf("Ana at Bruno: %+v", p)
	}
	if sec := call.Security; sec.Waiting < 1 || sec.Complete || strings.Contains(sec.Header, "end-to-end") || !strings.Contains(sec.Header, "waiting for keys") {
		t.Errorf("security at Bruno: %+v", sec)
	}
	// The people who have everyone's keys still get the end-to-end header.
	if sec := mustCall(t, c).Security; !sec.Complete || !strings.Contains(sec.Header, "end-to-end encrypted") {
		t.Errorf("security at Carla: %+v", sec)
	}
}

// The invite is a secret of the main window: the notch, the tray, a call read from another window and the guests'
// own events never carry it.
func TestInviteStaysInTheMainWindow(t *testing.T) {
	ps := network(t, 2)
	startCall(t, ps)
	a, b := ps[0], ps[1]
	c, _ := a.rec.lastCall()
	invite := c.Invite
	if invite == "" {
		t.Fatal("the host has no invite")
	}
	if !strings.Contains(strings.Join(a.rec.events(), "\n"), invite) {
		t.Fatal("the main window of the host never got the invite")
	}
	for name, p := range map[string]*peer{"host": a, "guest": b} {
		p.rec.mu.Lock()
		notch := strings.Join(p.rec.notchRaw, "\n")
		p.rec.mu.Unlock()
		if strings.Contains(notch, invite) {
			t.Errorf("%s: the notch got the invite", name)
		}
		for _, v := range []any{p.Notch(), p.trayNow(), p.Lobby(), p.Settings(), p.Info()} {
			if buf, _ := json.Marshal(v); strings.Contains(string(buf), invite) {
				t.Errorf("%s: %T holds the invite", name, v)
			}
		}
		if got := p.Call(context.Background()); got == nil || got.Invite != "" {
			t.Errorf("%s: a call read by another window: %+v", name, got)
		}
	}
	if n := a.Notch(); !n.HasInvite || !n.Active || n.Participants != 2 || n.State != callActive {
		t.Errorf("host notch %+v", n)
	}
	if n := b.Notch(); n.HasInvite {
		t.Errorf("guest notch %+v", n)
	}
	a.NotchCopyInvite()
	if a.clip() != invite {
		t.Error("the notch's Copy did not copy the invite")
	}
	b.NotchCopyInvite()
	if b.clip() != "" {
		t.Error("a guest copied an invite")
	}
	for _, e := range b.rec.events() {
		if strings.Contains(e, invite) {
			t.Errorf("a guest's window got the invite: %.80s", e)
		}
	}
	if st := a.trayNow(); !st.InCall || !st.HasInvite || st.Status != "In a call with 2 people" {
		t.Errorf("host tray %+v", st)
	}
	// The call bar's controls work from the notch and the tray: mute, camera, leave.
	if _, err := a.ToggleMicrophone(); err != nil {
		t.Fatal(err)
	}
	if n := a.Notch(); n.Microphone || !n.Camera {
		t.Errorf("notch after mute %+v", n)
	}
	a.NotchLeave()
	a.rec.waitCall(t, "ended", func(c Call) bool { return c.State == callEnded })
	if n := a.Notch(); n.Active || n.HasInvite || n.Message == "" {
		t.Errorf("notch after the call %+v", n)
	}
	if got := a.clip(); got != "" {
		t.Errorf("the invite is still on the clipboard after the call: %.12s", got)
	}
}

// The notch follows the setting: off, it gets nothing but an empty state and never opens.
func TestNotchSetting(t *testing.T) {
	ps := network(t, 2)
	a := ps[0]
	shown := make(chan struct{}, 8)
	a.notch = fakeNotch{show: func() { shown <- struct{}{} }}
	if _, err := a.SetShowInNotch(false); err != nil {
		t.Fatal(err)
	}
	if n := a.Notch(); n.Active {
		t.Errorf("notch while off: %+v", n)
	}
	startCall(t, ps)
	select {
	case <-shown:
		t.Fatal("the notch opened while the setting is off")
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := a.SetShowInNotch(true); err != nil {
		t.Fatal(err)
	}
	if n := a.Notch(); !n.Active || n.Participants != 2 {
		t.Errorf("notch on: %+v", n)
	}
	a.rec.mu.Lock()
	last := a.rec.notch[len(a.rec.notch)-1]
	a.rec.mu.Unlock()
	if !last.Active {
		t.Errorf("the notch page was not told: %+v", last)
	}
	if s := a.Settings(); !s.ShowInNotch {
		t.Error("setting lost")
	}
}

type fakeNotch struct{ show func() }

func (f fakeNotch) Show() { f.show() }
func (f fakeNotch) Hide() {}

// A second window started with -first on an address another one already holds says what to start it with instead.
func TestFirstNodeInUse(t *testing.T) {
	hold, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	addr := hold.LocalAddr().String()
	p := startPeer(t, "Eva", Config{First: addr}, newFakeHW(1, 1), nil)
	p.rec.waitNetwork(t, netFailed)
	e := p.Info().Network.Error
	if e == nil || e.Reason != "first-node-in-use" || e.Kind != kindNetwork || e.Detail != addr || e.Cause == "" {
		t.Fatalf("network error: %+v", e)
	}
	if !strings.Contains(e.Message, "-bootstrap "+addr+" instead of -first") || strings.Contains(e.Message, "Settings") {
		t.Errorf("message: %q", e.Message)
	}
}

// Access denied is a typed error, never a hang or a panic, and the call still works with what is left.
func TestPermissionsDenied(t *testing.T) {
	hw := newFakeHW(90, 500)
	hw.denyCamera.Store(true)
	hw.denyMic.Store(true)
	hw.noSpeaker.Store(true)
	p := startPeer(t, "Eva", Config{First: freeUDP(t)}, hw, nil)
	p.rec.waitNetwork(t, netReady)

	cam, err := p.SetCamera(true)
	var me *MeetError
	if !errors.As(err, &me) || me.Reason != "camera-denied" || me.Kind != kindPermission || cam.State != "error" || cam.Error.Reason != "camera-denied" {
		t.Fatalf("camera: %+v %v", cam, err)
	}
	mic, err := p.SetMicrophone(true)
	if !errors.As(err, &me) || me.Reason != "microphone-denied" || mic.State != "error" {
		t.Fatalf("microphone: %+v %v", mic, err)
	}
	if hw.opened.Load() != 0 {
		t.Error("a device is open")
	}
	if _, err := p.CreateCall(); err != nil {
		t.Fatal(err)
	}
	c := p.rec.waitCall(t, "active", func(c Call) bool { return c.State == callActive && c.Speaker.State == "error" })
	if c.Speaker.Error.Reason != "speaker-none" || c.Camera.Error == nil || c.Microphone.Error == nil {
		t.Errorf("devices in the call: %+v %+v %+v", c.Microphone, c.Camera, c.Speaker)
	}
	// Access granted later: the same call turns the devices on.
	hw.denyCamera.Store(false)
	hw.denyMic.Store(false)
	if _, err := p.SetMicrophone(true); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SetCamera(true); err != nil {
		t.Fatal(err)
	}
	p.rec.waitCall(t, "on", func(c Call) bool {
		return c.Microphone.State == "on" && c.Camera.State == "on" && c.Microphone.Error == nil
	})
	p.LeaveCall()
}

// A device that stops in the middle of a call is a typed error, the track goes away and the others see it.
func TestDeviceLostMidCall(t *testing.T) {
	ps := network(t, 2)
	startCall(t, ps)
	a, b := ps[0], ps[1]
	idA := idOf(t, a)
	a.hw.mu.Lock()
	cam, mic := a.hw.cam, a.hw.mic
	a.hw.mu.Unlock()
	cam.lose.Store(true)
	a.rec.waitCall(t, "camera error", func(c Call) bool { return c.Camera.State == "error" && c.Camera.Error.Reason == "camera-lost" })
	b.rec.waitCall(t, "camera gone at Bruno", func(c Call) bool { return !findPart(c, idA).VideoOn && findPart(c, idA).AudioOn })
	mic.lose.Store(true)
	a.rec.waitCall(t, "microphone error", func(c Call) bool { return c.Microphone.Error != nil && c.Microphone.Error.Reason == "microphone-lost" })
	b.rec.waitCall(t, "microphone gone at Bruno", func(c Call) bool { return !findPart(c, idA).AudioOn })
	if c, _ := a.rec.lastCall(); c.State != callActive {
		t.Errorf("the call ended with the devices: %s", describe(c))
	}
}

// Devices are listed without opening any, and the choice is saved and checked.
func TestDevicesAndChoice(t *testing.T) {
	hw := newFakeHW(90, 500)
	p := startPeer(t, "Eva", Config{First: freeUDP(t)}, hw, nil)
	ds, err := p.Devices()
	if err != nil || len(ds.Cameras) != 2 || len(ds.Microphones) != 1 || len(ds.Speakers) != 1 {
		t.Fatalf("devices %+v %v", ds, err)
	}
	if hw.opened.Load() != 0 {
		t.Error("listing opened a device")
	}
	if _, err := p.SetDevices("nope", "", ""); err == nil || err.(*MeetError).Reason != "device-unknown" {
		t.Errorf("unknown camera: %v", err)
	}
	s, err := p.SetDevices("cam-b", "mic-a", "spk-a")
	if err != nil || s.CameraID != "cam-b" || s.MicrophoneID != "mic-a" || s.SpeakerID != "spk-a" {
		t.Fatalf("SetDevices: %+v %v", s, err)
	}
	if _, err := p.SetCamera(true); err != nil {
		t.Fatal(err)
	}
	// Changing the camera while it is open opens the new one and closes the old one.
	before := hw.camOpens.Load()
	if _, err := p.SetDevices("cam-a", "mic-a", "spk-a"); err != nil {
		t.Fatal(err)
	}
	if hw.camOpens.Load() != before+1 || hw.opened.Load() != 1 {
		t.Errorf("camera opens %d, open now %d", hw.camOpens.Load(), hw.opened.Load())
	}
	if lob := p.Lobby(); lob.Camera.State != "on" || lob.Camera.DeviceID != "cam-a" || lob.Microphone.State != "off" {
		t.Errorf("lobby %+v", lob)
	}
	p.SetCamera(false)
	if hw.opened.Load() != 0 {
		t.Error("the camera stays open after it was turned off")
	}
	// Reopen the app: the choices and the name are still there.
	data := p.data
	p.ServiceShutdown()
	q := startPeer(t, "", Config{DataDir: data, First: freeUDP(t)}, newFakeHW(1, 1), nil)
	if s := q.Settings(); s.CameraID != "cam-a" || s.DisplayName != "Eva" {
		t.Errorf("settings after restart: %+v", s)
	}
}

// The lobby preview is the self-view: pictures of the local camera go to the main window as "self", and the meter
// follows the microphone.
func TestLobbyPreview(t *testing.T) {
	hw := newFakeHW(150, 700)
	p := startPeer(t, "Eva", Config{First: freeUDP(t)}, hw, nil)
	p.turnOn(t)
	waitUntil(t, "self picture", func() bool { return p.rec.frameCount("self") > 2 })
	if got := p.rec.meanBrightness(t, "self"); got < 100 || got > 160 {
		t.Errorf("self-view brightness %.0f", got)
	}
	waitUntil(t, "meter", func() bool {
		p.rec.mu.Lock()
		defer p.rec.mu.Unlock()
		for _, e := range p.rec.levels {
			for _, l := range e.Levels {
				if l.ID == "self" && l.Level > 0.3 && l.Speaking {
					return true
				}
			}
		}
		return false
	})
	hw.mu.Lock()
	hw.mic.silent.Store(true)
	hw.mu.Unlock()
	waitUntil(t, "silence", func() bool { return p.media.selfLevel().Level == 0 && !p.media.selfLevel().Speaking })
}

func TestDataDirIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes do not apply")
	}
	dir := filepath.Join(t.TempDir(), "data")
	os.MkdirAll(dir, 0o755)
	os.Chmod(dir, 0o755)
	s := NewMeetService(Config{DataDir: dir, Bootstrap: "127.0.0.1:9", Listen: "127.0.0.1:0"}, func(string, any) {})
	_, err := s.Start()
	var me *MeetError
	if !errors.As(err, &me) || me.Reason != "data-dir-open" {
		t.Fatalf("a data directory open to others: %v", err)
	}
	if err := prepareDataDir(filepath.Join(t.TempDir(), "new")); err != nil {
		t.Fatal(err)
	}
}

// A call's media never goes over a link between two participants: only the key track is readable there.
func TestPairLinkRefusesMedia(t *testing.T) {
	c := &call{people: map[string]*participant{}}
	peer := make(ed25519.PublicKey, 32)
	peer[0] = 7
	c.people[hex.EncodeToString(peer)] = &participant{}
	ns := kolomoq.Namespace{"room", "ab"}
	keyNS := kolomoq.Namespace{kolomoq.KeyNamespaceField, "room", "ab"}
	cases := []struct {
		peer ed25519.PublicKey
		kind kolomoq.RequestKind
		ns   kolomoq.Namespace
		ok   bool
	}{
		{peer, kolomoq.KindSubscribe, keyNS, true},
		{peer, kolomoq.KindFetch, keyNS, true},
		{peer, kolomoq.KindSubscribe, ns, false},
		{peer, kolomoq.KindPublishNamespace, keyNS, false},
		{peer, kolomoq.KindSubscribeNamespace, kolomoq.Namespace{"room"}, false},
		{make(ed25519.PublicKey, 32), kolomoq.KindSubscribe, keyNS, false},
		{peer, kolomoq.KindSubscribe, nil, false},
	}
	for i, tc := range cases {
		if err := c.pairAuthorize(tc.peer, tc.kind, tc.ns, "x"); (err == nil) != tc.ok {
			t.Errorf("case %d: %v", i, err)
		}
	}
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestTrayUsable(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "bus")
	up := func(p string) error {
		if p == sock || p == "@abstract" {
			return nil
		}
		return os.ErrNotExist
	}
	for _, c := range []struct {
		goos string
		env  map[string]string
		want bool
	}{
		{"darwin", nil, true},
		{"windows", nil, true},
		{"linux", nil, false},
		{"linux", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/nowhere"}, false},
		{"linux", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=" + sock + ",guid=1"}, true},
		{"linux", map[string]string{"DBUS_SESSION_BUS_ADDRESS": "tcp:host=x;unix:abstract=abstract"}, true},
		{"linux", map[string]string{"XDG_RUNTIME_DIR": filepath.Dir(sock)}, true},
	} {
		if got := trayUsable(c.goos, envOf(c.env), up); got != c.want {
			t.Errorf("%s %v: %v, want %v", c.goos, c.env, got, c.want)
		}
	}
}

// The tray icon is told when the call starts and ends, in order, and the service works without one.
func TestTrayFollowsTheCall(t *testing.T) {
	ps := network(t, 1)
	a := ps[0]
	ft := &fakeTray{}
	a.tray = ft
	a.turnOn(t)
	if _, err := a.CreateCall(); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "active tray", func() bool { s := ft.last(); return s.InCall && s.HasInvite && s.Active })
	if s := ft.last(); s.Status != "In a call, waiting for others" || !s.Microphone || !s.Camera {
		t.Errorf("tray %+v", s)
	}
	a.ToggleCamera()
	waitUntil(t, "camera off in the tray", func() bool { return !ft.last().Camera })
	a.LeaveCall()
	waitUntil(t, "idle tray", func() bool { s := ft.last(); return !s.Active && !s.InCall && s.Status == "No call" })
}

type fakeTray struct {
	mu sync.Mutex
	st TrayState
}

func (f *fakeTray) set(s TrayState) { f.mu.Lock(); f.st = s; f.mu.Unlock() }
func (f *fakeTray) last() TrayState { f.mu.Lock(); defer f.mu.Unlock(); return f.st }

// A key is taken only from the person it belongs to, for their own namespace, while they are in the call.
func TestKeyOwnerRule(t *testing.T) {
	a, b := make(ed25519.PublicKey, 32), make(ed25519.PublicKey, 32)
	a[0], b[0] = 1, 2
	r := &room{id: strings.Repeat("ab", 16)}
	c := &call{room: r, people: map[string]*participant{hex.EncodeToString(a): {}}}
	if err := c.keyOwner(a, r.memberNS(a)); err != nil {
		t.Errorf("own namespace: %v", err)
	}
	if err := c.keyOwner(a, r.memberNS(b)); err == nil {
		t.Error("a key for another person's namespace")
	}
	if err := c.keyOwner(b, r.memberNS(b)); err == nil {
		t.Error("a key from someone not in the call")
	}
	if err := c.keyOwner(a, kolomoq.Namespace{"other", hex.EncodeToString(a)}); err == nil {
		t.Error("a key for another room")
	}
	if err := c.keyOwner(r.relay, r.memberNS(r.relay)); err == nil {
		t.Error("a key from the relay")
	}
}

// The first reason given to end wins; a later one (the teardown's own, a lost connection) does not replace it.
func TestEndKeepsTheFirstReason(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &call{ctx: ctx, cancel: cancel}
	first, second := meetErr("call-ended-host-left", nil, ""), meetErr("call-ended-lost", nil, "")
	c.end(first)
	c.end(second)
	c.end(nil)
	if c.endErr != first || ctx.Err() == nil {
		t.Errorf("reason %+v", c.endErr)
	}
}

// A call that ends leaves its swarm topic, so the same topic can be joined again and no connection stays for it.
func TestCallLeavesItsTopic(t *testing.T) {
	ps := network(t, 2)
	startCall(t, ps)
	g := ps[1]
	topic := g.activeCall().room.topic
	if err := g.LeaveCall(); err != nil {
		t.Fatal(err)
	}
	sw := g.swarm()
	if sw == nil {
		t.Fatal("no swarm")
	}
	if err := sw.Join(context.Background(), topic, &koloswarm.JoinOptions{Lookup: true}); err != nil {
		t.Fatalf("the call's topic is still joined: %v", err)
	}
	sw.Leave(topic)
}

// Every way a call can end closes the camera, the microphone and the speaker: each device opened is closed once,
// and none is left open. The ways are leaving, the host ending the call, a join that fails and the app closing.
func TestDevicesClosedOnEveryEnd(t *testing.T) {
	ps := network(t, 3)
	host, leaver, stayer := ps[0], ps[1], ps[2]
	startCall(t, ps)
	c, _ := host.rec.lastCall()
	settled := func(p *peer) {
		t.Helper()
		waitUntil(t, "devices closed", func() bool { return p.hw.opened.Load() == 0 && p.hw.opens.Load() == p.hw.closes.Load() })
		if p.hw.opens.Load() == 0 {
			t.Error("the test opened nothing")
		}
	}
	// The person leaves.
	leaver.LeaveCall()
	settled(leaver)
	// The app closes during the call.
	stayer.ServiceShutdown()
	settled(stayer)
	// The host ends the call (the shutdown above already left it alone with the host): a guest that joins late finds
	// the invite revoked, with its devices on in the lobby, and the failed join closes them.
	late := startPeer(t, "Davi", Config{Bootstrap: host.Info().Network.FirstNode}, newFakeHW(100, 1100), nil)
	late.rec.waitNetwork(t, netReady)
	late.turnOn(t)
	host.LeaveCall()
	settled(host)
	if _, err := late.JoinCall(c.Invite); err != nil {
		t.Fatal(err)
	}
	end := late.rec.waitCall(t, "failed", func(c Call) bool { return c.State == callEnded })
	if end.Error == nil {
		t.Fatal("a join to an ended call worked")
	}
	settled(late)
	// A guest of a call the host ends is left with no device open either.
	ps2 := network(t, 2)
	startCall(t, ps2)
	ps2[0].LeaveCall()
	ps2[1].rec.waitCall(t, "ended by the host", func(c Call) bool {
		return c.State == callEnded && c.Error != nil && c.Error.Reason == "call-ended-host-left"
	})
	settled(ps2[1])
	settled(ps2[0])
}

// The history item of a call has the call's ID, and the lobby of the next call starts clean.
func TestHistoryItemHasTheCallID(t *testing.T) {
	ps := network(t, 2)
	startCall(t, ps)
	a, b := ps[0], ps[1]
	ca, _ := a.rec.lastCall()
	cb, _ := b.rec.lastCall()
	b.LeaveCall()
	a.LeaveCall()
	for _, x := range []struct {
		p *peer
		c Call
	}{{a, ca}, {b, cb}} {
		end := x.p.rec.waitCall(t, "ended", func(c Call) bool { return c.State == callEnded })
		if end.ID != x.c.ID {
			t.Errorf("the end event has call %s, the call was %s", end.ID, x.c.ID)
		}
		h := x.p.History()
		if len(h) != 1 || h[0].ID != x.c.ID {
			t.Errorf("history %+v for call %s", h, x.c.ID)
		}
		x.p.rec.mu.Lock()
		last := x.p.rec.history[len(x.p.rec.history)-1]
		x.p.rec.mu.Unlock()
		if len(last) != 1 || last[0].ID != x.c.ID {
			t.Errorf("meet:history %+v", last)
		}
	}
}

// bareCall is a call without a network, for rules that need only its fields. Its goroutines are refused (it is
// ending), so nothing runs behind a test.
func bareCall(t *testing.T, role string, max int) *call {
	t.Helper()
	s := &MeetService{emit: func(string, any) {}}
	s.media = newLocalMedia(newFakeHW(1, 1), defaultVideo, nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	me := bytes.Repeat([]byte{1}, 32)
	return &call{svc: s, id: "bare", role: role, ctx: ctx, cancel: cancel, me: me, selfID: hex.EncodeToString(me), state: callActive,
		room:   &room{id: strings.Repeat("ab", 16), max: max, relay: bytes.Repeat([]byte{2}, 32), host: bytes.Repeat([]byte{3}, 32)},
		people: map[string]*participant{}, pending: map[string]pendingPeer{}, admitted: map[string]*admission{}, ending: true}
}

// The keys of the other participants are erased when the call ends: an object sealed under one no longer opens.
func TestKeysErasedAtCallEnd(t *testing.T) {
	ps := network(t, 2)
	startCall(t, ps)
	host, guest := ps[0], ps[1]
	c := guest.activeCall()
	hostID := idOf(t, host)
	ns := c.room.memberNS(c.room.host)
	if hex.EncodeToString(c.room.host) != hostID {
		t.Fatal("the host is not who the call says")
	}
	// A sealed profile object of the host, as the relay carries it, opens while the call runs.
	sub, err := c.relaySess.Subscribe(context.Background(), ns, profileTrack, &kolomoq.SubscribeOptions{Join: kolomoq.JoinCurrentGroup})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	rctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var obj kolomoq.Object
	for {
		if obj, err = sub.ReadObject(rctx); err != nil {
			t.Fatal(err)
		}
		if obj.Status == kolomoq.StatusNormal {
			break
		}
	}
	keys, sess, sender := c.keys, c.relaySess, c.sender
	op, err := keys.Opener(sess, ns, profileTrack)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := op.Open(obj); err != nil {
		t.Fatalf("the object did not open during the call: %v", err)
	}
	if err := guest.LeaveCall(); err != nil {
		t.Fatal(err)
	}
	op2, err := keys.Opener(sess, ns, profileTrack)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := op2.Open(obj); err == nil || !isNoKey(err) {
		t.Fatalf("after the call the object opens or fails with %v, want no key", err)
	}
	// The person's own key is erased too: the Sender seals nothing after the call.
	if _, _, err := sender.Seal(profileTrack, 1<<40, 0, 0, []byte("x"), nil); err == nil {
		t.Fatal("the Sender still seals after the call ended")
	}
}

func isNoKey(err error) bool {
	return err != nil && strings.Contains(err.Error(), secobj.ErrNoKey.Error())
}

// After the final "ended" no other event of the call reaches the window, and the events of a call have growing Seq.
func TestNoEventAfterEnded(t *testing.T) {
	ps := network(t, 2)
	startCall(t, ps)
	g := ps[1]
	entered, release := make(chan struct{}, 1), make(chan struct{})
	hold := func() {
		select {
		case entered <- struct{}{}:
			<-release // a snapshot taken, its event not sent yet
		default:
		}
	}
	g.emitHook.Store(&hold)
	go g.SetMicrophone(false)
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("no event was held")
	}
	left := make(chan struct{})
	go func() { g.LeaveCall(); close(left) }()
	time.Sleep(300 * time.Millisecond) // the teardown reaches its final event and waits behind the held one
	close(release)
	select {
	case <-left:
	case <-time.After(30 * time.Second):
		t.Fatal("LeaveCall did not return")
	}
	time.Sleep(300 * time.Millisecond)
	g.rec.mu.Lock()
	calls := append([]Call(nil), g.rec.calls...)
	g.rec.mu.Unlock()
	last := calls[len(calls)-1]
	if last.State != callEnded {
		t.Fatalf("the last event is %q, not ended", last.State)
	}
	var prev uint64
	n := 0
	for _, c := range calls {
		if c.ID == last.ID {
			if c.Seq <= prev {
				t.Fatalf("Seq %d after %d (event %d of the call, state %s)", c.Seq, prev, n, c.State)
			}
			prev = c.Seq
			n++
		}
	}
	if n < 3 || prev != last.Seq {
		t.Errorf("%d events; the ended one has Seq %d, the highest is %d", n, last.Seq, prev)
	}
}

// A change reported after the teardown made its final snapshot, before the final event, sends nothing and does not
// use a Seq: the window gets "ended" once, with the highest Seq.
func TestNoEventOnceTheEndIsDecided(t *testing.T) {
	c := bareCall(t, roleGuest, 8)
	var got []Call
	c.svc.emit = func(_ string, v any) {
		if call, ok := v.(Call); ok {
			got = append(got, call)
		}
	}
	c.seq = 4
	c.changed()
	if len(got) != 1 || got[0].Seq != 5 {
		t.Fatalf("before the end: %+v", got)
	}
	end := Call{State: callEnded, Seq: 5}
	c.endedSnap = &end
	c.changed()
	if len(got) != 1 || c.seq != 5 {
		t.Fatalf("after the final snapshot: %d events, seq %d", len(got), c.seq)
	}
}

// A call starts with nobody speaking: who spoke at the end of the last call does not hide the first words of the next.
func TestSpeakerMemoryIsPerCall(t *testing.T) {
	c1, c2 := bareCall(t, roleGuest, 8), bareCall(t, roleGuest, 8)
	var w speakerWatch
	levels := []Level{{ID: "self", Speaking: true}}
	w.update(c1, levels)
	w.update(c2, levels)
	if c1.lastSpeakID != "self" || c2.lastSpeakID != "self" {
		t.Errorf("speakers %q, %q", c1.lastSpeakID, c2.lastSpeakID)
	}
}

// The relay lets a guest announce itself only if the call has a place: the refusal is a request error the guest maps
// to "the call is full", not a close of the connection that races with its request.
func TestRelayRefusesAGuestWithoutAPlace(t *testing.T) {
	c := bareCall(t, roleHost, 2)
	c.ending = false
	in, late, stranger := bytes.Repeat([]byte{5}, 32), bytes.Repeat([]byte{6}, 32), bytes.Repeat([]byte{7}, 32)
	c.admit(in)
	ns := func(k []byte) kolomoq.Namespace { return c.room.memberNS(k) }
	ok := func(k []byte) bool {
		return c.relayAuthorize(k, kolomoq.KindPublishNamespace, ns(k), "") == nil
	}
	if !ok(in) {
		t.Fatal("an admitted guest, one place: it may announce")
	}
	// Now it is in the call, which (the host and one guest) is full, and a guest that was in before comes back.
	c.people[hex.EncodeToString(in)] = &participant{}
	c.admitted[string(in)].seen = true
	c.admit(late)
	c.admitted[string(late)].seen = true
	if !ok(in) {
		t.Error("a guest in the call is refused when it announces again")
	}
	if ok(late) {
		t.Error("a guest without a place may announce")
	}
	if ok(stranger) {
		t.Error("a stranger may announce")
	}
	if err := c.relayAuthorize(c.me, kolomoq.KindPublishNamespace, ns(c.me), ""); err != nil {
		t.Errorf("the host: %v", err)
	}
	if err := c.relayAuthorize(in, kolomoq.KindPublishNamespace, ns(late), ""); err == nil {
		t.Error("a guest announced another person's namespace")
	}
}

// The join request carries nothing but the invite: no name goes to the relay's identity.
func TestJoinRequestCarriesNoName(t *testing.T) {
	ps := network(t, 2)
	var mu sync.Mutex
	var seen [][]byte
	ps[0].requestHook = func(ev invite.Event) { mu.Lock(); seen = append(seen, ev.Data); mu.Unlock() }
	startCall(t, ps)
	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Fatal("the host saw no request")
	}
	for _, d := range seen {
		if len(d) != 0 {
			t.Errorf("a request carried %q", d)
		}
	}
}

// What is taken from another participant is bounded: the decoder, the track choice and the picture.
func TestRemotePictureLimits(t *testing.T) {
	if cfg := remoteDecoderConfig(); cfg.MaxPixels != maxFrameWidth*maxFrameHeight || cfg.MaxPixels >= 8912896/2 {
		t.Errorf("decoder %+v", cfg)
	}
	caps := watchCapabilities()
	if caps.MaxWidth != maxFrameWidth || caps.MaxHeight != maxFrameHeight || len(caps.Codecs) != 2 {
		t.Errorf("capabilities %+v", caps)
	}
	if sel, err := kolomedia.Select(&kolomedia.Catalog{Tracks: []kolomedia.Track{{Name: "camera", Kind: kolomedia.Video, Codec: kolomedia.VP8,
		Timescale: 90000, Width: 544, Height: 16383}}}, caps); err == nil && sel.Video != nil {
		t.Error("a 544x16383 track was selected")
	}
	var tile tileRenderer
	tall, _ := kolovideo.NewFrame(320, 10000)
	if _, ok := tile.render(tall, time.Now()); ok {
		t.Error("a 320x10000 frame was drawn")
	}
	wide, _ := kolovideo.NewFrame(maxFrameWidth+2, 2)
	if _, ok := tile.render(wide, time.Now()); ok {
		t.Error("a frame wider than the limit was drawn")
	}
	normal, _ := kolovideo.NewFrame(640, 480)
	if ev, ok := tile.render(normal, time.Now()); !ok || ev.Width != 240 || ev.Height != 180 {
		t.Errorf("a 640x480 frame: %+v %v", ev, ok)
	}
}

// A guest refuses more participants than the call holds, whoever lists them.
func TestGuestRefusesMoreThanTheCallHolds(t *testing.T) {
	c := bareCall(t, roleGuest, 3)
	for i := range 4 {
		key := bytes.Repeat([]byte{byte(10 + i)}, 32)
		c.rosterChange(key, true)
	}
	if n := len(c.people); n != 2 {
		t.Fatalf("a call of 3 holds %d others", n)
	}
	// One that is already in stays when it is listed again, and one that leaves makes room.
	first := bytes.Repeat([]byte{10}, 32)
	c.rosterChange(first, true)
	c.rosterChange(first, false)
	c.rosterChange(bytes.Repeat([]byte{14}, 32), true)
	if n := len(c.people); n != 2 {
		t.Errorf("after one left and one came, %d others", n)
	}
}

// A microphone that sends only zeros for a while is flagged, and the flag goes away with the first real sample.
func TestSilentMicrophoneHint(t *testing.T) {
	old := zeroMicAfter
	zeroMicAfter = 300 * time.Millisecond
	t.Cleanup(func() { zeroMicAfter = old })
	hw := newFakeHW(100, 500)
	hw.micSilent.Store(true)
	p := startPeer(t, "Eva", Config{First: freeUDP(t)}, hw, nil)
	if _, err := p.SetMicrophone(true); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the hint", func() bool {
		m := p.Lobby().Microphone
		return m.State == "on" && m.Hint != nil && m.Hint.Reason == "microphone-silent"
	})
	p.rec.mu.Lock()
	told := false
	for _, l := range p.rec.lobbies {
		told = told || l.Microphone.Hint != nil
	}
	p.rec.mu.Unlock()
	if !told {
		t.Error("the window was not told")
	}
	hw.mu.Lock()
	hw.mic.silent.Store(false)
	hw.mu.Unlock()
	waitUntil(t, "the hint gone", func() bool { return p.Lobby().Microphone.Hint == nil })
	// A quiet room is not digital silence: samples of 1 are not zeros.
	var l localMedia
	for range 100 {
		l.watchZeros(make([]int16, frameSamples))
	}
	if !l.silent.Load() {
		t.Error("all-zero frames did not set the flag")
	}
	l.watchZeros(append(make([]int16, frameSamples-1), 1))
	if l.silent.Load() {
		t.Error("a sample of 1 did not clear the flag")
	}
}

// While the notch setting is off, nothing goes to the notch page during a whole call.
func TestNotchGetsNothingWhileOff(t *testing.T) {
	ps := network(t, 2)
	a := ps[0]
	if _, err := a.SetShowInNotch(false); err != nil {
		t.Fatal(err)
	}
	a.rec.mu.Lock()
	before := len(a.rec.notchRaw)
	a.rec.mu.Unlock()
	startCall(t, ps)
	a.SetMicrophone(false)
	a.LeaveCall()
	a.rec.waitCall(t, "ended", func(c Call) bool { return c.State == callEnded })
	time.Sleep(200 * time.Millisecond)
	a.rec.mu.Lock()
	defer a.rec.mu.Unlock()
	if after := a.rec.notchRaw[before:]; len(after) != 0 {
		t.Fatalf("the notch page got %d events while off: %v", len(after), after)
	}
	if b, _ := json.Marshal(a.rec.notch[len(a.rec.notch)-1]); !strings.Contains(string(b), `"active":false`) {
		t.Errorf("the last thing the notch page got: %s", b)
	}
}

// Only the main window gets an invite from NewInvite; the call shows it all the same.
func TestNewInviteOnlyForTheMainWindow(t *testing.T) {
	ps := network(t, 1)
	a := ps[0]
	a.CreateCall()
	a.rec.waitCall(t, "active", func(c Call) bool { return c.State == callActive && c.Invite != "" })
	first, _ := a.rec.lastCall()
	if code, err := a.NewInvite(otherCtx()); err != nil || code != "" {
		t.Fatalf("the notch got %q, %v", code, err)
	}
	a.rec.waitCall(t, "a new invite", func(c Call) bool { return c.Invite != first.Invite })
	code, err := a.NewInvite(mainCtx())
	if err != nil || !strings.HasPrefix(code, "koloinv0") {
		t.Fatalf("the main window got %q, %v", code, err)
	}
}

// Who speaks is a participant, not a name: a person with your name does not make the notch say you are speaking.
func TestSpeakerIsByID(t *testing.T) {
	c := bareCall(t, roleGuest, 8)
	c.svc.saved.DisplayName = "Ana"
	other := &participant{id: "other", name: "Ana"}
	c.people["other"] = other
	c.order = []string{"other"}
	levels := []Level{{ID: "self"}, {ID: "other", Speaking: true}}
	if id := c.speakerID(levels); id != "other" {
		t.Fatalf("speaker %q", id)
	}
	c.changedSpeaker("other")
	if name, you := c.currentSpeaker(); name != "Ana" || you {
		t.Errorf("a person named like you speaks: %q, you %v", name, you)
	}
	c.changedSpeaker("self")
	if name, you := c.currentSpeaker(); name != "Ana" || !you {
		t.Errorf("you speak: %q, you %v", name, you)
	}
	if id := c.speakerID([]Level{{ID: "self", Speaking: true}, {ID: "other", Speaking: true}}); id != "self" {
		t.Errorf("you come first: %q", id)
	}
	if id := c.speakerID([]Level{{ID: "self"}}); id != "" {
		t.Errorf("nobody: %q", id)
	}
}

// A guest told the relay is gone hears why: the call was full while it joined, its own network is down, or the host left.
func TestRelayGoneReasons(t *testing.T) {
	cases := []struct {
		role, state, net, want string
	}{
		{roleGuest, callConnecting, netReady, "call-full"},
		{roleGuest, callActive, netReady, "call-ended-host-left"},
		{roleGuest, callActive, netFailed, "call-ended-lost"},
		{roleGuest, callConnecting, netFailed, "call-ended-lost"},
		{roleHost, callActive, netReady, "call-ended-lost"},
	}
	for _, tc := range cases {
		c := bareCall(t, tc.role, 8)
		c.ending = false
		c.state = tc.state
		c.svc.net = Network{State: tc.net}
		c.relayGone(nil)
		if c.endErr == nil || c.endErr.Reason != tc.want {
			t.Errorf("%+v: %+v", tc, c.endErr)
		}
	}
}

// LeaveCall that cannot finish the teardown in time says so instead of reporting success.
func TestLeaveCallTimeout(t *testing.T) {
	old := endTimeout
	endTimeout = 300 * time.Millisecond
	t.Cleanup(func() { endTimeout = old })
	ps := network(t, 1)
	a := ps[0]
	hold := make(chan struct{})
	var once sync.Once
	a.teardownHook = func() { <-hold }
	t.Cleanup(func() { once.Do(func() { close(hold) }) })
	a.CreateCall()
	a.rec.waitCall(t, "active", func(c Call) bool { return c.State == callActive })
	err := a.LeaveCall()
	if me, ok := err.(*MeetError); !ok || me.Reason != "call-still-ending" {
		t.Fatalf("LeaveCall: %v", err)
	}
	if _, err := a.CreateCall(); err == nil || err.(*MeetError).Reason != "busy-call" {
		t.Errorf("a call started while the first still ends: %v", err)
	}
	once.Do(func() { close(hold) })
	a.rec.waitCall(t, "ended", func(c Call) bool { return c.State == callEnded })
	endTimeout = 15 * time.Second
	waitUntil(t, "no call", func() bool { return a.activeCall() == nil })
}

// The video encoder is closed even when the camera track could not be added.
func TestPublisherClosesItsEncoder(t *testing.T) {
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	origin, err := kolomoq.NewOrigin(nil)
	if err != nil {
		t.Fatal(err)
	}
	sender, err := secobj.NewSender(origin, kolomoq.Namespace{"r", "k"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	bc, err := kolomedia.NewSecureBroadcast(origin, sender)
	if err != nil {
		t.Fatal(err)
	}
	bc.Close() // adding a track now fails
	f, _ := kolovideo.NewFrame(160, 120)
	for _, how := range []string{"setVideo", "close"} {
		p := newPublisher(bc, sender, testVideo, nil)
		p.onVideo(f, time.Now())
		if p.venc == nil || p.video != nil {
			t.Fatalf("setup: encoder %v, track %v", p.venc != nil, p.video != nil)
		}
		if how == "setVideo" {
			p.setVideo(false)
		} else {
			p.close()
		}
		if p.venc != nil {
			t.Errorf("%s left the encoder open", how)
		}
	}
}

// The microphone's packets all have one size, silent or not, so their sizes do not follow speech.
func TestAudioEncoderIsConstantBitrate(t *testing.T) {
	cfg := audioEncoderConfig()
	if !cfg.CBR || cfg.DTX {
		t.Fatalf("config %+v", cfg)
	}
	enc, err := opus.NewEncoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	sizes := map[int]bool{}
	out := make([]byte, opus.PacketBufferSize)
	pcm := make([]int16, frameSamples)
	for i := range 100 {
		for j := range pcm {
			pcm[j] = 0
			if i%2 == 1 {
				pcm[j] = int16(9000 * math.Sin(float64(i*frameSamples+j)*2*math.Pi*440/sampleRate))
			}
		}
		n, err := enc.Encode(pcm, out)
		if err != nil {
			t.Fatal(err)
		}
		sizes[n] = true
	}
	if len(sizes) != 1 {
		t.Errorf("packet sizes %v", sizes)
	}
}

// A binding that got the call just before it ended and reports a change afterwards sends nothing: the window's last
// word on a call is "ended".
func TestChangeAfterEndIsDropped(t *testing.T) {
	ps := network(t, 1)
	a := ps[0]
	a.CreateCall()
	a.rec.waitCall(t, "active", func(c Call) bool { return c.State == callActive })
	c := a.activeCall()
	if err := a.LeaveCall(); err != nil {
		t.Fatal(err)
	}
	a.rec.mu.Lock()
	n := len(a.rec.calls)
	last := a.rec.calls[n-1]
	a.rec.mu.Unlock()
	if last.State != callEnded {
		t.Fatalf("last event %q", last.State)
	}
	c.changed()
	c.changed()
	a.rec.mu.Lock()
	defer a.rec.mu.Unlock()
	if len(a.rec.calls) != n {
		t.Errorf("%d events after the end", len(a.rec.calls)-n)
	}
}

type fakePeer struct {
	key    ed25519.PublicKey
	topics []koloswarm.Topic
}

func (p fakePeer) PublicKey() ed25519.PublicKey { return p.key }
func (p fakePeer) Topics() []koloswarm.Topic    { return p.topics }
func (p fakePeer) Conn() koloswarm.Conn         { return nil }

// A guest takes the relay's connection for media only if it was made for the call's topic: the invite's connection
// to the same identity (or a dial of it in flight when the invite topic was left) must not be used, because the relay
// serves invites on it and no media session would ever start.
func TestGuestUsesOnlyTheCallsConnectionToTheRelay(t *testing.T) {
	c := bareCall(t, roleGuest, 8)
	c.ending = false
	c.state = callConnecting
	c.relayPeer = make(chan swarmPeer, 1)
	invite, call := koloswarm.Topic{1}, c.room.topic
	if c.takePeer(fakePeer{key: c.room.relay, topics: []koloswarm.Topic{invite}}) {
		t.Fatal("the invite's connection to the relay was taken for media")
	}
	select {
	case <-c.relayPeer:
		t.Fatal("it was handed to the join")
	default:
	}
	if !c.takePeer(fakePeer{key: c.room.relay, topics: []koloswarm.Topic{invite, call}}) {
		t.Fatal("the call's connection was refused")
	}
	select {
	case <-c.relayPeer:
	default:
		t.Fatal("the call's connection was not handed to the join")
	}
}
