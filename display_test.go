//go:build finished

package main

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// othersRate adds self and n other people to a budget in the given mode and returns the rate a tile draws at.
func othersRate(mode string, n int) int {
	var b tileBudget
	b.setMode(mode)
	b.take(selfTile, 640, 480)
	for i := range n {
		b.take(string(rune('a'+i)), 640, 480)
	}
	_, _, rate := b.take(selfTile, 640, 480)
	return rate
}

// Each mode's rate by the number of other people showing video, 0 to 8: auto is 30 up to tileFewRemote and 15 above,
// 30 and 15 are fixed.
func TestDisplayModeRates(t *testing.T) {
	if tileFewRemote != 4 {
		t.Fatalf("tileFewRemote %d, want 4", tileFewRemote)
	}
	for n := 0; n <= 8; n++ {
		auto := tileRateHigh
		if n > tileFewRemote {
			auto = tileRateLow
		}
		for _, tc := range []struct {
			mode string
			want int
		}{{"", auto}, {displayAuto, auto}, {display30, 30}, {display15, 15}, {"bogus", auto}} {
			if got := othersRate(tc.mode, n); got != tc.want {
				t.Errorf("mode %q, %d other people: %d pictures a second, want %d", tc.mode, n, got, tc.want)
			}
		}
	}
}

// The byte budget of every mode is the one of its rate, shared by the tiles; with 30 it is the 30 budget for any
// number of tiles, so a tile's share shrinks with the tiles and the rate does not.
func TestDisplayModeByteBudget(t *testing.T) {
	for _, mode := range []string{display30, display15} {
		want := map[string]int{display30: tileRateHigh, display15: tileRateLow}[mode]
		var b tileBudget
		b.setMode(mode)
		for n := 1; n <= 9; n++ {
			b.take(string(rune('a'+n)), 640, 480)
			if _, share, rate := b.take("b", 640, 480); rate != want || share != tileByteRate(want)/n {
				t.Errorf("mode %s, %d tiles: share %d at %d, want %d at %d", mode, n, share, rate, tileByteRate(want)/n, want)
			}
		}
	}
	if got := tileByteRate(tileRateHigh); got != 3579138 {
		t.Errorf("byte budget at 30: %d", got)
	}
}

// With -display-fps 30 the guard is off: tiles that cost a whole second a picture still draw at 30, and the same costs
// make auto fall to 15. With 15 the rate is 15 whatever the cost.
func TestDisplayModeGuard(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want int
	}{{displayAuto, tileRateLow}, {display30, tileRateHigh}, {display15, tileRateLow}} {
		var b tileBudget
		b.setMode(tc.mode)
		b.take(selfTile, 640, 480)
		b.take("a", 640, 480)
		for range 2 * tileCostWarm {
			b.noteCost(selfTile, time.Second)
			b.noteCost("a", time.Second)
		}
		if r, _ := b.display("a"); r != tc.want {
			t.Errorf("mode %s with a drawing cost of 1 s a picture: %d, want %d", tc.mode, r, tc.want)
		}
	}
	// Changing the mode forgets a guard that had fallen.
	var b tileBudget
	b.take("a", 640, 480)
	for range 2 * tileCostWarm {
		b.noteCost("a", time.Second)
	}
	if r, _ := b.display("a"); r != tileRateLow {
		t.Fatalf("guard did not fall: %d", r)
	}
	b.setMode(display30)
	b.setMode(displayAuto)
	b.noteCost("a", time.Millisecond)
	if r, _ := b.display("a"); r != tileRateLow {
		t.Errorf("back to auto with the cost still high: %d", r)
	}
}

// The picture size does not depend on the mode: the same sizes for 30, 15 and auto.
func TestDisplayModeKeepsPictureSizes(t *testing.T) {
	for n := 1; n <= 9; n++ {
		var want [2]int
		for i, mode := range []string{displayAuto, display30, display15} {
			var b tileBudget
			b.setMode(mode)
			for j := range n {
				b.take(string(rune('a'+j)), 1280, 720)
			}
			size, _, _ := b.take("a", 1280, 720)
			if i == 0 {
				want = [2]int{size.X, size.Y}
			} else if [2]int{size.X, size.Y} != want {
				t.Errorf("%d tiles, mode %s: %v, auto gives %v", n, mode, size, want)
			}
		}
	}
}

