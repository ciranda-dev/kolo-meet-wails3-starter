package main

// Tests of the starter only, with the made-up devices: no test opens a real camera, microphone or speaker. The
// finished app's own tests run with: go test -tags finished .

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	kolodht "github.com/ciranda-dev/kolo-dht"
)

func TestMadeUpDevices(t *testing.T) {
	var hw hardware = starterDevices
	cams, err := hw.Cameras()
	if err != nil || len(cams) != 1 || !strings.Contains(cams[0].Name, "Made-up") {
		t.Fatalf("cameras = %v, %v", cams, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cam, err := hw.OpenCamera(cams[0].ID, 64, 48, 30)
	if err != nil {
		t.Fatal(err)
	}
	f, err := cam.Read(ctx)
	if err != nil || f.Width != 64 || f.Height != 48 {
		t.Fatalf("frame = %v, %v", f, err)
	}
	cam.Close()
	if _, err := cam.Read(ctx); err == nil {
		t.Fatal("a closed camera still reads")
	}
	mic, err := hw.OpenMicrophone("")
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, frameSamples)
	if _, err := mic.Read(ctx, pcm); err != nil {
		t.Fatal(err)
	}
	loud := false
	for _, v := range pcm {
		loud = loud || v != 0
	}
	if !loud {
		t.Fatal("the made-up microphone is silent at the start of a second")
	}
	mic.Close()
}

func TestNotInStarterNamesTheStep(t *testing.T) {
	err := notInStarter(3)
	if !errors.Is(err, ErrNotInStarter) {
		t.Fatalf("%v is not ErrNotInStarter", err)
	}
	me := explainStart(err)
	if me.Reason != "call-unavailable" || !strings.Contains(me.Message, "not implemented in the starter (step 3 of the guide)") {
		t.Fatalf("explainStart = %+v", me)
	}
}

type starterEvents struct {
	mu     sync.Mutex
	net    []Network
	frames int
}

func (e *starterEvents) emit(name string, data any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch v := data.(type) {
	case Network:
		e.net = append(e.net, v)
	case FrameEvent:
		if v.ID == "self" {
			e.frames++
		}
	}
}

func (e *starterEvents) wait(t *testing.T, what string, ok func(*starterEvents) bool) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		e.mu.Lock()
		done := ok(e)
		e.mu.Unlock()
		if done {
			return
		}
	}
	t.Fatalf("never %s", what)
}

// TestStarterRunsTheLobby starts the service as the window does, on 127.0.0.1 with the first entry node, and turns
// the made-up camera and microphone on and off.
func TestStarterRunsTheLobby(t *testing.T) {
	c, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	first := c.LocalAddr().String()
	c.Close()
	t.Setenv("HOME", t.TempDir())
	ev := &starterEvents{}
	s := NewMeetService(Config{DataDir: filepath.Join(t.TempDir(), "data"), First: first}, ev.emit)
	s.hw = starterDevices
	if _, err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.ServiceShutdown()
	ev.wait(t, "ready", func(e *starterEvents) bool { return len(e.net) > 0 && e.net[len(e.net)-1].State == netReady })
	if cam, err := s.SetCamera(true); err != nil || cam.State != "on" {
		t.Fatalf("camera = %+v, %v", cam, err)
	}
	if mic, err := s.SetMicrophone(true); err != nil || mic.State != "on" {
		t.Fatalf("microphone = %+v, %v", mic, err)
	}
	ev.wait(t, "a self-view picture", func(e *starterEvents) bool { return e.frames > 0 })
	s.SetCamera(false)
	s.SetMicrophone(false)
	if mic, cam := s.media.isOpen(); mic || cam {
		t.Fatalf("devices still open: microphone %v, camera %v", mic, cam)
	}
}

