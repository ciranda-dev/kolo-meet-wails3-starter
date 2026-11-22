//go:build finished

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	kolodht "github.com/ciranda-dev/kolo-dht"
	invite "github.com/ciranda-dev/kolo-invite"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestLoadConfig(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	c, err := LoadConfig([]string{"-data", "/x", "-bootstrap", "127.0.0.1:4000", "-max-participants", "5"}, env(map[string]string{"KOLO_MEET_FIND_TIMEOUT": "9s"}), os.Stderr)
	if err != nil || c.DataDir != "/x" || c.MaxParticipants != 5 || c.FindTimeout != 9*time.Second || c.bootstrapFrom != "flag" {
		t.Fatalf("%+v %v", c, err)
	}
	if c, _ = LoadConfig(nil, env(map[string]string{"KOLO_BOOTSTRAP": "127.0.0.1:1"}), os.Stderr); c.bootstrapFrom != "env" || c.Bootstrap != "127.0.0.1:1" {
		t.Errorf("env: %+v", c)
	}
	if c, _ = LoadConfig([]string{"-local"}, env(nil), os.Stderr); !c.Local {
		t.Errorf("-local: %+v", c)
	}
	if c, _ = LoadConfig(nil, env(map[string]string{"KOLO_MEET_LOCAL": "1"}), os.Stderr); !c.Local {
		t.Errorf("KOLO_MEET_LOCAL=1: %+v", c)
	}
	if c, _ = LoadConfig([]string{"-local=false"}, env(map[string]string{"KOLO_MEET_LOCAL": "1"}), os.Stderr); c.Local {
		t.Errorf("a flag wins over its variable: %+v", c)
	}
	if c, _ = LoadConfig(nil, env(nil), os.Stderr); c.Local || c.Bootstrap != "" {
		t.Errorf("nothing given: %+v", c)
	}
	if _, err := LoadConfig(nil, env(map[string]string{"KOLO_MEET_LOCAL": "maybe"}), os.Stderr); !errors.Is(err, ErrConfig) || !errors.Is(err, ErrMalformed) {
		t.Errorf("a bad KOLO_MEET_LOCAL: %v", err)
	}
	if _, err := LoadConfig([]string{"-nope"}, env(nil), &bytes.Buffer{}); err == nil {
		t.Error("an unknown flag")
	}
	if _, err := LoadConfig([]string{"-h"}, env(nil), &bytes.Buffer{}); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h: %v", err)
	}
	if _, err := LoadConfig(nil, env(map[string]string{"KOLO_MEET_FIND_TIMEOUT": "soon"}), os.Stderr); !errors.Is(err, ErrConfig) {
		t.Errorf("bad duration: %v", err)
	}
}

func TestResolve(t *testing.T) {
	bad := []Config{
		{MaxParticipants: 1}, {MaxParticipants: 21}, {MaxParticipants: -3}, {FindTimeout: time.Millisecond},
		{LookupInterval: time.Millisecond}, {LookupInterval: 2 * time.Hour}, {First: "10.0.0.1:4000"}, {First: "127.0.0.1:0"},
		{First: "[::1]:4000"}, {Listen: "[::1]:0"}, {Bootstrap: "not a node"}, {Bootstrap: "[::1]:4000"},
	}
	for _, c := range bad {
		c.DataDir = t.TempDir()
		if _, err := c.resolve(); !errors.Is(err, ErrConfig) {
			t.Errorf("%+v resolved: %v", c, err)
		}
	}
	s, err := Config{DataDir: t.TempDir(), Bootstrap: "127.0.0.1:4000"}.resolve()
	if err != nil || s.MaxParticipants != defaultMaxParticipants || s.Listen != localListen || s.nodesFrom != "flag" {
		t.Fatalf("%+v %v", s, err)
	}
	if s, _ = (Config{DataDir: t.TempDir(), Bootstrap: "example.org:4000"}).resolve(); s.Listen != publicListen {
		t.Errorf("listen %q", s.Listen)
	}
	if s, _ = (Config{DataDir: t.TempDir(), First: "127.0.0.1:4000"}).resolve(); len(s.nodes) != 1 || s.Listen != localListen || s.mode != modeOwn || s.nodesFrom != "first" {
		t.Errorf("first node: %+v", s)
	}
}

