package main

import (
	"errors"
	"time"

	kolomoq "github.com/ciranda-dev/kolo-moq"
)

// joinAnswerTimeout bounds the wait for the host's answer: the invite library's own timeout for a request.
const joinAnswerTimeout = 30 * time.Second

// runGuest joins a call: it finds the host on the invite's topic, asks to join, takes the grant, and connects to the
// relay of the call.
func (c *call) runGuest() {
	if err := c.joinHost(); err != nil {
		c.end(explainJoin(err))
	}
}

func explainJoin(err error) *MeetError {
	if me, ok := err.(*MeetError); ok && me.Reason == "rejoin-refused" {
		return me
	}
	if me := relayRefusal(err); me.Reason == "call-full" {
		return me
	}
	if c := explain(err); c.Reason != "internal" {
		return c
	}
	return meetErr("relay-unreachable", err, "")
}

// relayRefusal says why a guest could not use the relay: the relay (the host's app) refused the request or closed the
// session, which it does when the call has no place for the guest; anything else is a relay that cannot be reached.
func relayRefusal(err error) *MeetError {
	var se *kolomoq.SessionError
	var re *kolomoq.RequestError
	if errors.As(err, &se) && se.Remote || errors.As(err, &re) {
		return meetErr("call-full", err, "")
	}
	return meetErr("relay-unreachable", err, "")
}

// relayHook is a test seam: when set, a guest reaches the call's relay through the connection it returns for the
// swarm's one, so a test can put an impaired link on the path of the media.
var relayHook func(c *call, conn kolomoq.Conn) kolomoq.Conn

// joinHost finds the host, asks to join and reaches the relay. Step 4 of the guide writes it.
func (c *call) joinHost() error { return notInStarter(4) }
