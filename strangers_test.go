//go:build finished

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"net"
	"testing"
	"time"

	koloid "github.com/ciranda-dev/kolo-id"
	kolosecure "github.com/ciranda-dev/kolo-secure"
)

// openUni opens one-way streams on conn until the peer's limit stops it and returns how many it opened, at most limit.
func openUni(t *testing.T, conn *kolosecure.Conn, limit int) int {
	t.Helper()
	n := 0
	for n < limit {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		_, err := conn.OpenUniStream(ctx)
		cancel()
		if err != nil {
			break
		}
		n++
	}
	return n
}

// A stranger reaches only the invite swarm, with kolo-transport's 16 streams; a person the host let in gets the call
// relay's swarm with the streams a call needs; a key nobody let in is refused there.
func TestStrangersGetFewStreams(t *testing.T) {
	first := freeUDP(t)
	host := startPeer(t, "Ana", Config{First: first}, newFakeHW(60, 440), nil)
	host.rec.waitNetwork(t, netReady)
	if _, err := host.CreateCall(); err != nil {
		t.Fatal(err)
	}
	host.rec.waitCall(t, "had an invite", func(c Call) bool { return c.Invite != "" && c.State == callActive })
	c := host.activeCall()
	if c.isw == nil || c.rsw == nil || bytes.Equal(c.isw.PublicKey(), c.rsw.PublicKey()) {
		t.Fatal("the invite and the relay need two swarms with their own keys")
	}
	if c.isw.Addr().String() == c.rsw.Addr().String() {
		t.Fatal("the two swarms share an address")
	}

	dial := func(sw interface {
		Addr() net.Addr
		PublicKey() ed25519.PublicKey
	}, id *koloid.Identity) *kolosecure.Conn {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, err := kolosecure.Dial(ctx, sw.Addr().String(), id, sw.PublicKey(), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		return conn
	}
	stranger, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if n := openUni(t, dial(c.isw, stranger), 2*maxStreams); n != inviteStreams {
		t.Errorf("a stranger opened %d streams on the invite swarm, want %d", n, inviteStreams)
	}

	member, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	c.admit(member.PublicKey())
	if n := openUni(t, dial(c.rsw, member), 2*maxStreams); n != maxStreams {
		t.Errorf("a person let in opened %d streams on the relay swarm, want %d", n, maxStreams)
	}
}