// Nothing configured is the public network: no entry node of the app's own, so kolo-swarm uses kolo-dht's list.
// The test reads the settings and sends nothing.
func TestDefaultIsThePublicNetwork(t *testing.T) {
	s, err := Config{DataDir: t.TempDir()}.resolve()
	if err != nil || s.mode != modePublic || s.nodesFrom != "public" || len(s.nodes) != 0 || s.nodesText != "" || s.local.IsValid() || s.first.IsValid() {
		t.Fatalf("%+v %v", s, err)
	}
	if s.Listen != publicListen {
		t.Errorf("listen %q, want %q", s.Listen, publicListen)
	}
	if want := len(kolodht.PublicServers()); want == 0 || s.entryNodeCount() != want {
		t.Errorf("entry nodes %d, want the %d public servers", s.entryNodeCount(), want)
	}
	svc := NewMeetService(Config{DataDir: s.DataDir}, func(string, any) {})
	svc.set = s
	if got := svc.Settings(); got.NetworkMode != "public" || got.EntryNodes != "" || got.EntryNodesFrom != "public" {
		t.Errorf("settings %+v", got)
	}
	// The window is told the mode and a count, never the public addresses, and so is the error it shows.
	svc.setNetwork(netStarting, nil)
	svc.setNetwork(netFailed, meetErr("public-unreachable", kolodht.ErrNoNodes, ""))
	payload, err := json.Marshal(struct {
		Info     Info
		Settings Settings
	}{svc.Info(), svc.Settings()})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range kolodht.PublicServers() {
		if bytes.Contains(payload, []byte(p.Addr().String())) {
			t.Errorf("the payload lists the public entry node %s: %s", p, payload)
		}
	}
	if !bytes.Contains(payload, []byte(`"mode":"public"`)) {
		t.Errorf("the payload lacks the mode: %s", payload)
	}
}

// -bootstrap is used alone: the public list is not added, and the network is the app's own.
func TestBootstrapIsYourOwnNetwork(t *testing.T) {
	s, err := Config{DataDir: t.TempDir(), Bootstrap: "203.0.113.7:7447,seed.example.org:7447", bootstrapFrom: "env"}.resolve()
	if err != nil || s.mode != modeOwn || s.nodesFrom != "env" || len(s.nodes) != 2 || s.entryNodeCount() != 2 {
		t.Fatalf("%+v %v", s, err)
	}
	for _, n := range s.nodes {
		for _, p := range kolodht.PublicServers() {
			if n == p.String() {
				t.Errorf("the public server %s is in an own network", n)
			}
		}
	}
	if s.local.IsValid() || s.Listen != publicListen {
		t.Errorf("%+v", s)
	}
	if s, err = (Config{DataDir: t.TempDir(), Bootstrap: "127.0.0.1:4000"}).resolve(); err != nil || s.mode != modeOwn || s.nodesFrom != "flag" || s.Listen != localListen {
		t.Errorf("a loopback own network: %+v %v", s, err)
	}
}

// -local is this computer only: one entry node on loopback, the default port unless a test gives another.
func TestLocalIsThisComputerOnly(t *testing.T) {
	s, err := Config{DataDir: t.TempDir(), Local: true}.resolve()
	want := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), defaultLocalPort)
	if err != nil || s.mode != modeLocal || s.nodesFrom != "local" || s.local != want || len(s.nodes) != 1 || s.nodes[0] != want.String() || s.Listen != localListen {
		t.Fatalf("%+v %v", s, err)
	}
	if s, err = (Config{DataDir: t.TempDir(), Local: true, source: loopbackSource(7000)}).resolve(); err != nil || s.local.String() != "127.0.0.1:7000" || s.entryNodeCount() != 1 {
		t.Errorf("%+v %v", s, err)
	}
}

