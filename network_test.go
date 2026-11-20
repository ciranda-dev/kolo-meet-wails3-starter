//go:build finished

package main

import (
	"net"
	"strings"
	"testing"
)

// With -local the first window of the computer runs the entry node and the others join it: a call works between
// them with no -first and no -bootstrap.
func TestLocalWindowsMeetOnThisComputer(t *testing.T) {
	port := freePort(t)
	src := loopbackSource(port)
	var ps []*peer
	for _, who := range parties[:2] {
		p := startPeer(t, who.name, Config{Local: true, source: src}, newFakeHW(who.luma, who.freq), nil)
		p.rec.waitNetwork(t, netReady)
		ps = append(ps, p)
	}
	for _, p := range ps {
		n := p.Info().Network
		if n.Mode != "local" || n.EntryNodes != 1 {
			t.Errorf("network %+v", n)
		}
		if s := p.Settings(); s.NetworkMode != "local" || s.EntryNodesFrom != "local" || !strings.HasPrefix(s.EntryNodes, "127.0.0.1:") {
			t.Errorf("settings %+v", s)
		}
	}
	if ps[0].node == nil || ps[1].node != nil {
		t.Errorf("the first window runs the entry node and the second joins it: %v %v", ps[0].node != nil, ps[1].node != nil)
	}
	startCall(t, ps)
}

// A program that is not Kolo Meet holds the local port: the window says so and does not use another network.
func TestLocalPortTaken(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	port := uint16(pc.LocalAddr().(*net.UDPAddr).Port)
	p := startPeer(t, "Ana", Config{Local: true, source: loopbackSource(port)}, newFakeHW(60, 440), nil)
	p.rec.waitNetwork(t, netFailed)
	n := p.Info().Network
	if n.Error == nil || n.Error.Reason != "local-port-taken" || n.Error.Detail != pc.LocalAddr().String() || n.Mode != "local" {
		t.Fatalf("%+v", n)
	}
	if !strings.Contains(n.Message, "without -local") || !strings.Contains(n.Message, "-bootstrap") {
		t.Errorf("message %q", n.Message)
	}
}

// An own network whose entry node does not answer fails with the entry nodes named, and the window does not
// fall back to another network.
func TestOwnNetworkThatDoesNotAnswer(t *testing.T) {
	addr := freeUDP(t) // nothing listens there now
	p := startPeer(t, "Ana", Config{Bootstrap: addr}, newFakeHW(60, 440), nil)
	p.rec.waitNetwork(t, netFailed)
	n := p.Info().Network
	if n.Error == nil || n.Error.Reason != "bootstrap-failed" || n.Error.Detail != addr || n.Mode != "own" {
		t.Fatalf("%+v", n)
	}
}

// -bootstrap alone: the window starts on that node and reports an own network.
func TestOwnNetworkReportsItsMode(t *testing.T) {
	ps := network(t, 2)
	for _, p := range ps {
		if n := p.Info().Network; n.Mode != "own" || n.EntryNodes < 1 {
			t.Errorf("network %+v", n)
		}
		if s := p.Settings(); s.NetworkMode != "own" || s.EntryNodes == "" {
			t.Errorf("settings %+v", s)
		}
	}
}
