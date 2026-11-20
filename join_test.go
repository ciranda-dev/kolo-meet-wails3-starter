package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	koloid "github.com/ciranda-dev/kolo-id"
	invite "github.com/ciranda-dev/kolo-invite"
	kolostore "github.com/ciranda-dev/kolo-store"
)

func TestParseInviteTextBoundsThePastedText(t *testing.T) {
	if _, err := parseInviteText(strings.Repeat("a", maxCodeText+1)); !errors.Is(err, invite.ErrInvalidCode) {
		t.Errorf("text over %d bytes: %v", maxCodeText, err)
	}
	if _, err := parseInviteText(strings.Repeat(" \n", 1<<20)); !errors.Is(err, invite.ErrInvalidCode) {
		t.Errorf("a megabyte of spaces: %v", err)
	}
	s := NewMeetService(Config{}, nil)
	_, err := s.JoinCall(strings.Repeat("a", 1<<20))
	var me *MeetError
	if !errors.As(err, &me) || me.Reason != "invalid-invite" {
		t.Errorf("JoinCall of a huge text: %v", err)
	}
}

func TestParseInviteTextIgnoresSpacesAndLineBreaks(t *testing.T) {
	id, err := koloid.Generate()
	if err != nil {
		t.Fatal(err)
	}
	db := kolostore.NewMemory()
	defer db.Close()
	inv, err := invite.Open(db, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer inv.Close()
	text, _, err := inv.Create(&invite.CreateOptions{TTL: time.Hour, MaxUses: 1, Manual: true})
	if err != nil {
		t.Fatal(err)
	}
	want, err := parseInviteText(text)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseInviteText(" " + text[:10] + " \n\t" + text[10:20] + "\n" + text[20:] + "\n")
	if err != nil || got.ID() != want.ID() {
		t.Errorf("a code with spaces and line breaks: %v, %v", got, err)
	}
}

func TestClearInviteCopy(t *testing.T) {
	for _, tc := range []struct {
		name      string
		clipboard string // what the clipboard holds when the call ends
		readable  bool
		want      string
	}{
		{"still the invite", "invite-code", true, ""},
		{"changed by the person", "my shopping list", true, "my shopping list"},
		{"cannot be read", "invite-code", false, "invite-code"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clip := ""
			s := NewMeetService(Config{}, nil)
			s.copyText = func(text string) { clip = text }
			if tc.readable {
				s.readText = func() (string, bool) { return clip, true }
			}
			s.call = &call{inviteStr: "invite-code"}
			s.CopyInvite()
			if clip != "invite-code" {
				t.Fatalf("CopyInvite put %q on the clipboard", clip)
			}
			clip = tc.clipboard
			s.clearInviteCopy()
			if clip != tc.want {
				t.Errorf("clipboard %q, want %q", clip, tc.want)
			}
			s.clearInviteCopy() // nothing is remembered: a second call does nothing
		})
	}
}
