package main

import (
	"net"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// macOptions keeps the app running while its windows are hidden. With ApplicationShouldTerminateAfterLastWindowClosed,
// AppKit ended the app when a click on the tray icon hid the main window, the only one on screen. Closing the main
// window still quits, through quitOnClose.
func macOptions() application.MacOptions { return application.MacOptions{} }

// shownWindow is the main window, as much of it as a click on the tray icon needs.
type shownWindow interface {
	IsVisible() bool
	Hide() application.Window
}

// trayClick hides the main window when it is on screen, and opens the app otherwise. Hide only takes the window
// off the screen: it does not close it, so the hook that quits never runs.
func trayClick(w shownWindow, openApp func()) {
	if w.IsVisible() {
		w.Hide()
		return
	}
	openApp()
}

// quitOnClose is the main window's WindowClosing hook: the notch window and the tray outlive the main window, so
// closing it quits the app, once.
func quitOnClose(quit func()) func(*application.WindowEvent) {
	var once sync.Once
	return func(*application.WindowEvent) { once.Do(quit) }
}

// trayUsable reports whether goos can show a tray icon. On Linux the tray is a D-Bus service, and Wails crashes
// when it changes the icon in a session without a session bus, so the app runs without a tray there.
func trayUsable(goos string, getenv func(string) string, dial func(path string) error) bool {
	if goos != "linux" {
		return true
	}
	for _, path := range sessionBusSockets(getenv) {
		if dial(path) == nil {
			return true
		}
	}
	return false
}

// sessionBusSockets lists where the D-Bus session bus may listen: the unix: addresses of
// DBUS_SESSION_BUS_ADDRESS (path=, or abstract=, written with a leading "@"), else the "bus" socket of
// XDG_RUNTIME_DIR.
func sessionBusSockets(getenv func(string) string) []string {
	addrs := getenv("DBUS_SESSION_BUS_ADDRESS")
	if addrs == "" {
		if dir := getenv("XDG_RUNTIME_DIR"); dir != "" {
			return []string{dir + "/bus"}
		}
		return nil
	}
	var out []string
	for _, addr := range strings.Split(addrs, ";") {
		kind, params, ok := strings.Cut(addr, ":")
		if !ok || kind != "unix" {
			continue
		}
		for _, kv := range strings.Split(params, ",") {
			switch k, v, _ := strings.Cut(kv, "="); k {
			case "path":
				out = append(out, v)
			case "abstract":
				out = append(out, "@"+v)
			}
		}
	}
	return out
}

func dialUnix(path string) error {
	c, err := net.DialTimeout("unix", path, time.Second)
	if err == nil {
		c.Close()
	}
	return err
}
