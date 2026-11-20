package main

import "fmt"

// TrayState is what the menu bar or tray icon shows: the active icon during a call, the status line of its menu,
// and what the items of the menu offer.
type TrayState struct {
	Active     bool   `json:"active"`
	Status     string `json:"status"`
	InCall     bool   `json:"inCall"`
	HasInvite  bool   `json:"hasInvite"`
	Microphone bool   `json:"microphone"`
	Camera     bool   `json:"camera"`
	// ShowInNotch is the setting, for the checkbox of the menu.
	ShowInNotch bool `json:"showInNotch"`
}

// trayUI is the menu bar or tray icon; the app's comes from Wails, and tests have none.
type trayUI interface {
	set(TrayState)
}

// trayOf maps the call to the tray. It never holds the invite.
func trayOf(c *Call) TrayState {
	st := TrayState{Status: "No call"}
	if c == nil {
		return st
	}
	st.Active = c.State != callEnded
	st.Microphone, st.Camera = c.Microphone.State == "on", c.Camera.State == "on"
	switch c.State {
	case callCreating:
		st.Status = "Starting a call"
	case callJoining, callConnecting:
		st.Status = "Joining a call"
	case callActive:
		st.InCall, st.HasInvite = true, c.Invite != ""
		switch n := len(c.Participants); {
		case n == 1:
			st.Status = "In a call, waiting for others"
		default:
			st.Status = fmt.Sprintf("In a call with %d people", n)
		}
	}
	return st
}

// trayNow returns what the tray icon shows now.
func (s *MeetService) trayNow() TrayState {
	var snap *Call
	if c := s.activeCall(); c != nil {
		v := c.snapshot(true)
		snap = &v
	}
	st := trayOf(snap)
	if snap == nil {
		mic, cam, _ := s.mediaStates()
		st.Microphone, st.Camera = mic.State == "on", cam.State == "on"
	}
	st.ShowInNotch = s.prefs().showInNotch()
	return st
}

// pushTray tells the tray icon what it shows, when that changed.
func (s *MeetService) pushTray() {
	s.trayMu.Lock()
	defer s.trayMu.Unlock()
	st := s.trayNow()
	s.mu.Lock()
	changed := st != s.lastTray
	s.lastTray = st
	s.mu.Unlock()
	if changed && s.tray != nil {
		s.tray.set(st)
	}
}
