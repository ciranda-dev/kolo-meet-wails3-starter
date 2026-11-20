package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

const (
	maxHistory     = 50
	maxHistoryFile = 1 << 20 // bytes; 50 items take a few KiB, so a larger file is not ours
	historyFile    = "history.json"
)

// HistoryItem is one finished call, as the window lists it. It never holds an invite.
type HistoryItem struct {
	ID           string `json:"id"`
	Role         string `json:"role"`   // "host" or "guest"
	Result       string `json:"result"` // "done" or "failed"
	Message      string `json:"message"`
	Participants int    `json:"participants"` // the most people in the call at once, you included
	Seconds      int64  `json:"seconds"`      // time in the call; 0 if it never became active
	At           int64  `json:"at"`           // ms since epoch, when it ended
	// Error is the reason of Message, to translate it; nil when the call ended well.
	Error *MeetError `json:"error"`
}

// history keeps the last maxHistory items, newest first, in a file of the data directory.
type history struct {
	mu    sync.Mutex
	path  string
	items []HistoryItem
}

// openHistory reads the history file of dir. A missing file is an empty history; a file that cannot be parsed is
// replaced by an empty history at the next write, since it holds nothing the app needs.
func openHistory(dir string) (*history, error) {
	h := &history{path: filepath.Join(dir, historyFile)}
	info, err := os.Lstat(h.path)
	if errors.Is(err, fs.ErrNotExist) {
		return h, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxHistoryFile {
		return h, nil
	}
	b, err := os.ReadFile(h.path)
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(b, &h.items) != nil {
		h.items = nil
	}
	if len(h.items) > maxHistory {
		h.items = h.items[:maxHistory]
	}
	return h, nil
}

// add puts it first, drops the oldest past maxHistory and saves.
func (h *history) add(it HistoryItem) ([]HistoryItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.items = append([]HistoryItem{it}, h.items...)
	if len(h.items) > maxHistory {
		h.items = h.items[:maxHistory]
	}
	return h.listLocked(), h.save()
}

func (h *history) clear() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.items = nil
	return h.save()
}

func (h *history) list() []HistoryItem {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.listLocked()
}

func (h *history) listLocked() []HistoryItem {
	return append([]HistoryItem{}, h.items...)
}

func (h *history) save() error {
	b, err := json.Marshal(h.items)
	if err != nil {
		return err
	}
	return writeFileAtomic(h.path, b)
}
