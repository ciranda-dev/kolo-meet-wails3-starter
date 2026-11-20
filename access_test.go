//go:build finished

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"testing"
	"time"

	koloaudio "github.com/ciranda-dev/kolo-audio"
)

func indexOf(calls []string, want string) int { return slices.Index(calls, want) }

// The status is read without asking and without opening a device.
func TestMicrophoneAccessNeverOpens(t *testing.T) {
	hw := newFakeHW(100, 500)
	hw.micAccess.Store(1)
	p := startPeer(t, "Eva", Config{First: freeUDP(t)}, hw, nil)
	for want, code := range map[string]int32{micNotDetermined: 1, micDenied: 2, micRestricted: 3, micAuthorized: 0, micUnknown: 4} {
		hw.micAccess.Store(code)
		if got, err := p.MicrophoneAccess(); err != nil || got != want {
			t.Errorf("status %d: %q, %v; want %q", code, got, err, want)
		}
	}
	c := hw.calls()
	if indexOf(c, "request") >= 0 || indexOf(c, "open-mic") >= 0 || hw.opens.Load() != 0 {
		t.Errorf("the status asked or opened: %v", c)
	}
}

// A person never asked is asked first, and the device is opened only after the answer.
func TestMicrophoneAsksBeforeOpening(t *testing.T) {
	hw := newFakeHW(100, 500)
	hw.micAccess.Store(1)
	hw.micAnswer = make(chan string, 1)
	p := startPeer(t, "Eva", Config{First: freeUDP(t)}, hw, nil)
	hw.micAnswer <- "allow"
	mic, err := p.SetMicrophone(true)
	if err != nil || mic.State != "on" {
		t.Fatalf("%+v %v", mic, err)
	}
	c := hw.calls()
	if r, o := indexOf(c, "request"), indexOf(c, "open-mic"); r < 0 || o < 0 || r > o {
		t.Errorf("order %v: the request must come before the open", c)
	}
	// Asked once: the next time the status says authorized and nothing is asked.
	p.SetMicrophone(false)
	p.SetMicrophone(true)
	n := 0
	for _, x := range hw.calls() {
		if x == "request" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("asked %d times", n)
	}
}

// A refusal, by the person or by the system, is typed and no device is opened.
func TestMicrophoneRefusals(t *testing.T) {
	cases := []struct {
		name   string
		access int32
		answer string
		reason string
		asked  bool
	}{
		{"asked and refused", 1, "deny", "microphone-denied", true},
		{"already denied", 2, "", "microphone-denied", false},
		{"restricted", 3, "", "microphone-restricted", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hw := newFakeHW(100, 500)
			hw.micAccess.Store(tc.access)
			hw.micAnswer = make(chan string, 1)
			p := startPeer(t, "Eva", Config{First: freeUDP(t)}, hw, nil)
			if tc.answer != "" {
				hw.micAnswer <- tc.answer
			}
			mic, err := p.SetMicrophone(true)
			var me *MeetError
			if !errors.As(err, &me) || me.Reason != tc.reason || me.Kind != kindPermission || mic.State != "error" || mic.Error.Reason != tc.reason {
				t.Fatalf("%+v %v", mic, err)
			}
			c := hw.calls()
			if indexOf(c, "open-mic") >= 0 || hw.opens.Load() != 0 {
				t.Errorf("the microphone was opened: %v", c)
			}
			if asked := indexOf(c, "request") >= 0; asked != tc.asked {
				t.Errorf("asked %v: %v", asked, c)
			}
			if lob := p.Lobby(); lob.Microphone.State != "error" {
				t.Errorf("lobby %+v", lob.Microphone)
			}
		})
	}
}

// A platform that cannot say (Linux, Windows) asks nothing; the open tells, and a refusal there is typed too.
func TestMicrophoneUnknownPlatform(t *testing.T) {
	hw := newFakeHW(100, 500)
	hw.micAccess.Store(4)
	p := startPeer(t, "Eva", Config{First: freeUDP(t)}, hw, nil)
	if mic, err := p.SetMicrophone(true); err != nil || mic.State != "on" {
		t.Fatalf("%+v %v", mic, err)
	}
	if indexOf(hw.calls(), "request") >= 0 {
		t.Error("asked on a platform with nothing to ask")
	}
	p.SetMicrophone(false)
	hw.denyMic.Store(true) // the system refuses at the open
	mic, err := p.SetMicrophone(true)
	var me *MeetError
	if !errors.As(err, &me) || me.Reason != "microphone-denied" || mic.State != "error" {
		t.Errorf("%+v %v", mic, err)
	}
}

// The prompt waits for the person with no time limit of the app's; cancelling the request, quitting or leaving the
// lobby ends the wait without opening anything.
func TestMicrophonePromptWaitsForThePerson(t *testing.T) {
	hw := newFakeHW(100, 500)
	hw.micAccess.Store(1)
	hw.micAnswer = make(chan string, 1)
	p := startPeer(t, "Eva", Config{First: freeUDP(t)}, hw, nil)
	type result struct {
		status string
		err    error
	}
	done := make(chan result, 1)
	go func() { s, err := p.RequestMicrophone(); done <- result{s, err} }()
	waitUntil(t, "the prompt", func() bool { return indexOf(hw.calls(), "request") >= 0 })
	select {
	case r := <-done:
		t.Fatalf("the request ended by itself: %+v", r)
	case <-time.After(1500 * time.Millisecond):
	}
	p.CancelMicrophoneRequest()
	select {
	case r := <-done:
		var me *MeetError
		if !errors.As(r.err, &me) || me.Reason != "cancelled" || r.status != micNotDetermined {
			t.Fatalf("%+v", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the cancel did not end the wait")
	}
	if hw.opens.Load() != 0 || indexOf(hw.calls(), "open-mic") >= 0 {
		t.Error("a device was opened")
	}
	// Quitting ends a wait too.
	go func() { s, err := p.RequestMicrophone(); done <- result{s, err} }()
	waitUntil(t, "a second prompt", func() bool {
		n := 0
		for _, x := range hw.calls() {
			if x == "request" {
				n++
			}
		}
		return n == 2
	})
	p.ServiceShutdown()
	select {
	case r := <-done:
		var me *MeetError
		if !errors.As(r.err, &me) || me.Reason != "cancelled" {
			t.Fatalf("after quitting: %+v", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("quitting did not end the wait")
	}
}

// An error of kolo-audio that wraps ErrPermission is a refusal, whichever way it arrives.
func TestAudioPermissionErrorsAreTyped(t *testing.T) {
	for _, err := range []error{
		koloaudio.ErrPermission,
		fmt.Errorf("%w (denied): %w", koloaudio.ErrPermission, fs.ErrPermission),
		fmt.Errorf("koloaudio: audio backend failed: %w", fs.ErrPermission),
	} {
		if me := deviceError(devMicrophone, err); me.Reason != "microphone-denied" {
			t.Errorf("%v: %s", err, me.Reason)
		}
	}
}