// TestStartACallSaysStep3 starts a call before step 3 of the guide: it ends at once and says which step writes it.
func TestStartACallSaysStep3(t *testing.T) {
	if err := (&call{svc: &MeetService{}}).startHost(); !errors.Is(err, ErrNotInStarter) {
		t.Skip("step 3 is written")
	}
	c, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	first := c.LocalAddr().String()
	c.Close()
	t.Setenv("HOME", t.TempDir())
	var mu sync.Mutex
	var last Call
	ready := false
	s := NewMeetService(Config{DataDir: filepath.Join(t.TempDir(), "data"), First: first}, func(name string, data any) {
		mu.Lock()
		defer mu.Unlock()
		switch v := data.(type) {
		case Call:
			last = v
		case Network:
			ready = v.State == netReady
		}
	})
	s.hw = starterDevices
	if _, err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.ServiceShutdown()
	check := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			mu.Lock()
			done := ok()
			mu.Unlock()
			if done {
				return
			}
		}
		t.Fatalf("never %s; last call %+v", what, last)
	}
	check("ready", func() bool { return ready })
	if _, err := s.SetDisplayName("Ana"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCall(); err != nil {
		t.Fatal(err)
	}
	check("ended with step 3", func() bool {
		return last.State == callEnded && last.Error != nil &&
			last.Error.Message == "Could not start the call: not implemented in the starter (step 3 of the guide)."
	})
}

// The three networks, read from the settings: nothing is sent. Nothing configured is the public network, -bootstrap
// is your own network and is used alone, and -local is this computer only.
func TestStarterNetworkModes(t *testing.T) {
	pub, err := Config{DataDir: t.TempDir()}.resolve()
	if err != nil || pub.mode != modePublic || len(pub.nodes) != 0 || pub.nodesFrom != "public" || pub.entryNodeCount() != len(kolodht.PublicServers()) {
		t.Fatalf("default: %+v %v", pub, err)
	}
	own, err := Config{DataDir: t.TempDir(), Bootstrap: "203.0.113.7:7447"}.resolve()
	if err != nil || own.mode != modeOwn || len(own.nodes) != 1 || own.nodesFrom != "flag" {
		t.Fatalf("-bootstrap: %+v %v", own, err)
	}
	local, err := Config{DataDir: t.TempDir(), Local: true}.resolve()
	want := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), defaultLocalPort)
	if err != nil || local.mode != modeLocal || local.local != want || local.Listen != localListen {
		t.Fatalf("-local: %+v %v", local, err)
	}
	env := func(k string) string {
		return map[string]string{"KOLO_MEET_LOCAL": "1", "KOLO_BOOTSTRAP": "127.0.0.1:4000"}[k]
	}
	cfg, err := LoadConfig(nil, env, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DataDir = t.TempDir()
	if _, err := cfg.resolve(); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "-local") || !strings.Contains(err.Error(), "-bootstrap") {
		t.Errorf("-local with -bootstrap: %v", err)
	}
	if _, err := (Config{DataDir: t.TempDir(), Local: true, First: "127.0.0.1:4000"}).resolve(); !errors.Is(err, ErrConflict) {
		t.Errorf("-local with -first: %v", err)
	}
}

// A public network that does not answer says so and names the three options.
func TestStarterPublicUnreachableNamesTheOptions(t *testing.T) {
	pub, _ := Config{DataDir: t.TempDir()}.resolve()
	me := bootstrapFailure(pub, kolodht.ErrNoNodes, false)
	if me.Reason != "public-unreachable" {
		t.Fatalf("%+v", me)
	}
	for _, want := range []string{"public", "internet", "-bootstrap", "-local"} {
		if !strings.Contains(me.Message, want) {
			t.Errorf("%q does not name %q", me.Message, want)
		}
	}
}

// With -local the first window runs the entry node and the second joins it, with no -first and no -bootstrap.
func TestStarterLocalWindowsFindEachOther(t *testing.T) {
	c, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(c.LocalAddr().(*net.UDPAddr).Port)
	c.Close()
	t.Setenv("HOME", t.TempDir())
	var services []*MeetService
	for range 2 {
		ev := &starterEvents{}
		s := NewMeetService(Config{DataDir: filepath.Join(t.TempDir(), "data"), Local: true, source: loopbackSource(port)}, ev.emit)
		s.hw = starterDevices
		if _, err := s.Start(); err != nil {
			t.Fatal(err)
		}
		defer s.ServiceShutdown()
		ev.wait(t, "ready", func(e *starterEvents) bool { return len(e.net) > 0 && e.net[len(e.net)-1].State == netReady })
		if n := s.Info().Network; n.Mode != "local" || s.Settings().NetworkMode != "local" {
			t.Errorf("network %+v", n)
		}
		services = append(services, s)
	}
	if services[0].node == nil || services[1].node != nil {
		t.Errorf("the first window runs the entry node and the second joins it: %v %v", services[0].node != nil, services[1].node != nil)
	}
}
