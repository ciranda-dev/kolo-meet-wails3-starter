//go:build finished

package main

import (
	"errors"
	"os"
	"testing"
)

func TestCongestionConfig(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if c, err := LoadConfig([]string{"-congestion", "bbr"}, env(nil), os.Stderr); err != nil || c.Congestion != "bbr" {
		t.Errorf("flag: %+v %v", c, err)
	}
	if c, _ := LoadConfig(nil, env(map[string]string{"KOLO_MEET_CONGESTION": "cubic"}), os.Stderr); c.Congestion != "cubic" {
		t.Errorf("variable: %+v", c)
	}
	if c, _ := LoadConfig([]string{"-congestion", "reno"}, env(map[string]string{"KOLO_MEET_CONGESTION": "bbr"}), os.Stderr); c.Congestion != "reno" {
		t.Errorf("a flag wins over its variable: %+v", c)
	}
}

// reno is the default and bbr and cubic are chosen; anything else, including another case, is refused.
func TestCongestionResolve(t *testing.T) {
	s, err := Config{DataDir: t.TempDir()}.resolve()
	if err != nil || s.Congestion != congestionReno {
		t.Errorf("default: %q %v", s.Congestion, err)
	}
	for _, v := range []string{"reno", "cubic", "bbr"} {
		if s, err := (Config{DataDir: t.TempDir(), Congestion: v}).resolve(); err != nil || s.Congestion != v {
			t.Errorf("%s: %q %v", v, s.Congestion, err)
		}
	}
	for _, v := range []string{"BBR", "vegas", "bbr2", " bbr", "reno,bbr"} {
		var ce *ConfigError
		_, err := Config{DataDir: t.TempDir(), Congestion: v}.resolve()
		if !errors.As(err, &ce) || ce.Setting != "congestion" || !errors.Is(err, ErrMalformed) {
			t.Errorf("%q: %v", v, err)
		}
	}
}

// Every swarm that carries a call's media, the guests' and the host's relay swarm, gets the controller, and the
// default leaves kolo-transport's own, which is reno.
func TestCongestionReachesTheTransport(t *testing.T) {
	for _, v := range []string{"reno", "cubic", "bbr"} {
		s := NewMeetService(Config{DataDir: t.TempDir(), Congestion: v}, nil)
		set, err := s.cfg.resolve()
		if err != nil {
			t.Fatal(err)
		}
		s.set = set
		for which, streams := range map[string]int{swarmMember: maxStreams, swarmCallRelay: maxStreams, swarmCallInvite: inviteStreams} {
			c := s.swarmTransport(which)
			if c.CongestionControl != v || c.MaxIncomingStreams != streams {
				t.Errorf("%s with -congestion %s: %q, %d incoming streams, want %d", which, v, c.CongestionControl, c.MaxIncomingStreams, streams)
			}
		}
	}
	s := NewMeetService(Config{DataDir: t.TempDir()}, nil)
	set, _ := s.cfg.resolve()
	s.set = set
	if got := s.swarmTransport(swarmMember).CongestionControl; got != congestionReno {
		t.Errorf("default: %q", got)
	}
}

// A call between two people whose connections pace with bbr carries pictures and sound.
func TestCallOnBBR(t *testing.T) {
	first := freeUDP(t)
	mk := func(i int, cfg Config) *peer {
		cfg.Congestion = congestionBBR
		p := startPeer(t, parties[i].name, cfg, newFakeHW(parties[i].luma, parties[i].freq), nil)
		p.rec.waitNetwork(t, netReady)
		return p
	}
	a, b := mk(0, Config{First: first}), mk(1, Config{Bootstrap: first})
	startCall(t, []*peer{a, b})
	idA, idB := idOf(t, a), idOf(t, b)
	waitUntil(t, "pictures", func() bool { return a.rec.frameCount(idB) > 3 && b.rec.frameCount(idA) > 3 })
	waitUntil(t, "sound", func() bool { return tone(a.hw.heard(), 660) > 0.05 && tone(b.hw.heard(), 440) > 0.05 })
}
