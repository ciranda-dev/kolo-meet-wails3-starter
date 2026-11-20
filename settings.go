package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	settingsFile    = "settings.json"
	maxSettingsFile = 64 << 10
)

// Settings is what the app runs with. The window changes the display name, the devices and the notch setting; it
// shows the entry nodes and the connection relays read only, as the flags or the variables gave them.
type Settings struct {
	// NetworkMode is the network in use: "public" (the default), "own" (-bootstrap or -first) or "local" (-local).
	NetworkMode string `json:"networkMode"`
	// EntryNodes is the entry nodes of an own or local network, "" on the public one; EntryNodesFrom is "public",
	// "flag", "env", "first" or "local".
	EntryNodes     string `json:"entryNodes"`
	EntryNodesFrom string `json:"entryNodesFrom"`
	// ConnectionRelays is the list of kolo-relays used when a direct connection fails, as host:port@key, comma
	// separated; ConnectionRelaysFrom is "flag", "env" or "none".
	ConnectionRelays     string `json:"connectionRelays"`
	ConnectionRelaysFrom string `json:"connectionRelaysFrom"`
	FirstNode            string `json:"firstNode"`
	DataDir              string `json:"dataDir"`
	Listen               string `json:"listen"`
	MaxParticipants      int    `json:"maxParticipants"`
	ShowInNotch          bool   `json:"showInNotch"`
	DisplayName          string `json:"displayName"`
	CameraID             string `json:"cameraId"`     // "" is the system default
	MicrophoneID         string `json:"microphoneId"` // "" is the system default
	SpeakerID            string `json:"speakerId"`    // "" is the system default
}

// savedSettings is the file kept in the data directory.
type savedSettings struct {
	ShowInNotch  *bool  `json:"showInNotch,omitempty"` // nil: on
	DisplayName  string `json:"displayName,omitempty"`
	CameraID     string `json:"cameraId,omitempty"`
	MicrophoneID string `json:"microphoneId,omitempty"`
	SpeakerID    string `json:"speakerId,omitempty"`
}

func (s savedSettings) showInNotch() bool { return s.ShowInNotch == nil || *s.ShowInNotch }

// loadSaved reads the saved settings of dir; a missing, oversized or unreadable file gives the zero value. A saved
// display name is cleaned again, and dropped if nothing valid is left.
func loadSaved(dir string) (savedSettings, error) {
	var s savedSettings
	path := filepath.Join(dir, settingsFile)
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSettingsFile {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	if json.Unmarshal(b, &s) != nil {
		return savedSettings{}, nil
	}
	s.DisplayName, _ = cleanName(s.DisplayName) // "" if nothing valid is left
	return s, nil
}

func saveSettings(dir string, s savedSettings) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, settingsFile), b)
}
