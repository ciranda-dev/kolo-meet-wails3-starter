package main

import (
	"context"
	"os"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const mainWindowName = "main"

// prepareDataDir creates dir with mode 0700, or checks that an existing one is not open to other users, since it
// holds the identity.
func prepareDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return meetErr("data-dir-open", nil, dir)
	}
	return nil
}

// fromMainWindow reports whether a bound call comes from the main window. Wails puts the calling window in the
// context; a call without one (a test) counts as another window.
func fromMainWindow(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	w, ok := ctx.Value(application.WindowKey).(interface{ Name() string })
	return ok && w.Name() == mainWindowName
}
