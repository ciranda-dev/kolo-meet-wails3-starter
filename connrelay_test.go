//go:build finished

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	koloid "github.com/ciranda-dev/kolo-id"
	invite "github.com/ciranda-dev/kolo-invite"
	kolorelay "github.com/ciranda-dev/kolo-relay"
	kolosecure "github.com/ciranda-dev/kolo-secure"
	kolostore "github.com/ciranda-dev/kolo-store"
	koloswarm "github.com/ciranda-dev/kolo-swarm"
	kolotransport "github.com/ciranda-dev/kolo-transport"
)

// relayEntry is one connection relay in the ParseRelays form, with a fresh key, at addr.
func relayEntry(t *testing.T, addr string) (string, ed25519.PublicKey) {
	t.Helper()
	id, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return addr + "@" + hex.EncodeToString(id.PublicKey()), id.PublicKey()
}

// startConnRelay runs a kolo-relay on 127.0.0.1. Every client of a test is 127.0.0.1, so the listener's per-address
// limit is raised (kolo-relay docs/limits.md, sizing a relay for a swarm).
func startConnRelay(t *testing.T) string {
	t.Helper()
	id, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := kolorelay.Listen(context.Background(), "127.0.0.1:0", id, &kolorelay.ServerConfig{
		Secure: &kolosecure.Config{Transport: &kolotransport.Config{MaxConns: 256, MaxConnsPerIP: 256, MaxDialed: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return srv.Addr().String() + "@" + hex.EncodeToString(id.PublicKey())
}

// downConnRelay is a listed connection relay that reads every packet and never answers.
func downConnRelay(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 2048)
		for {
			if _, _, err := pc.ReadFrom(buf); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		pc.Close()
		<-done
	})
	e, _ := relayEntry(t, pc.LocalAddr().String())
	return e
}

func TestRelaySettingSources(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	a, ka := relayEntry(t, "127.0.0.1:7001")
	b, _ := relayEntry(t, "127.0.0.1:7002")
	c, _ := relayEntry(t, "relay.example.org:7003")
	file := filepath.Join(t.TempDir(), "relays.txt")
	if err := os.WriteFile(file, []byte("# relays\n"+c+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolve := func(args []string, vars map[string]string) (settings, error) {
		t.Helper()
		cfg, err := LoadConfig(args, env(vars), &bytes.Buffer{})
		if err != nil {
			return settings{}, err
		}
		cfg.DataDir = t.TempDir()
		return cfg.resolve()
	}
	s, err := resolve([]string{"-relay", a, "-relay", b + " , " + a, "-relay-file", file}, map[string]string{"KOLO_MEET_RELAYS": b})
	if err != nil || len(s.relays) != 3 || s.relaysFrom != "flag-and-file" || s.relaysText != a+","+b+","+c || !bytes.Equal(s.relays[0].PublicKey, ka) {
		t.Fatalf("flags: %+v %q %v", s.relays, s.relaysText, err)
	}
	if s, err = resolve([]string{"-relay", a}, nil); err != nil || s.relaysFrom != "flag" {
		t.Errorf("a flag alone: %q %v", s.relaysFrom, err)
	}
	if s, err = resolve([]string{"-relay-file", file}, map[string]string{"KOLO_MEET_RELAYS": a}); err != nil || s.relaysText != c || s.relaysFrom != "file" {
		t.Errorf("a file alone keeps the variable out: %q %q %v", s.relaysText, s.relaysFrom, err)
	}
	if s, err = resolve(nil, map[string]string{"KOLO_MEET_RELAYS": b}); err != nil || s.relaysText != b || s.relaysFrom != "env" {
		t.Errorf("variable: %q %q %v", s.relaysText, s.relaysFrom, err)
	}
	if s, err = resolve(nil, nil); err != nil || len(s.relays) != 0 || s.relaysFrom != "none" {
		t.Errorf("none: %+v %q %v", s.relays, s.relaysFrom, err)
	}
	var sixteen []string
	for i := range 17 {
		e, _ := relayEntry(t, fmt.Sprintf("127.0.0.1:%d", 8000+i))
		sixteen = append(sixteen, e)
	}
	if s, err = resolve([]string{"-relay", strings.Join(sixteen, ",")}, nil); err != nil || len(s.relays) != 17 {
		t.Errorf("17 relays: %d %v", len(s.relays), err)
	}
	if s, err = resolve([]string{"-relay-file", file, "-relay", strings.Join(sixteen[:16], ",")}, nil); err != nil || len(s.relays) != 17 {
		t.Errorf("a file over 16 flags: %d %v", len(s.relays), err)
	}
	other, _ := relayEntry(t, "127.0.0.1:7001")
	dir := t.TempDir()
	zero, big := filepath.Join(dir, "zero"), filepath.Join(dir, "big")
	if err := os.Symlink("/dev/zero", zero); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(big, bytes.Repeat([]byte("#"), 64<<10+1), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"one key, two addrs":  {"-relay", a, "-relay", strings.Replace(a, "7001", "7009", 1)},
		"one addr, two keys":  {"-relay", a + "," + other},
		"IPv6":                {"-relay", "[::1]:7001@" + hex.EncodeToString(ka)},
		"IPv4-mapped":         {"-relay", "[::ffff:127.0.0.1]:7001@" + hex.EncodeToString(ka)},
		"port 0":              {"-relay", "127.0.0.1:0@" + hex.EncodeToString(ka)},
		"short key":           {"-relay", "127.0.0.1:7001@abcd"},
		"no key":              {"-relay", "127.0.0.1:7001"},
		"a link to /dev/zero": {"-relay-file", zero},
		"a directory":         {"-relay-file", dir},
		"64 KiB and 1 byte":   {"-relay-file", big},
		"flag and file clash": {"-relay", strings.Replace(c, "relay.example.org", "127.0.0.9", 1), "-relay-file", file},
	} {
		_, err := resolve(args, nil)
		var ce *ConfigError
		if !errors.As(err, &ce) || (ce.Setting != "relay" && ce.Setting != "relay-file") || !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: %v", name, err)
		}
	}
	_, err = resolve([]string{"-relay-file", filepath.Join(dir, "none")}, nil)
	var ce *ConfigError
	if !errors.As(err, &ce) || ce.Setting != "relay-file" || !errors.Is(err, ErrUnreadable) || !errors.Is(err, fs.ErrNotExist) || errors.Is(err, ErrMalformed) {
		t.Errorf("a missing file: %v", err)
	}
	if m := configSetting(ce).Message; !strings.HasPrefix(m, "The setting -relay-file cannot be read: ") {
		t.Errorf("a missing file says %q", m)
	}
	if _, err := resolve(nil, map[string]string{"KOLO_MEET_RELAYS": "nope"}); err == nil || !strings.Contains(err.Error(), "KOLO_MEET_RELAYS") {
		t.Errorf("a bad variable does not say where it came from: %v", err)
	}
}

func TestFindTimeoutWithRelays(t *testing.T) {
	a, _ := relayEntry(t, "127.0.0.1:7001")
	for _, c := range []struct {
		cfg  Config
		want time.Duration
	}{
		{Config{}, defaultFindTimeout},
		{Config{Relays: []string{a}}, 72 * time.Second},
		{Config{Relays: []string{a}, FindTimeout: 9 * time.Second}, 9 * time.Second},
	} {
		c.cfg.DataDir = t.TempDir()
		s, err := c.cfg.resolve()
		if err != nil || s.FindTimeout != c.want {
			t.Errorf("%+v: %v %v", c.cfg, s.FindTimeout, err)
		}
	}
}

func TestGrantCarriesRelays(t *testing.T) {
	relay, host := bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{3}, 32)
	keys := make([]string, 200)
	for i := range keys {
		keys[i] = hex.EncodeToString(bytes.Repeat([]byte{byte(10 + i)}, 32))
	}
	b, err := encodeGrant(strings.Repeat("ab", 16), make([]byte, 32), relay, host, strings.Repeat("<", 48), maxParticipantsCeiling, keys)
	if err != nil || len(b) > maxGrantBytes {
		t.Fatalf("a grant with 200 relays and the longest name: %d bytes, %v", len(b), err)
	}
	r, err := parseGrant(b)
	if err != nil || len(r.relayKeys) != len(keys) || hex.EncodeToString(r.relayKeys[199]) != keys[199] {
		t.Fatalf("parse: %v", err)
	}
	if b, _ = encodeGrant(strings.Repeat("ab", 16), make([]byte, 32), relay, host, "Ana", 8, nil); true {
		if r, err := parseGrant(b); err != nil || len(r.relayKeys) != 0 {
			t.Errorf("no relays: %v", err)
		}
	}
	var g grant
	json.Unmarshal(b, &g)
	for name, list := range map[string][]string{
		"duplicate": {keys[0], keys[1], keys[0]},
		"short":     {"abcd"},
		"uppercase": {strings.ToUpper(keys[0])},
	} {
		g.Relays = list
		b, _ := json.Marshal(g)
		if _, err := parseGrant(b); !errors.Is(err, ErrBadGrant) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRelayMismatchRule(t *testing.T) {
	x := koloswarm.Relay{Addr: "127.0.0.1:1", PublicKey: bytes.Repeat([]byte{7}, 32)}
	y := koloswarm.Relay{Addr: "127.0.0.1:2", PublicKey: bytes.Repeat([]byte{8}, 32)}
	keys := func(rs ...koloswarm.Relay) [][]byte {
		var out [][]byte
		for _, r := range rs {
			out = append(out, r.PublicKey)
		}
		return out
	}
	for _, c := range []struct {
		mine []koloswarm.Relay
		host [][]byte
		warn bool
	}{
		{nil, nil, false},
		{[]koloswarm.Relay{x}, keys(x), false},
		{[]koloswarm.Relay{y, x}, keys(x, y), false},
		{[]koloswarm.Relay{y}, keys(x), true},
		{[]koloswarm.Relay{x}, nil, true},
		{nil, keys(x), true},
	} {
		got := relayMismatch(c.mine, c.host)
		if (got != nil) != c.warn || (got != nil && got.Reason != "connection-relay-none-shared") {
			t.Errorf("mine %d host %d: %v", len(c.mine), len(c.host), got)
		}
		if shareRelay(c.mine, c.host) == c.warn && (len(c.mine) > 0 || len(c.host) > 0) {
			t.Errorf("shareRelay disagrees for mine %d host %d", len(c.mine), len(c.host))
		}
	}
}

func TestRelayWaitReason(t *testing.T) {
	for _, c := range []struct {
		unreachable, shared bool
		want                string
	}{
		{false, false, "relay-unreachable"},
		{false, true, "relay-unreachable"},
		{true, false, "relay-no-common"},
		{true, true, "host-unreachable-relays"},
	} {
		if got := relayWaitReason(c.unreachable, c.shared); got.Reason != c.want || !errors.Is(got, ErrNotFound) {
			t.Errorf("%+v: %v", c, got)
		}
	}
	if got := hostUnreachable(false); got.Reason != "host-unreachable" || !errors.Is(got, ErrNotFound) {
		t.Errorf("without relays: %v", got)
	}
}

// Only the key a joining guest waits for counts: the inviter while joining, the call's relay while connecting.
func TestUnreachableRecordsOnlyTheAwaitedKey(t *testing.T) {
	inv := mustInvite(t)
	c := bareCall(t, roleGuest, 8)
	c.code, c.state = inv.code, callJoining
	c.unreachable(c.room.relay)
	c.unreachable(bytes.Repeat([]byte{9}, 32))
	if c.noInviter || c.noRelay {
		t.Fatal("another key counted while joining")
	}
	c.unreachable(inv.key)
	if !c.noInviter || c.noRelay {
		t.Fatal("the inviter did not count")
	}
	c.noInviter, c.state = false, callConnecting
	c.unreachable(inv.key)
	if c.noInviter || c.noRelay {
		t.Fatal("the inviter counted while connecting")
	}
	c.unreachable(c.room.relay)
	if !c.noRelay {
		t.Fatal("the call's relay did not count")
	}
	h := bareCall(t, roleHost, 8)
	h.state = callConnecting
	h.unreachable(h.room.relay)
	if h.noRelay {
		t.Fatal("a host counted it")
	}
}

func TestRelayStatusesWorstState(t *testing.T) {
	x := koloswarm.Relay{Addr: "127.0.0.1:1", PublicKey: bytes.Repeat([]byte{7}, 32)}
	y := koloswarm.Relay{Addr: "127.0.0.1:2", PublicKey: bytes.Repeat([]byte{8}, 32)}
	s := &MeetService{emit: func(string, any) {}}
	s.set.relays = []koloswarm.Relay{x, y}
	state := func() string {
		var out []string
		for _, r := range s.relayStatuses() {
			reason := ""
			if r.Error != nil {
				reason = ":" + r.Error.Reason
			}
			out = append(out, r.State+reason)
		}
		return strings.Join(out, " ")
	}
	if got := state(); got != "idle idle" {
		t.Fatal(got)
	}
	s.relayChanged(swarmMember, koloswarm.Event{Kind: koloswarm.RelayChanged, PublicKey: x.PublicKey})
	s.relayChanged(swarmMember, koloswarm.Event{Kind: koloswarm.RelayChanged, PublicKey: y.PublicKey, Err: kolorelay.ErrNotAllowed})
	if got := state(); got != "reserved not-reserved:connection-relay-not-allowed" {
		t.Fatal(got)
	}
	s.relayChanged(swarmCallRelay, koloswarm.Event{Kind: koloswarm.RelayChanged, PublicKey: x.PublicKey, Err: kolorelay.ErrLimit})
	s.relayChanged(swarmCallRelay, koloswarm.Event{Kind: koloswarm.RelayChanged, PublicKey: y.PublicKey})
	if got := state(); got != "not-reserved:connection-relay-full not-reserved:connection-relay-not-allowed" {
		t.Fatal(got)
	}
	// The call relay's key is new for each call: an allow list refuses it, and asking to allow it would not help.
	s.relayChanged(swarmMember, koloswarm.Event{Kind: koloswarm.RelayChanged, PublicKey: y.PublicKey})
	s.relayChanged(swarmCallRelay, koloswarm.Event{Kind: koloswarm.RelayChanged, PublicKey: y.PublicKey, Err: kolorelay.ErrNotAllowed})
	if got := state(); got != "not-reserved:connection-relay-full not-reserved:connection-relay-call-denied" {
		t.Fatal(got)
	}
	s.forgetRelays(swarmCallRelay)
	s.relayChanged(swarmMember, koloswarm.Event{Kind: koloswarm.RelayChanged, PublicKey: y.PublicKey, Err: koloswarm.ErrClosed})
	if got := state(); got != "reserved idle" {
		t.Fatal(got)
	}
	for err, want := range map[error]string{
		kolosecure.ErrUnexpectedPeer: "connection-relay-wrong-key", koloswarm.ErrInvalidConfig: "connection-relay-bad-key",
		koloswarm.ErrLost: "connection-relay-unreachable", kolorelay.ErrNotAllowed: "connection-relay-not-allowed",
	} {
		if got := relayError(err, x.Addr, false); got.Reason != want || got.Detail != x.Addr {
			t.Errorf("%v: %v", err, got)
		}
	}
}

// logTo sends the log to a buffer for the rest of the test.
func logTo(t *testing.T) *bytes.Buffer {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

// A place ends with the relay's lifetime and kolo-swarm takes a new one: that is not a failure, so the window and the
// terminal hear nothing. A real failure is logged once, however often it comes back.
func TestRelayPlaceRenewalIsQuiet(t *testing.T) {
	x := koloswarm.Relay{Addr: "127.0.0.1:1", PublicKey: bytes.Repeat([]byte{7}, 32)}
	events := 0
	s := &MeetService{emit: func(name string, _ any) {
		if name == "meet:network" {
			events++
		}
	}}
	s.set.relays = []koloswarm.Relay{x}
	logged := logTo(t)
	held := koloswarm.Event{Kind: koloswarm.RelayChanged, PublicKey: x.PublicKey}
	ended := koloswarm.Event{Kind: koloswarm.RelayChanged, PublicKey: x.PublicKey, Err: kolorelay.ErrReservationEnded}
	for _, ev := range []koloswarm.Event{held, ended, held, ended, held} {
		s.relayChanged(swarmMember, ev)
	}
	if st := s.relayStatuses(); events != 1 || logged.Len() != 0 || st[0].State != relayReserved {
		t.Fatalf("%d events, log %q, %+v", events, logged, st)
	}
	wrong := koloswarm.Event{Kind: koloswarm.RelayChanged, PublicKey: x.PublicKey, Err: kolosecure.ErrUnexpectedPeer}
	for _, ev := range []koloswarm.Event{wrong, wrong, held, wrong} {
		s.relayChanged(swarmMember, ev)
	}
	if n := strings.Count(logged.String(), "connection relay 127.0.0.1:1: "); n != 1 || events != 4 {
		t.Errorf("%d events, %d log lines: %q", events, n, logged)
	}
}

// A relay listed with a wrong key is reported as soon as the app starts, before any call: kolo-swarm would say so
// only for a place, which a guest that joins does not hold, so it would learn it only when the join timed out.
func TestWrongRelayKeyIsReportedAtStart(t *testing.T) {
	addr := strings.Split(startConnRelay(t), "@")[0]
	wrong, _ := relayEntry(t, addr)
	p := startPeer(t, "Bruno", Config{First: freeUDP(t), Relays: []string{wrong}}, newFakeHW(130, 660), nil)
	waitUntil(t, "the wrong key reported", func() bool {
		n := p.Info().Network.Relays
		return len(n) == 1 && n[0].State == relayNotReserved && n[0].Error.Reason == "connection-relay-wrong-key" && n[0].Error.Detail == addr
	})
}

// tapRelay stands where a relay operator stands. Apps list it as their relay: it holds the relay key they check, and
// passes every stream on to a real kolo-relay as the same app, so it keeps every byte an operator can read. It stands
// in only for the identities it is given. With swap set it is hostile instead: anyone else goes on as the tap itself,
// and the first request is turned into a connect to swap.
type tapRelay struct {
	self  *koloid.Identity
	entry string
	to    string
	toKey ed25519.PublicKey
	swap  ed25519.PublicKey
	wg    sync.WaitGroup
	mu    sync.Mutex
	ids   map[string]*koloid.Identity
	seen  []byte
}

func startTapRelay(t *testing.T, swap ed25519.PublicKey) *tapRelay {
	t.Helper()
	to, toKey, _ := strings.Cut(startConnRelay(t), "@")
	self, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ln, err := kolosecure.Listen(ctx, "127.0.0.1:0", self, &kolosecure.Config{Transport: &kolotransport.Config{MaxConns: 256, MaxConnsPerIP: 256}})
	if err != nil {
		t.Fatal(err)
	}
	k, _ := hex.DecodeString(toKey)
	tap := &tapRelay{self: self, to: to, toKey: k, swap: swap, ids: map[string]*koloid.Identity{},
		entry: ln.Addr().String() + "@" + hex.EncodeToString(self.PublicKey())}
	tap.wg.Go(func() {
		for {
			c, err := ln.Accept(ctx)
			if err != nil {
				return
			}
			tap.wg.Go(func() { tap.serve(ctx, c) })
		}
	})
	t.Cleanup(func() {
		cancel()
		ln.Close()
		tap.wg.Wait()
	})
	return tap
}

// standIn lets the tap carry the connections of these identities.
func (tap *tapRelay) standIn(ids ...*koloid.Identity) {
	tap.mu.Lock()
	defer tap.mu.Unlock()
	for _, id := range ids {
		tap.ids[string(id.PublicKey())] = id
	}
}

func (tap *tapRelay) serve(ctx context.Context, c *kolosecure.Conn) {
	defer c.Close()
	tap.mu.Lock()
	id := tap.ids[string(c.RemotePublicKey())]
	tap.mu.Unlock()
	swap := id == nil && tap.swap != nil
	if swap {
		id = tap.self
	}
	if id == nil {
		return
	}
	rc, err := kolosecure.Dial(ctx, tap.to, id, tap.toKey, nil)
	if err != nil {
		return
	}
	defer rc.Close()
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	context.AfterFunc(c.Context(), cancel)
	context.AfterFunc(rc.Context(), cancel)
	var wg sync.WaitGroup
	wg.Go(func() { tap.join(cctx, c, rc, swap) })
	wg.Go(func() { tap.join(cctx, rc, c, false) })
	<-cctx.Done()
	c.Close()
	rc.Close()
	wg.Wait()
}

// join passes every stream that from opens to a new stream of to. With swap, the first frame's key (a connect
// request: a length byte, then the key last) becomes tap.swap.
func (tap *tapRelay) join(ctx context.Context, from, to *kolosecure.Conn, swap bool) {
	for {
		a, err := from.AcceptStream(ctx)
		if err != nil {
			return
		}
		b, err := to.OpenStream(ctx)
		if err != nil {
			a.CancelRead(0)
			a.CancelWrite(0)
			return
		}
		if swap {
			swap = false
			frame := make([]byte, 1, 64)
			if _, err := io.ReadFull(a, frame); err != nil || frame[0] < ed25519.PublicKeySize {
				return
			}
			frame = frame[:1+int(frame[0])]
			if _, err := io.ReadFull(a, frame[1:]); err != nil {
				return
			}
			copy(frame[len(frame)-ed25519.PublicKeySize:], tap.swap)
			if _, err := b.Write(frame); err != nil {
				return
			}
		}
		tap.wg.Go(func() { tap.pipe(b, a) })
		tap.wg.Go(func() { tap.pipe(a, b) })
	}
}

func (tap *tapRelay) pipe(dst, src *kolotransport.Stream) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			tap.mu.Lock()
			tap.seen = append(tap.seen, buf[:n]...)
			tap.mu.Unlock()
			if _, werr := dst.Write(buf[:n]); werr != nil {
				src.CancelRead(0)
				return
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				dst.Close()
			} else {
				dst.CancelWrite(0)
			}
			return
		}
	}
}

func (tap *tapRelay) captured() []byte {
	tap.mu.Lock()
	defer tap.mu.Unlock()
	return bytes.Clone(tap.seen)
}

// A relay with the right key that then connects the check to someone else must not make the app blame the key the
// person typed: the check proves the relay's key and asks for nothing more.
func TestRelayCheckBlamesOnlyTheKey(t *testing.T) {
	decoy, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	tap := startTapRelay(t, decoy.PublicKey())
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res, err := kolorelay.Reserve(ctx, tap.to, tap.toKey, decoy, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close()
	go func() {
		for {
			c, err := res.Accept(ctx)
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	addr, _, _ := strings.Cut(tap.entry, "@")
	r := koloswarm.Relay{Addr: addr, PublicKey: tap.self.PublicKey()}
	me, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	target, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	// The relay really answers oddly: a connect through it ends with another key.
	if c, err := kolorelay.Dial(ctx, r.Addr, r.PublicKey, me, target.PublicKey(), nil); !errors.Is(err, kolosecure.ErrUnexpectedPeer) {
		if c != nil {
			c.Close()
		}
		t.Fatalf("the hostile relay was not hostile: %v", err)
	}
	if err := checkRelay(ctx, r); err != nil {
		t.Errorf("the check blamed a right key: %v", err)
	}
	wrong := koloswarm.Relay{Addr: addr, PublicKey: decoy.PublicKey()}
	if err := checkRelay(ctx, wrong); !errors.Is(err, kolosecure.ErrUnexpectedPeer) {
		t.Errorf("a wrong key passed: %v", err)
	}
}

// Only a wrong key is reported: a check that fails for any other reason, such as the network stopping while it runs
// or a relay that is down, leaves the relay as it was.
func TestRelayCheckReportsOnlyAWrongKey(t *testing.T) {
	x := koloswarm.Relay{Addr: "127.0.0.1:1", PublicKey: bytes.Repeat([]byte{7}, 32)}
	s := &MeetService{emit: func(string, any) {}}
	s.set.relays = []koloswarm.Relay{x}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.checkRelays(ctx)
	s.netWG.Wait()
	if st := s.relayStatuses(); st[0].State != relayIdle {
		t.Errorf("a stopped check reported %+v", st[0])
	}
}

// A relay that never answers holds the key check for up to relayCheckTimeout, but a Reconnect or a stop of the
// network ends it at once: stopNetwork waits for every check, so it returns only when none is left.
func TestRelayCheckEndsWithTheNetwork(t *testing.T) {
	p := startPeer(t, "Bruno", Config{First: freeUDP(t), Relays: []string{downConnRelay(t)}}, newFakeHW(130, 660), nil)
	p.rec.waitNetwork(t, netReady)
	start := time.Now()
	if err := p.Reconnect(); err != nil {
		t.Fatal(err)
	}
	p.stopNetwork()
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("a Reconnect and a stop waited %v for a relay that never answers", d)
	}
}

// What a relay operator reads of two guests who meet through its relay: their keys and how much they send, never the
// call's room or the names of its tracks. The room's ID and each person's key in hex name every key track and media object, so
// they are in the bytes of any key link the relay could read.
func TestRelayOperatorReadsNothingOfTheCall(t *testing.T) {
	tap := startTapRelay(t, nil)
	ps := relayNetwork(t, [3][]string{{tap.entry}, {tap.entry}, {tap.entry}}, false)
	for _, p := range ps {
		tap.standIn(p.id)
	}
	startCall(t, ps)
	other := idOf(t, ps[2])
	ps[1].rec.waitCall(t, "a relayed link", func(c Call) bool {
		x, ok := participantIn(c, other)
		return ok && x.Link == linkRelayed && x.State == partLive
	})
	c := ps[1].activeCall()
	c.mu.Lock()
	room := c.room.id
	c.mu.Unlock()
	seen := tap.captured()
	// What the relay legitimately sees: both guests' keys, so the tap reads the operator's view.
	for _, p := range ps[1:] {
		if !bytes.Contains(seen, p.id.PublicKey()) {
			t.Fatalf("the tap never saw %x", p.id.PublicKey())
		}
	}
	never := map[string][]byte{"the room": []byte(room)}
	for i, p := range ps {
		never[parties[i].name+"'s key in hex"] = []byte(hex.EncodeToString(p.id.PublicKey()))
	}
	for what, b := range never {
		if bytes.Contains(seen, b) {
			t.Errorf("the relay saw %s", what)
		}
	}
}

// relayedConn is a connection through a connection relay, as kolo-relay's Conn says.
type relayedConn struct {
	koloswarm.Conn
	relay ed25519.PublicKey
}

func (c relayedConn) RelayPublicKey() ed25519.PublicKey { return c.relay }

func TestLinkOf(t *testing.T) {
	x := koloswarm.Relay{Addr: "127.0.0.1:1", PublicKey: bytes.Repeat([]byte{7}, 32)}
	s := &MeetService{}
	s.set.relays = []koloswarm.Relay{x}
	if kind, via := s.linkOf(relayedConn{relay: x.PublicKey}); kind != linkRelayed || via != x.Addr {
		t.Errorf("relayed: %s %s", kind, via)
	}
	if kind, via := s.linkOf(&kolosecure.Conn{}); kind != linkDirect || via != "" {
		t.Errorf("direct: %s %s", kind, via)
	}
}

func TestSecurityNamesConnectionRelays(t *testing.T) {
	if d := securityOf(3, 0, "Ana", false, 0).Detail; strings.Contains(d, "connection relay") {
		t.Errorf("without relays: %s", d)
	}
	for _, waiting := range []int{0, 1} {
		if d := securityOf(3, waiting, "Ana", false, 2).Detail; !strings.HasSuffix(d, detailConnectionRelays) {
			t.Errorf("with relays, %d waiting: %s", waiting, d)
		}
	}
}

// walls is a test firewall: each guest's own swarm refuses connections that start at the swarm of the other guest.
type walls struct {
	mu      sync.Mutex
	refused map[*MeetService]map[netip.AddrPort]bool
	keys    map[*MeetService]map[string]bool
}

// dialHook refuses a direct dial to the other guest; a relayed connection (no address) passes.
func (w *walls) dialHook(s *MeetService) func(ed25519.PublicKey, netip.AddrPort) bool {
	return func(key ed25519.PublicKey, a netip.AddrPort) bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		return !a.IsValid() || !w.keys[s][string(key)]
	}
}

func (w *walls) hook(s *MeetService) func(string, netip.AddrPort) bool {
	return func(which string, a netip.AddrPort) bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		return which != swarmMember || !w.refused[s][a]
	}
}

// block makes a and b refuse each other's direct connections.
func (w *walls) block(t *testing.T, a, b *peer) {
	t.Helper()
	addr := func(p *peer) netip.AddrPort {
		sw := p.swarm()
		if sw == nil {
			t.Fatal("no swarm")
		}
		ap, err := netip.ParseAddrPort(sw.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		return ap
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.refused == nil {
		w.refused, w.keys = map[*MeetService]map[netip.AddrPort]bool{}, map[*MeetService]map[string]bool{}
	}
	for _, x := range [][2]*peer{{a, b}, {b, a}} {
		if w.refused[x[0].MeetService] == nil {
			w.refused[x[0].MeetService], w.keys[x[0].MeetService] = map[netip.AddrPort]bool{}, map[string]bool{}
		}
		w.refused[x[0].MeetService][addr(x[1])] = true
		w.keys[x[0].MeetService][string(x[1].id.PublicKey())] = true
	}
}

// relayNetwork starts a host and two guests with the given connection relays; the guests cannot connect to each
// other directly. With atDial, each guest's swarm never dials the other guest directly instead of refusing its
// connections (see TestCallRelaysShareNothing).
func relayNetwork(t *testing.T, relays [3][]string, atDial bool) []*peer {
	t.Helper()
	w := &walls{}
	first := freeUDP(t)
	var ps []*peer
	for i := range 3 {
		cfg := Config{Bootstrap: first, Relays: relays[i]}
		if i == 0 {
			cfg = Config{First: first, Relays: relays[i]}
		}
		setup := func(s *MeetService) { s.acceptHook = w.hook(s) }
		if atDial {
			setup = func(s *MeetService) { s.dialHook = w.dialHook(s) }
		}
		p := startPeer(t, parties[i].name, cfg, newFakeHW(parties[i].luma, parties[i].freq), setup)
		p.rec.waitNetwork(t, netReady)
		ps = append(ps, p)
	}
	w.block(t, ps[1], ps[2])
	t.Cleanup(func() {
		if t.Failed() {
			for i, p := range ps {
				c, _ := p.rec.lastCall()
				t.Logf("%s: %s%s", parties[i].name, describe(c), p.rec.diag())
			}
		}
	})
	return ps
}

// participantIn returns how p sees the person with id.
func participantIn(c Call, id string) (Participant, bool) {
	for _, x := range c.Participants {
		if x.ID == id {
			return x, true
		}
	}
	return Participant{}, false
}

// Two guests who cannot connect directly and list the same connection relay get their key link through it, and
// each plays the other (startCall waits until everyone sees everyone live, which needs the keys).
func TestCallThroughSharedRelay(t *testing.T) {
	r := startConnRelay(t)
	ps := relayNetwork(t, [3][]string{{r}, {r}, {r}}, false)
	if n := ps[1].Info().Network.Relays; len(n) != 1 || n[0].State != relayIdle {
		t.Fatalf("before the call: %+v", n)
	}
	startCall(t, ps)
	addr := strings.Split(r, "@")[0]
	for i, j := range map[int]int{1: 2, 2: 1} {
		other := idOf(t, ps[j])
		c := ps[i].rec.waitCall(t, "a relayed link", func(c Call) bool {
			x, ok := participantIn(c, other)
			return ok && x.Link == linkRelayed && x.ConnectionRelay == addr && x.State == partLive
		})
		if c.Warning != nil {
			t.Errorf("%s warned: %v", parties[i].name, c.Warning)
		}
		host, _ := participantIn(c, idOf(t, ps[0]))
		if host.Link != linkDirect {
			t.Errorf("%s reaches the host %q", parties[i].name, host.Link)
		}
	}
	waitUntil(t, "pictures between the guests", func() bool {
		return ps[1].rec.frameCount(idOf(t, ps[2])) > 3 && ps[2].rec.frameCount(idOf(t, ps[1])) > 3
	})
	for _, p := range ps {
		waitUntil(t, "a place at the connection relay", func() bool {
			n := p.Info().Network.Relays
			return len(n) == 1 && n[0].State == relayReserved
		})
	}
	// Outside a call the app holds no place: the host's two swarms both let theirs go.
	if err := ps[0].LeaveCall(); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "no place at the relay after the call", func() bool {
		n := ps[0].Info().Network.Relays
		return len(n) == 1 && n[0].State == relayIdle
	})
}

// A guest that cannot reach the host's call relay directly joins through a connection relay both list, and the
// call says the relay is reached that way.
func TestJoinThroughRelayWhenCallRelayUnreachable(t *testing.T) {
	r := startConnRelay(t)
	first := freeUDP(t)
	host := startPeer(t, "Ana", Config{First: first, Relays: []string{r}}, newFakeHW(60, 440), func(s *MeetService) {
		s.acceptHook = func(which string, _ netip.AddrPort) bool { return which != swarmCallRelay && which != swarmCallInvite }
	})
	host.rec.waitNetwork(t, netReady)
	guest := startPeer(t, "Bruno", Config{Bootstrap: first, Relays: []string{r}}, newFakeHW(130, 660), nil)
	guest.rec.waitNetwork(t, netReady)
	startCall(t, []*peer{host, guest})
	c, _ := guest.rec.lastCall()
	if c.RelayLink != linkRelayed || c.RelayVia != strings.Split(r, "@")[0] {
		t.Errorf("the call's relay is reached %q via %q", c.RelayLink, c.RelayVia)
	}
	if h, _ := host.rec.lastCall(); h.RelayLink != "" {
		t.Errorf("the host's own relay has a link %q", h.RelayLink)
	}
}

// With two listed connection relays of which the first never answers, the guests meet through the second.
func TestCallThroughSecondRelayWhenFirstDown(t *testing.T) {
	down, r := downConnRelay(t), startConnRelay(t)
	ps := relayNetwork(t, [3][]string{{down, r}, {down, r}, {down, r}}, false)
	startCall(t, ps)
	addr := strings.Split(r, "@")[0]
	other := idOf(t, ps[2])
	ps[1].rec.waitCall(t, "a link through the second relay", func(c Call) bool {
		x, ok := participantIn(c, other)
		return ok && x.Link == linkRelayed && x.ConnectionRelay == addr
	})
}

// A guest whose connection relays share none with the host's is warned; a guest that shares one is not.
func TestCallConnectionRelayMismatchWarns(t *testing.T) {
	x, y := startConnRelay(t), startConnRelay(t)
	first := freeUDP(t)
	var ps []*peer
	for i, relays := range [][]string{{x}, {y}, {y, x}} {
		cfg := Config{Bootstrap: first, Relays: relays}
		if i == 0 {
			cfg = Config{First: first, Relays: relays}
		}
		p := startPeer(t, parties[i].name, cfg, newFakeHW(parties[i].luma, parties[i].freq), nil)
		p.rec.waitNetwork(t, netReady)
		ps = append(ps, p)
	}
	startCall(t, ps)
	if c, _ := ps[1].rec.lastCall(); c.Warning == nil || c.Warning.Reason != "connection-relay-none-shared" {
		t.Errorf("Bruno lists only Y and was not warned: %+v", c.Warning)
	}
	if c, _ := ps[2].rec.lastCall(); c.Warning != nil {
		t.Errorf("Carla lists X too and was warned: %v", c.Warning)
	}
	if c, _ := ps[0].rec.lastCall(); c.Warning != nil {
		t.Errorf("the host was warned: %v", c.Warning)
	}
}

// When the lists share nothing, the pairs that connect directly still have the call; the two guests who cannot stay
// waiting for each other's keys, and both are warned. The guests do not dial each other at all here: refused at the
// listener on 127.0.0.1, every punch they retry holds a kolo-dht punch session for PunchTimeout (45 s), at most 2 per
// host, and on loopback every app is that one host, so a guest's own dial to the call's relay was refused too. The relay attempt of a pair with no shared relay is kolo-swarm's
// TestRelaysShareNone.
func TestCallRelaysShareNothing(t *testing.T) {
	x, y, z := startConnRelay(t), startConnRelay(t), startConnRelay(t)
	ps := relayNetwork(t, [3][]string{{x}, {y}, {z}}, true)
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
	host.rec.waitCall(t, "both guests live", func(c Call) bool {
		live := 0
		for _, p := range c.Participants {
			if !p.Self && p.State == partLive {
				live++
			}
		}
		return live == 2
	})
	for i, j := range map[int]int{1: 2, 2: 1} {
		other, hostID := idOf(t, ps[j]), idOf(t, host)
		got := ps[i].rec.waitCall(t, "the host live and the other guest waiting", func(c Call) bool {
			h, okH := participantIn(c, hostID)
			o, okO := participantIn(c, other)
			return okH && okO && h.State == partLive && o.State == partWaitingKeys
		})
		if got.Warning == nil || got.Warning.Reason != "connection-relay-none-shared" {
			t.Errorf("%s was not warned: %+v", parties[i].name, got.Warning)
		}
		if o, _ := participantIn(got, other); o.Link != "" {
			t.Errorf("%s has a link to the other guest: %q", parties[i].name, o.Link)
		}
	}
}

// A guest that finds the host's app but cannot connect to it, directly or through a connection relay, is told so,
// not that the host was not found.
// With connection relays, the sentence says they failed too instead of asking for one.
func TestJoinHostUnreachable(t *testing.T) {
	t.Run("without relays", func(t *testing.T) { joinHostUnreachable(t, nil, "host-unreachable") })
	t.Run("with relays", func(t *testing.T) {
		joinHostUnreachable(t, []string{startConnRelay(t)}, "host-unreachable-relays")
	})
}

func joinHostUnreachable(t *testing.T, relays []string, want string) {
	first := freeUDP(t)
	host := startPeer(t, "Ana", Config{First: first}, newFakeHW(60, 440), func(s *MeetService) {
		s.acceptHook = func(which string, _ netip.AddrPort) bool { return which != swarmCallRelay && which != swarmCallInvite }
	})
	host.rec.waitNetwork(t, netReady)
	guest := startPeer(t, "Bruno", Config{Bootstrap: first, FindTimeout: 20 * time.Second, Relays: relays}, newFakeHW(130, 660), nil)
	guest.rec.waitNetwork(t, netReady)
	host.turnOn(t)
	if _, err := host.CreateCall(); err != nil {
		t.Fatal(err)
	}
	c := host.rec.waitCall(t, "had an invite", func(c Call) bool { return c.Invite != "" && c.State == callActive })
	if _, err := guest.JoinCall(c.Invite); err != nil {
		t.Fatal(err)
	}
	end := guest.rec.waitCall(t, "the join ended", func(c Call) bool { return c.State == callEnded })
	if end.Error == nil || end.Error.Reason != want {
		t.Fatalf("ended with %+v", end.Error)
	}
}

type testInvite struct {
	code invite.Code
	key  ed25519.PublicKey
}

// mustInvite makes a real invite of a fresh inviter, in memory.
func mustInvite(t *testing.T) testInvite {
	t.Helper()
	id, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	db := kolostore.NewMemory()
	t.Cleanup(func() { db.Close() })
	inv, err := invite.Open(db, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { inv.Close() })
	text, _, err := inv.Create(&invite.CreateOptions{TTL: time.Hour, MaxUses: 1, Manual: true})
	if err != nil {
		t.Fatal(err)
	}
	code, err := invite.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return testInvite{code: code, key: id.PublicKey()}
}

// The relay setting of the app either refuses a text or keeps a list that reads back the same.
func FuzzRelaySetting(f *testing.F) {
	k := strings.Repeat("ab", 32)
	for _, s := range []string{"", "127.0.0.1:7001@" + k, "relay.example.org:9@" + k + ", 127.0.0.1:1@" + strings.Repeat("cd", 32), "[::1]:1@" + k, "x@y", ",,,"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		relays, err := parseRelayList(text)
		if err != nil {
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("%q: %v", text, err)
			}
			return
		}
		again, err := parseRelayList(relaysText(relays))
		if err != nil || len(again) != len(relays) || len(relays) > 16 {
			t.Fatalf("%q read back as %v, %v", text, again, err)
		}
		for i := range relays {
			if again[i].Addr != relays[i].Addr || !bytes.Equal(again[i].PublicKey, relays[i].PublicKey) || len(relays[i].PublicKey) != 32 {
				t.Fatalf("%q: entry %d changed", text, i)
			}
		}
	})
}

// The window hears of every new or lost link at once, since a link can change while the person stays live (a
// relayed pair that turns direct); a lost connection removes only its own link.
func TestLinkChangesAreTold(t *testing.T) {
	c := bareCall(t, roleGuest, 8)
	c.ending = false
	var events []Call
	c.svc.emit = func(name string, data any) {
		if v, ok := data.(Call); ok {
			events = append(events, v)
		}
	}
	key := bytes.Repeat([]byte{5}, 32)
	p := &participant{key: key, id: hex.EncodeToString(key), state: partLive}
	c.people[p.id], c.order = p, []string{p.id}
	linkNow := func() (string, string) {
		x, _ := participantIn(events[len(events)-1], p.id)
		return x.Link, x.ConnectionRelay
	}
	one, two := &fakePeer{key: key}, &fakePeer{key: key}
	stopped := false
	if old := c.setLink(p, &link{peer: one, kind: linkRelayed, via: "127.0.0.1:1", cancel: func() { stopped = true }}, nil); old != nil {
		t.Fatal("an old link")
	}
	if kind, via := linkNow(); len(events) != 1 || kind != linkRelayed || via != "127.0.0.1:1" {
		t.Fatalf("%d events, link %q via %q", len(events), kind, via)
	}
	if old := c.setLink(p, nil, two); old != nil || len(events) != 1 || p.link == nil {
		t.Fatal("the end of another connection removed the link")
	}
	c.peerLost(one)
	if kind, _ := linkNow(); len(events) != 2 || kind != "" || p.link != nil || !stopped {
		t.Fatalf("lost: %d events, link %q, stopped %v", len(events), kind, stopped)
	}
}

func TestNetworkSeqGrows(t *testing.T) {
	var sent []Network
	s := &MeetService{emit: func(name string, v any) {
		if name == "meet:network" {
			sent = append(sent, v.(Network))
		}
	}}
	x := koloswarm.Relay{Addr: "127.0.0.1:1", PublicKey: bytes.Repeat([]byte{7}, 32)}
	s.set.relays = []koloswarm.Relay{x}
	s.setNetwork(netStarting, nil)
	s.setNetwork(netReady, nil)
	s.relayChanged(swarmMember, koloswarm.Event{Kind: koloswarm.RelayChanged, PublicKey: x.PublicKey})
	s.setNetwork(netFailed, meetErr("network-open", errors.New("x"), ""))
	for i, n := range sent {
		if n.Seq != uint64(i+1) {
			t.Fatalf("event %d has seq %d", i, n.Seq)
		}
	}
	if got := s.Info().Network; got.Seq != uint64(len(sent)) || got.State != netFailed {
		t.Errorf("snapshot %+v, want the last event's seq %d", got, len(sent))
	}
}
