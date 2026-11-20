//go:build finished

package main

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// noDevices lists nothing and fails, as a machine without a camera, a microphone and a speaker may.
type noDevices struct{ *fakeHW }

func (noDevices) Cameras() ([]Device, error)     { return nil, errors.New("no camera") }
func (noDevices) Microphones() ([]Device, error) { return nil, errors.New("no microphone") }
func (noDevices) Speakers() ([]Device, error)    { return nil, errors.New("no speaker") }

// listFields adds the JSON names of the list fields of t, and of the structs it holds, to names.
func listFields(t reflect.Type, names map[string]bool) {
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return // bytes are a base64 string, not a list
		}
		listFields(t.Elem(), names)
	case reflect.Struct:
		for i := range t.NumField() {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name != "" && name != "-" && (f.Type.Kind() == reflect.Slice && f.Type.Elem().Kind() != reflect.Uint8 || f.Type.Kind() == reflect.Map) {
				names[name] = true
			}
			listFields(f.Type, names)
		}
	}
}

// The window reads every list of a payload as a list: a null in its place stops the window on an error. Every payload
// a window gets is marshaled in its empty, initial and joining states, and no list may be null.
func TestPayloadsHaveNoNullLists(t *testing.T) {
	types := []any{Call{}, Network{}, Info{}, Lobby{}, Devices{}, LevelsEvent{}, ParticipantEvent{}, NotchState{}, Settings{}, HistoryItem{}}
	names := map[string]bool{}
	for _, v := range types {
		listFields(reflect.TypeOf(v), names)
	}
	for _, want := range []string{"participants", "relays", "cameras", "microphones", "speakers", "levels"} {
		if !names[want] {
			t.Fatalf("list field %q not found in the payload types", want)
		}
	}

	fresh := NewMeetService(Config{}, func(string, any) {})
	fresh.hw = noDevices{newFakeHW(1, 1)}
	none, _ := fresh.Devices()
	payloads := map[string]any{
		"Info() before Start":       fresh.Info(),
		"History() before Start":    fresh.History(),
		"Devices() without devices": none,
	}
	p := startPeer(t, "Ana", Config{First: freeUDP(t)}, newFakeHW(1, 1), nil)
	devs, err := p.Devices()
	if err != nil {
		t.Fatal(err)
	}
	payloads["Info()"], payloads["History()"], payloads["Devices()"] = p.Info(), p.History(), devs
	payloads["Lobby()"], payloads["Notch()"], payloads["Settings()"] = p.Lobby(), p.Notch(), p.Settings()
	for _, st := range []string{callCreating, callJoining, callConnecting, callActive, callEnded} {
		role := roleGuest
		if st == callCreating {
			role = roleHost
		}
		c := bareCall(t, role, 8)
		c.state = st
		payloads["meet:call "+st] = c.snapshot(true)
	}

	texts := p.rec.events()
	for what, v := range payloads {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		texts = append(texts, what+" "+string(b))
	}
	for _, text := range texts {
		if strings.HasSuffix(text, " null") {
			t.Errorf("a null list: %.200s", text)
		}
		for name := range names {
			if strings.Contains(text, `"`+name+`":null`) {
				t.Errorf("%q is null in %.200s", name, text)
			}
		}
	}
}

// A window's error reaches the log as one entry, cleaned, without invites, keys or pictures, and bounded.
func TestReportError(t *testing.T) {
	var buf strings.Builder
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	s := NewMeetService(Config{}, func(string, any) {})

	s.ReportError("TypeError: x is null", "    at CallView\x1b[31m\r\n    at App\u202e")
	if got := buf.String(); !strings.Contains(got, "frontend error: TypeError: x is null\n    at CallView[31m\n    at App\n") || strings.ContainsAny(got, "\x1b\r\u202e") {
		t.Errorf("%q", got)
	}

	buf.Reset()
	key := strings.Repeat("ab", 32)
	s.ReportError("bad invite KOLOINV0ABCDEFGH234 for "+key, "at img data:image/jpeg;base64,/9j/4AAQ== end")
	got := buf.String()
	for _, secret := range []string{"ABCDEFGH234", key, "/9j/4AAQ"} {
		if strings.Contains(got, secret) {
			t.Errorf("%q reached the log: %q", secret, got)
		}
	}
	for _, mark := range []string{"bad invite [invite] for [key]", "at img [picture] end"} {
		if !strings.Contains(got, mark) {
			t.Errorf("%q not in %q", mark, got)
		}
	}

	buf.Reset()
	s.ReportError(strings.Repeat("é", maxReportRunes), "at App")
	_, text, _ := strings.Cut(strings.TrimSuffix(buf.String(), "\n"), "frontend error: ")
	if n := utf8.RuneCountInString(text); n != maxReportRunes || strings.Contains(text, "at App") {
		t.Errorf("%d characters logged", n)
	}
}