// -local with -bootstrap or -first is an error that names both options, whoever gave them.
func TestLocalWithAnotherNetworkIsAnError(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for name, c := range map[string]struct {
		args []string
		env  map[string]string
		with string
	}{
		"flags":             {[]string{"-local", "-bootstrap", "127.0.0.1:4000"}, nil, "-bootstrap"},
		"variables":         {nil, map[string]string{"KOLO_MEET_LOCAL": "1", "KOLO_BOOTSTRAP": "127.0.0.1:4000"}, "-bootstrap"},
		"flag and variable": {[]string{"-local"}, map[string]string{"KOLO_BOOTSTRAP": "127.0.0.1:4000"}, "KOLO_BOOTSTRAP"},
		"first":             {[]string{"-local", "-first", "127.0.0.1:4000"}, nil, "-first"},
	} {
		cfg, err := LoadConfig(c.args, env(c.env), os.Stderr)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cfg.DataDir = t.TempDir()
		_, err = cfg.resolve()
		var ce *ConfigError
		if !errors.Is(err, ErrConfig) || !errors.Is(err, ErrConflict) || !errors.As(err, &ce) || ce.Setting != "local" ||
			!strings.Contains(err.Error(), c.with) || !strings.Contains(err.Error(), "-local") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The public network that does not answer says so with the three ways out; an own network that does not answer
// names its entry nodes. Nothing falls back to another network.
func TestUnreachableNetworkErrors(t *testing.T) {
	cause := kolodht.ErrNoNodes
	pub, _ := Config{DataDir: t.TempDir()}.resolve()
	me := bootstrapFailure(pub, cause, false)
	if me.Reason != "public-unreachable" || me.Kind != kindNetwork {
		t.Fatalf("%+v", me)
	}
	for _, want := range []string{"public", "internet", "-bootstrap", "-local"} {
		if !strings.Contains(me.Message, want) {
			t.Errorf("%q does not name %q", me.Message, want)
		}
	}
	own, _ := Config{DataDir: t.TempDir(), Bootstrap: "203.0.113.7:7447"}.resolve()
	if me = bootstrapFailure(own, cause, false); me.Reason != "bootstrap-failed" || me.Detail != "203.0.113.7:7447" {
		t.Errorf("%+v", me)
	}
	local, _ := Config{DataDir: t.TempDir(), Local: true}.resolve()
	if me = bootstrapFailure(local, cause, false); me.Reason != "local-port-taken" || me.Detail != local.local.String() {
		t.Errorf("%+v", me)
	}
}

// Every reason has a kind, an English sentence, and no leftover placeholder when filled in.
func TestReasons(t *testing.T) {
	kinds := map[string]bool{kindInvalidInvite: true, kindNotFound: true, kindRefused: true, kindExpired: true, kindBusy: true, kindNetwork: true,
		kindPermission: true, kindDevice: true, kindMedia: true, kindName: true, kindConfig: true, kindEnded: true, kindCancelled: true, kindInternal: true}
	for id, r := range reasons {
		if !kinds[r.kind] || r.text == "" {
			t.Errorf("%s: %+v", id, r)
		}
		if strings.Contains(r.text, "in Settings") || strings.Contains(r.text, "them in Settings") {
			t.Errorf("%s sends to Settings, which has no network settings: %q", id, r.text)
		}
		e := meetErr(id, errors.New("boom"), "D")
		if strings.Contains(e.Message, "{") || strings.Contains(e.Message, "kolo-meet:") || strings.Contains(e.Message, "\u2014") {
			t.Errorf("%s: %q", id, e.Message)
		}
		if strings.Contains(r.text, "{detail}") && e.Detail == "" || e.Reason != id || e.Kind != r.kind {
			t.Errorf("%s: %+v", id, e)
		}
	}
	for _, name := range []string{"camera", "microphone", "speaker"} {
		for _, suffix := range []string{"-denied", "-none", "-lost", "-failed"} {
			if name == "speaker" && suffix == "-denied" {
				continue
			}
			if _, ok := reasons[name+suffix]; !ok {
				t.Errorf("no reason %s%s", name, suffix)
			}
		}
	}
}

func TestExplain(t *testing.T) {
	cases := []struct {
		err    error
		reason string
	}{
		{nil, ""},
		{ErrInvalidInvite, "invalid-invite"},
		{invite.ErrInvalidCode, "invalid-invite"},
		{invite.ErrDenied, "join-denied"},
		{invite.ErrExpired, "invite-expired"},
		{invite.ErrUsedUp, "invite-used-up"},
		{invite.ErrRevoked, "invite-revoked"},
		{invite.ErrInvalid, "invite-unknown"},
		{invite.ErrRateLimited, "join-rate-limited"},
		{invite.ErrBusy, "host-busy"},
		{invite.ErrProtocol, "bad-grant"},
		{ErrBadGrant, "bad-grant"},
		{invite.ErrNotAnswered, "host-lost"},
		{invite.ErrUnreachable, "host-not-found"},
		{ErrNotFound, "host-not-found"},
		{context.Canceled, "cancelled"},
		{ErrNotReady, "network-not-ready"},
		{ErrBusy, "busy-call"},
		{ErrNoCall, "no-call"},
		{ErrClosed, "app-closed"},
		{ErrUnknownDevice, "device-unknown"},
		{errors.New("x"), "internal"},
	}
	for _, tc := range cases {
		got := explain(tc.err)
		switch {
		case tc.err == nil && got != nil:
			t.Errorf("nil: %+v", got)
		case tc.err != nil && (got == nil || got.Reason != tc.reason || !errors.Is(got, tc.err)):
			t.Errorf("%v: %+v", tc.err, got)
		}
	}
	e := meetErr("name-empty", ErrBadName, "")
	if explain(e) != e {
		t.Error("a MeetError is explained as itself")
	}
}

func TestNames(t *testing.T) {
	ok := map[string]string{
		"Ana": "Ana", "  Ana   Maria\t": "Ana Maria", "José": "José", "李雷": "李雷", "a\u200bb": "ab", "x\u0000y": "xy",
		"tab\there": "tab here", "line\nbreak": "line break", "\u202eevil": "evil", "a\u2028b": "a b",
		strings.Repeat("é", maxNameRunes): strings.Repeat("é", maxNameRunes),
	}
	for in, want := range ok {
		if got, err := cleanName(in); err != nil || got != want {
			t.Errorf("cleanName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "   ", "\u200b\u0000", strings.Repeat("a", maxNameRunes+1), "\xff\xfe"} {
		var me *MeetError
		if _, err := cleanName(in); !errors.As(err, &me) || !errors.Is(err, ErrBadName) {
			t.Errorf("cleanName(%q): %v", in, err)
		}
	}
	key := bytes.Repeat([]byte{0xab}, 32)
	if got := peerName("", key); got != "Guest abab" {
		t.Errorf("peerName empty: %q", got)
	}
	if got := peerName(strings.Repeat("x", 500), key); utf8.RuneCountInString(got) != maxNameRunes {
		t.Errorf("peerName long: %d", len(got))
	}
}

func FuzzCleanName(f *testing.F) {
	for _, s := range []string{"Ana", "", "\u202e", strings.Repeat("é", 60), "a\x00b", "\xff"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		name, err := cleanName(s)
		if err == nil {
			if !utf8.ValidString(name) || name == "" || utf8.RuneCountInString(name) > maxNameRunes || name != tidyName(name) {
				t.Fatalf("%q -> %q", s, name)
			}
		}
		if got := peerName(s, make([]byte, 32)); !utf8.ValidString(got) || got == "" || utf8.RuneCountInString(got) > maxNameRunes+6 {
			t.Fatalf("peerName(%q) = %q", s, got)
		}
	})
}

func testGrant(t *testing.T) (grant, ed25519.PublicKey) {
	t.Helper()
	relay := bytes.Repeat([]byte{2}, 32)
	return grant{Version: grantVersion, Room: strings.Repeat("ab", 16), Seed: strings.Repeat("cd", 32), Relay: hex.EncodeToString(relay),
		Host: strings.Repeat("ef", 32), HostName: "Ana", Max: 8}, relay
}

func TestParseGrant(t *testing.T) {
	g, inviter := testGrant(t)
	enc := func(g grant) []byte { b, _ := json.Marshal(g); return b }
	r, err := parseGrant(enc(g))
	if err != nil || r.id != g.Room || r.max != 8 || r.hostName != "Ana" || !bytes.Equal(r.relay, inviter) {
		t.Fatalf("%+v %v", r, err)
	}
	if want := roomTopic(bytes.Repeat([]byte{0xcd}, 32)); r.topic != want {
		t.Error("topic")
	}
	if ns := r.memberNS(inviter); len(ns) != 2 || ns[0] != g.Room || ns[1] != hex.EncodeToString(inviter) || !r.owns(inviter, ns) || r.owns(r.host, ns) {
		t.Errorf("namespace %v", ns)
	}
	mut := func(f func(*grant)) []byte { c := g; f(&c); return enc(c) }
	bad := map[string][]byte{
		"empty": nil, "not json": []byte("{"), "array": []byte("[]"), "trailing": append(enc(g), ' ', '{', '}'),
		"unknown field": append(bytes.TrimSuffix(enc(g), []byte("}")), []byte(`,"x":1}`)...),
		"version":       mut(func(g *grant) { g.Version = 2 }),
		"room short":    mut(func(g *grant) { g.Room = "ab" }),
		"room upper":    mut(func(g *grant) { g.Room = strings.Repeat("AB", 16) }),
		"seed":          mut(func(g *grant) { g.Seed = "zz" }),
		"relay":         mut(func(g *grant) { g.Relay = "00" }),
		"host is relay": mut(func(g *grant) { g.Host = g.Relay }),
		"max low":       mut(func(g *grant) { g.Max = 1 }),
		"max high":      mut(func(g *grant) { g.Max = maxParticipantsCeiling + 1 }),
		"huge":          bytes.Repeat([]byte("x"), maxGrantBytes+1),
	}
	for name, b := range bad {
		if _, err := parseGrant(b); !errors.Is(err, ErrBadGrant) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if r, err := parseGrant(mut(func(g *grant) { g.HostName = "\u202e" })); err != nil || r.hostName == "" || strings.ContainsRune(r.hostName, '\u202e') {
		t.Errorf("host name: %q %v", r.hostName, err)
	}
}

func FuzzParseGrant(f *testing.F) {
	g, _ := testGrant(&testing.T{})
	b, _ := json.Marshal(g)
	f.Add(b)
	f.Add([]byte(`{"v":1}`))
	f.Add([]byte(""))
	g.Relays = []string{strings.Repeat("ef", 32), strings.Repeat("01", 32)}
	b, _ = json.Marshal(g)
	f.Add(b)
	f.Fuzz(func(t *testing.T, b []byte) {
		r, err := parseGrant(b)
		if err == nil && (r == nil || len(r.relay) != 32 || len(r.host) != 32 || !hex16.MatchString(r.id) || r.max < 2 || r.max > maxParticipantsCeiling) {
			t.Fatalf("accepted %q as %+v", b, r)
		}
		if err != nil {
			if !errors.Is(err, ErrBadGrant) {
				t.Fatalf("error %v", err)
			}
			return
		}
		if len(r.relayKeys) > maxRelayKeys {
			t.Fatalf("accepted %d relay keys", len(r.relayKeys))
		}
		for i, k := range r.relayKeys {
			if len(k) != 32 || (i > 0 && slices.ContainsFunc(r.relayKeys[:i], func(x []byte) bool { return bytes.Equal(x, k) })) {
				t.Fatalf("relay key %d of %q", i, b)
			}
		}
	})
}

func TestParseProfile(t *testing.T) {
	key := bytes.Repeat([]byte{9}, 32)
	b, _ := encodeProfile("Ana")
	if name, err := parseProfile(b, key); err != nil || name != "Ana" {
		t.Fatalf("%q %v", name, err)
	}
	for _, bad := range []string{``, `{`, `{"v":2,"name":"x"}`, `{"v":1,"name":"x","extra":1}`, `{"v":1,"name":"x"} {}`, `{"v":1,"name":5}`, strings.Repeat("x", maxProfileBytes+1)} {
		if _, err := parseProfile([]byte(bad), key); !errors.Is(err, ErrBadGrant) {
			t.Errorf("%.30q: %v", bad, err)
		}
	}
	hostile, _ := encodeProfile("\u202e" + strings.Repeat("x", 200))
	if name, err := parseProfile(hostile, key); err != nil || utf8.RuneCountInString(name) > maxNameRunes || strings.ContainsRune(name, '\u202e') {
		t.Errorf("hostile name %q %v", name, err)
	}
}

func FuzzParseProfile(f *testing.F) {
	b, _ := encodeProfile("Ana")
	f.Add(b)
	f.Add([]byte(`{"v":1,"name":"\u0000"}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		name, err := parseProfile(b, make([]byte, 32))
		if err == nil && (!utf8.ValidString(name) || name == "" || utf8.RuneCountInString(name) > maxNameRunes+6) {
			t.Fatalf("name %q from %q", name, b)
		}
	})
}

// The texts say what is true: the relay's operator is named, "end-to-end encrypted" comes with what the relay still
// sees and with decision 0010's sentence about forging, and it is said only while every key has arrived. There is no
// text for an unprotected call because there is no such call.
func TestSecurityTexts(t *testing.T) {
	guest := securityOf(3, 0, "Ana", false, 0)
	host := securityOf(2, 0, "Ana", true, 0)
	waiting := securityOf(3, 1, "Ana", false, 0)
	for _, s := range []Security{guest, host, waiting} {
		if s.Mode != modeGroupKeysHostRelay || !strings.Contains(s.Header, "through relay run by") {
			t.Errorf("header %q", s.Header)
		}
		for _, must := range []string{"cannot see audio, video or names, not even how long a name is",
			"Anyone in the call could, with the relay's help, make audio, video or names that seem to come from someone else.",
			"who is in the call", "who watches whom", "when each person sends and how much",
			"when microphones and cameras are off", "the room ID", "drop or delay media", "Anyone in the call can read everyone's media",
			"no recovery after a compromise", "could make audio, video or names that seem to come from someone else."} {
			if !strings.Contains(s.Detail, must) {
				t.Errorf("detail lacks %q: %s", must, s.Detail)
			}
		}
		if strings.Contains(s.Header+s.Detail, "messages") || strings.Contains(strings.ToLower(s.Header+s.Detail), "not end-to-end") {
			t.Errorf("claims something that does not exist: %s %s", s.Header, s.Detail)
		}
	}
	if guest.Header != "3 in call, end-to-end encrypted, through relay run by Ana" || !guest.Complete || !strings.Contains(guest.Detail, "Ana runs the relay, so Ana could make") {
		t.Errorf("guest %+v", guest)
	}
	if host.Header != "2 in call, end-to-end encrypted, through relay run by you" || !strings.Contains(host.Detail, "You run the relay, so you could make") {
		t.Errorf("host %+v", host)
	}
	// With a key missing the header does not say end-to-end (decision 0010), and the detail says what does not play.
	if waiting.Header != "3 in call, 1 waiting for keys, through relay run by Ana" || waiting.Complete || waiting.Waiting != 1 ||
		strings.Contains(waiting.Header, "end-to-end") || !strings.Contains(waiting.Detail, "Nothing from a person who is waiting for keys is played.") {
		t.Errorf("waiting %+v", waiting)
	}
	if leftSentence("Bruno") != "Bruno no longer receives your media." {
		t.Error(leftSentence("Bruno"))
	}
}

// Every profile object has the same size, whatever the name, so the relay does not learn the length of a name; the
// padding is read back as the name alone.
func TestProfileHasOneSize(t *testing.T) {
	key := bytes.Repeat([]byte{9}, 32)
	for _, name := range []string{"A", "Ana", strings.Repeat("é", maxNameRunes), strings.Repeat("<", maxNameRunes), strings.Repeat("😀", maxNameRunes)} {
		b, err := encodeProfile(name)
		if err != nil || len(b) != maxProfileBytes {
			t.Fatalf("%q: %d bytes, %v", name, len(b), err)
		}
		if got, err := parseProfile(b, key); err != nil || got != peerName(name, key) {
			t.Errorf("%q read back as %q, %v", name, got, err)
		}
	}
}

// The call moves only along its edges.
func TestCallStateMachine(t *testing.T) {
	states := []string{"", callCreating, callJoining, callConnecting, callActive, callEnded}
	allowed := map[[2]string]bool{
		{"", callCreating}: true, {"", callJoining}: true, {callCreating, callActive}: true, {callCreating, callEnded}: true,
		{callJoining, callConnecting}: true, {callJoining, callEnded}: true, {callConnecting, callActive}: true,
		{callConnecting, callEnded}: true, {callActive, callEnded}: true,
	}
	for _, from := range states {
		for _, to := range states {
			if got := canMove(from, to); got != allowed[[2]string{from, to}] {
				t.Errorf("canMove(%q, %q) = %v", from, to, got)
			}
		}
	}
	c := &call{svc: &MeetService{emit: func(string, any) {}}, state: callCreating}
	c.svc.media = newLocalMedia(newFakeHW(1, 1), defaultVideo, nil, nil, nil)
	if c.move(callJoining, "") || c.state != callCreating {
		t.Error("creating moved to joining")
	}
	if !c.move(callActive, "") || c.started.IsZero() {
		t.Error("creating did not move to active")
	}
	if c.move(callConnecting, "") || c.move(callCreating, "") {
		t.Error("active moved back")
	}
}

func TestSeatsAndMembers(t *testing.T) {
	me := bytes.Repeat([]byte{1}, 32)
	g1, g2, g3 := bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32)
	c := &call{me: me, room: &room{max: 3}, people: map[string]*participant{}, admitted: map[string]*admission{}}
	if !c.isMember(me) || c.isMember(g1) {
		t.Fatal("host, or a stranger")
	}
	c.admit(g1)
	c.admit(g2)
	if c.freeSlots() != 0 {
		t.Errorf("two guests let in, %d free", c.freeSlots())
	}
	if !c.isMember(g1) || !c.isMember(g2) || c.isMember(g3) {
		t.Error("members")
	}
	c.people[hex.EncodeToString(g1)] = &participant{}
	c.admitted[string(g1)].seen = true
	delete(c.people, hex.EncodeToString(g1)) // g1 left: its place is free, g2's is still held
	if c.freeSlots() != 1 {
		t.Errorf("after a guest left, %d free", c.freeSlots())
	}
	if !c.isMember(g1) {
		t.Error("a guest that left cannot come back while there is a place")
	}
	c.admit(g3)
	if c.freeSlots() != 0 || c.isMember(g1) {
		t.Errorf("free %d, g1 member %v", c.freeSlots(), c.isMember(g1))
	}
	c.admitted[string(g2)].at = time.Now().Add(-2 * admitGrace) // never appeared: its place expires
	if c.freeSlots() != 1 {
		t.Errorf("after the grace, %d free", c.freeSlots())
	}
}

func TestHistoryAndSettingsFiles(t *testing.T) {
	dir := t.TempDir()
	h, err := openHistory(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := range maxHistory + 5 {
		if _, err := h.add(HistoryItem{ID: string(rune('a' + i%26)), At: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if got := h.list(); len(got) != maxHistory || got[0].At != int64(maxHistory+4) {
		t.Errorf("history %d, newest %d", len(got), got[0].At)
	}
	h2, _ := openHistory(dir)
	if len(h2.list()) != maxHistory {
		t.Error("history not persisted")
	}
	if info, _ := os.Stat(filepath.Join(dir, historyFile)); info.Mode().Perm()&0o077 != 0 {
		t.Errorf("history mode %v", info.Mode())
	}
	os.WriteFile(filepath.Join(dir, historyFile), []byte("{not json"), 0o600)
	if h3, err := openHistory(dir); err != nil || len(h3.list()) != 0 {
		t.Errorf("a damaged history: %v", err)
	}
	h.clear()
	if len(h.list()) != 0 {
		t.Error("clear")
	}

	if s, err := loadSaved(dir); err != nil || s.DisplayName != "" || !s.showInNotch() {
		t.Errorf("no file: %+v %v", s, err)
	}
	off := false
	if err := saveSettings(dir, savedSettings{CameraID: "cam-1", ShowInNotch: &off, DisplayName: "Ana\u202e"}); err != nil {
		t.Fatal(err)
	}
	if s, _ := loadSaved(dir); s.CameraID != "cam-1" || s.showInNotch() || s.DisplayName != "Ana" {
		t.Errorf("saved: %+v", s) // a name is cleaned again when it is read
	}
	os.WriteFile(filepath.Join(dir, settingsFile), bytes.Repeat([]byte("x"), maxSettingsFile+1), 0o600)
	if s, err := loadSaved(dir); err != nil || s.CameraID != "" {
		t.Errorf("oversized: %+v %v", s, err)
	}
}

func TestTrayAndNotchOf(t *testing.T) {
	if st := trayOf(nil); st.Active || st.Status != "No call" {
		t.Errorf("%+v", st)
	}
	c := &Call{State: callCreating}
	if st := trayOf(c); !st.Active || st.Status != "Starting a call" || st.InCall {
		t.Errorf("%+v", st)
	}
	c.State = callJoining
	if st := trayOf(c); st.Status != "Joining a call" {
		t.Errorf("%+v", st)
	}
	c = &Call{State: callActive, Invite: "koloinv0secret", Participants: []Participant{{}}, Microphone: MediaState{State: "on"}}
	st := trayOf(c)
	if !st.InCall || !st.HasInvite || st.Status != "In a call, waiting for others" || !st.Microphone || st.Camera {
		t.Errorf("%+v", st)
	}
	if b, _ := json.Marshal(st); strings.Contains(string(b), "secret") {
		t.Error("the tray holds the invite")
	}
	c.Participants = make([]Participant, 4)
	if st := trayOf(c); st.Status != "In a call with 4 people" {
		t.Errorf("%+v", st)
	}
	n := notchOf(*c, "Ana", false)
	if !n.Active || n.Participants != 4 || n.Speaker != "Ana" || !n.HasInvite || !n.Microphone {
		t.Errorf("%+v", n)
	}
	if b, _ := json.Marshal(n); strings.Contains(string(b), "secret") {
		t.Error("the notch holds the invite")
	}
	end := notchOf(Call{State: callEnded, Invite: "koloinv0secret"}, "", false)
	if end.Active || end.HasInvite || end.Message != "You left the call." {
		t.Errorf("%+v", end)
	}
	end = notchOf(Call{State: callEnded, Error: meetErr("call-ended-host-left", nil, "")}, "", false)
	if end.Message != reasons["call-ended-host-left"].text || end.Error == nil {
		t.Errorf("%+v", end)
	}
}

func TestMeterAndSpeaking(t *testing.T) {
	for db, want := range map[float64]float64{-100: 0, meterFloor: 0, 0: 1, 5: 1, -30: 0.5} {
		if got := meter(db); got != want {
			t.Errorf("meter(%v) = %v, want %v", db, got, want)
		}
	}
	var s speakTracker
	now := time.Now()
	if s.update(-80, now) {
		t.Error("silence is speaking")
	}
	if !s.update(-20, now) || !s.update(-80, now.Add(speakingHold/2)) || s.update(-80, now.Add(2*speakingHold)) {
		t.Error("hold")
	}
}

func TestMixerMixesAndBounds(t *testing.T) {
	m := newMixer(nil)
	a := make([]int16, frameSamples)
	for i := range a {
		a[i] = 1000
	}
	for range mixQueueFrames + 10 {
		m.push("a", a)
	}
	m.push("b", a)
	if len(m.queues["a"]) != mixQueueFrames {
		t.Errorf("queue %d", len(m.queues["a"]))
	}
	out := make([]int16, frameSamples)
	if !m.mix(out) || out[0] != 2000 {
		t.Errorf("mix %d", out[0])
	}
	m.drop("a")
	m.drop("b")
	if m.mix(out) || out[0] != 0 {
		t.Error("empty mix")
	}
}

func TestHelpNamesTheFindTimeoutWithRelays(t *testing.T) {
	var out bytes.Buffer
	if _, err := LoadConfig([]string{"-h"}, func(string) string { return "" }, &out); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(out.String(), "default 1m, 72s with connection relays") || relayFindTimeout != 72*time.Second {
		t.Errorf("help: %s", out.String())
	}
	if strings.Contains(out.String(), "Settings") {
		t.Errorf("the window has no network settings, the help must not send there: %s", out.String())
	}
}

// fakeShownWindow records what a click on the tray icon does to the main window.
type fakeShownWindow struct {
	visible bool
	calls   []string
}

func (w *fakeShownWindow) IsVisible() bool { return w.visible }
func (w *fakeShownWindow) Hide() application.Window {
	w.calls, w.visible = append(w.calls, "hide"), false
	return nil
}

// A click on the tray icon hides the window and the next one opens the app. Nothing on that path closes the
// window, and the app does not ask AppKit to end it when the window is gone from the screen. Closing the window
// quits, once.
func TestTrayClickTogglesAndNeverQuits(t *testing.T) {
	if macOptions().ApplicationShouldTerminateAfterLastWindowClosed {
		t.Fatal("hiding the main window from the tray would end the app")
	}
	w := &fakeShownWindow{visible: true}
	open := func() { w.calls, w.visible = append(w.calls, "open"), true }
	trayClick(w, open)
	trayClick(w, open)
	if strings.Join(w.calls, ",") != "hide,open" || !w.visible {
		t.Fatalf("two clicks: %v", w.calls)
	}
	quits := 0
	closing := quitOnClose(func() { quits++ })
	closing(nil)
	closing(nil)
	if quits != 1 {
		t.Fatalf("closing the window quit %d times", quits)
	}
}

func TestAddrInUse(t *testing.T) {
	inUse := fmt.Errorf("kolotransport: listen: %w", &net.OpError{Op: "listen", Net: "udp4", Err: os.NewSyscallError("bind", syscall.EADDRINUSE)})
	if !addrInUse(inUse) {
		t.Errorf("not seen: %v", inUse)
	}
	for _, err := range []error{nil, errors.New("address already in use"), os.NewSyscallError("bind", syscall.EACCES)} {
		if addrInUse(err) {
			t.Errorf("seen in %v", err)
		}
	}
}