func TestDisplayModeConfig(t *testing.T) {
	load := func(args []string, env map[string]string) Config {
		t.Helper()
		c, err := LoadConfig(args, envOf(env), os.Stderr)
		if err != nil {
			t.Fatalf("%v %v: %v", args, env, err)
		}
		return c
	}
	if c := load([]string{"-display-fps", "30"}, nil); c.DisplayFPS != "30" {
		t.Errorf("flag: %q", c.DisplayFPS)
	}
	if c := load(nil, map[string]string{"KOLO_MEET_DISPLAY_FPS": "15"}); c.DisplayFPS != "15" {
		t.Errorf("variable: %q", c.DisplayFPS)
	}
	if c := load([]string{"-display-fps", "auto"}, map[string]string{"KOLO_MEET_DISPLAY_FPS": "30"}); c.DisplayFPS != "auto" {
		t.Errorf("a flag wins over its variable: %q", c.DisplayFPS)
	}
	if c := load([]string{"-display-fps", "15"}, map[string]string{"KOLO_MEET_DISPLAY_FPS": "nonsense"}); c.DisplayFPS != "15" {
		t.Errorf("a bad variable under a flag: %q", c.DisplayFPS)
	}
	if s, err := (Config{DataDir: t.TempDir()}).resolve(); err != nil || s.DisplayFPS != displayAuto {
		t.Errorf("default: %q %v", s.DisplayFPS, err)
	}
	for _, v := range []string{"auto", "30", "15"} {
		if s, err := (Config{DataDir: t.TempDir(), DisplayFPS: v}).resolve(); err != nil || s.DisplayFPS != v {
			t.Errorf("%s: %q %v", v, s.DisplayFPS, err)
		}
	}
	for _, v := range []string{"60", "0", "24", "AUTO", " 30", "30fps", "-1", "on"} {
		var ce *ConfigError
		_, err := Config{DataDir: t.TempDir(), DisplayFPS: v}.resolve()
		if !errors.As(err, &ce) || ce.Setting != "display-fps" || !errors.Is(err, ErrMalformed) || !errors.Is(err, ErrConfig) {
			t.Errorf("-display-fps %q: %v", v, err)
		}
		if err != nil && !strings.Contains(err.Error(), "-display-fps") {
			t.Errorf("-display-fps %q: the error does not name the flag: %v", v, err)
		}
	}
	// A bad variable is refused at resolve, as for the other text settings.
	c := load(nil, map[string]string{"KOLO_MEET_DISPLAY_FPS": "60"})
	c.DataDir = t.TempDir()
	if _, err := c.resolve(); !errors.Is(err, ErrMalformed) {
		t.Errorf("KOLO_MEET_DISPLAY_FPS=60: %v", err)
	}
}

// The service gives its budget the mode of its setting, and the quality report names it.
func TestDisplayModeReachesTheBudget(t *testing.T) {
	var none *tileBudget
	if got := none.modeName(); got != displayAuto {
		t.Errorf("no budget: %q", got)
	}
	var b tileBudget
	if got := b.modeName(); got != displayAuto {
		t.Errorf("zero budget: %q", got)
	}
	for _, v := range []string{display30, display15, displayAuto} {
		b.setMode(v)
		if got := b.modeName(); got != v {
			t.Errorf("set %s: %q", v, got)
		}
	}
	for _, v := range []string{"", display30, display15} {
		p := startPeer(t, "", Config{First: freeUDP(t), DisplayFPS: v}, newFakeHW(60, 440), nil)
		want := v
		if want == "" {
			want = displayAuto
		}
		if got := p.tiles.modeName(); got != want {
			t.Errorf("-display-fps %q: budget mode %q, want %q", v, got, want)
		}
	}
}
