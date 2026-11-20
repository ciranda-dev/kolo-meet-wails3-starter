package main

import (
	"time"
)

const (
	notchLinger      = 6 * time.Second // the end of a call stays in the notch this long, then it hides
	notchShortcut    = "CmdOrCtrl+Alt+Shift+M"
	notchWindowWidth = 660 // the library's default content area
	notchWindowH     = 92
)

// NotchState is what the notch window shows, the payload of the "meet:notch" event: the call bar. It never holds
// the invite: HasInvite says that NotchCopyInvite has one to copy.
type NotchState struct {
	Active       bool       `json:"active"`
	State        string     `json:"state"` // the call's state, "" when there is none
	Role         string     `json:"role"`
	Participants int        `json:"participants"`
	Speaker      string     `json:"speaker"` // the name of who is speaking, "" when nobody
	SpeakerIsYou bool       `json:"speakerIsYou"`
	Microphone   bool       `json:"microphone"`
	Camera       bool       `json:"camera"`
	Started      int64      `json:"started"` // ms since epoch, for the timer
	HasInvite    bool       `json:"hasInvite"`
	Header       string     `json:"header"`  // the line about protection, as in Call.Security
	Message      string     `json:"message"` // after the end: why
	Error        *MeetError `json:"error"`
}

// notchWindow is the part of the library's *application.NotchWindow the service uses.
type notchWindow interface {
	Show()
	Hide()
}

// notchOf is the call bar for a call as the window sees it. Active is true while the call runs.
func notchOf(c Call, speaker string, speakerIsYou bool) NotchState {
	n := NotchState{
		Active: c.State != callEnded, State: c.State, Role: c.Role, Participants: len(c.Participants), Speaker: speaker,
		SpeakerIsYou: speakerIsYou, Microphone: c.Microphone.State == "on", Camera: c.Camera.State == "on",
		Started: c.Started, HasInvite: c.Invite != "", Header: c.Security.Header,
	}
	if c.State == callEnded {
		n.HasInvite = false
		n.Error = c.Error
		switch {
		case c.Error != nil:
			n.Message = c.Error.Message
		default:
			n.Message = "You left the call."
		}
	}
	return n
}

// Notch returns what the notch window shows now: nothing while "Show calls in the notch" is off.
func (s *MeetService) Notch() NotchState {
	if !s.prefs().showInNotch() {
		return NotchState{}
	}
	return s.notchNow()
}

func (s *MeetService) notchNow() NotchState {
	if c := s.activeCall(); c != nil {
		snap := c.snapshot(true)
		who, you := c.currentSpeaker()
		return notchOf(snap, who, you)
	}
	s.mu.Lock()
	last, at := s.lastEnded, s.lastEndAt
	s.mu.Unlock()
	if last != nil && time.Since(at) < notchLinger {
		return notchOf(*last, "", false)
	}
	return NotchState{}
}

// NotchCopyInvite puts the invite of the call on the clipboard, here in Go, so the notch page never holds it.
func (s *MeetService) NotchCopyInvite() { s.CopyInvite() }

// NotchLeave leaves the call from the notch.
func (s *MeetService) NotchLeave() { s.LeaveCall() }

// sendNotch sends the notch window what it shows now, while the setting is on. notchMu orders the sends with the
// setting's changes, and the setting is read under it, so no state follows the empty one of "off".
func (s *MeetService) sendNotch() {
	s.notchMu.Lock()
	defer s.notchMu.Unlock()
	on := s.prefs().showInNotch()
	n := s.notchNow()
	if s.notchHook != nil {
		s.notchHook()
	}
	if on && s.emitNotch != nil {
		s.emitNotch("meet:notch", n)
	}
	if on && n.Active && n.State == callActive {
		s.mu.Lock()
		c := s.call
		s.mu.Unlock()
		if c != nil && c.markNotchShown() {
			s.NotchShow()
		}
	}
}

// NotchShow shows the notch window.
func (s *MeetService) NotchShow() {
	if s.notch != nil {
		s.notch.Show()
	}
}

// NotchHide hides the notch window; its page stays loaded.
func (s *MeetService) NotchHide() {
	if s.notch != nil {
		s.notch.Hide()
	}
}

// NotchOpenApp brings the main window to the front.
func (s *MeetService) NotchOpenApp() {
	if s.openApp != nil {
		s.openApp()
	}
}

// SetShowInNotch turns the setting "Show calls in the notch" on or off and saves it.
func (s *MeetService) SetShowInNotch(on bool) (Settings, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if !s.started {
		return Settings{}, explain(ErrNotReady)
	}
	s.notchMu.Lock()
	err := s.saveSaved(func(p *savedSettings) { p.ShowInNotch = &on })
	if err == nil && !on && s.emitNotch != nil {
		s.emitNotch("meet:notch", NotchState{}) // the page forgets what it showed
	}
	s.notchMu.Unlock()
	if err != nil {
		return Settings{}, meetErr("internal", err, "")
	}
	if on {
		s.sendNotch()
	} else {
		s.NotchHide()
	}
	s.pushTray()
	return s.Settings(), nil
}

// notchEnded shows the end of the call for notchLinger, then hides the notch.
func (s *MeetService) notchEnded() {
	s.mu.Lock()
	if s.notchTimer != nil {
		s.notchTimer.Stop()
	}
	s.notchTimer = time.AfterFunc(notchLinger, s.notchExpired)
	s.mu.Unlock()
	s.sendNotch()
}

// notchExpired runs notchLinger after a call ended: the notch forgets it and hides, unless a call started meanwhile.
func (s *MeetService) notchExpired() {
	s.mu.Lock()
	if s.call != nil {
		s.mu.Unlock()
		return
	}
	s.lastEnded, s.notchTimer = nil, nil
	s.mu.Unlock()
	s.sendNotch()
	s.NotchHide()
}

// currentSpeaker is the name of the participant who is speaking now, with yourself first.
func (c *call) currentSpeaker() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.lastSpeakID {
	case "":
		return "", false
	case "self":
		return c.svc.displayName(), true
	}
	if p := c.people[c.lastSpeakID]; p != nil {
		return p.name, false
	}
	return "", false
}

// speakerID finds who speaks in levels: yourself ("self") first, then the others in the order they joined. It
// is a participant's ID, never a name: two people may share one.
func (c *call) speakerID(levels []Level) string {
	for _, l := range levels {
		if l.Speaking {
			return l.ID
		}
	}
	return ""
}

// changedSpeaker records who speaks and tells the notch.
func (c *call) changedSpeaker(id string) {
	c.mu.Lock()
	c.lastSpeakID = id
	c.mu.Unlock()
	c.svc.sendNotch()
}

// markNotchShown reports true the first time it is called for the call: the notch opens once per call by itself.
func (c *call) markNotchShown() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.notchShown {
		return false
	}
	c.notchShown = true
	return true
}
