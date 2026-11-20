package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"image"
	"math"
	"strings"
	"sync"
	"time"

	kolovideo "github.com/ciranda-dev/kolo-video"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// The display rates, in pictures a second of one tile, and the rule that picks one.
const (
	// tileRateHigh is the rate while at most tileFewRemote other people show video: kolo-video's default capture rate,
	// so every picture sent is shown.
	tileRateHigh = kolovideo.DefaultFrameRate
	// tileRateLow is the rate with more people, and while the guard finds the drawing too costly: half of it.
	tileRateLow   = kolovideo.DefaultFrameRate / 2
	tileFewRemote = 4
	// tileCPUMax is the share of one core the JPEG work of all tiles may take at tileRateHigh before the rate falls to
	// tileRateLow; it goes back up when that work is under tileCPUResume, so a load near the limit does not flip the rate.
	tileCPUMax    = 0.5
	tileCPUResume = 0.4
	// tileCostWeight is the weight of each new picture in a tile's mean cost, and tileCostWarm the pictures a tile
	// needs before its cost counts.
	tileCostWeight = 0.1
	tileCostWarm   = 5

	// tileEarly is how long before its turn a frame is still taken: camera frames are not evenly spaced, and at
	// tileRateHigh a frame that came a millisecond early would cost a whole interval.
	tileEarly = 8 * time.Millisecond

	// tileByteRateLow bounds the bytes a second of picture events over all tiles at tileRateLow. Wails parks an event
	// over 8 KiB until the webview fetches it, at most 64 MiB in all, each for 30 s plus up to a sweep of 7.5 s, and
	// sends what does not fit inline, which keeps memory on macOS (wails v3.0.0 pkg/application/event_payload_store.go:33-57,
	// webview_window.go:1459-1475). At this rate even a webview that fetches nothing never fills the store; the budget
	// grows with the rate (tileByteRate), so at tileRateHigh such a webview fills it in half the time.
	tileByteRateLow = (64 << 20) * 4 / (30 * 5)

	// selfTile is the ID of the tile of our own camera.
	selfTile = "self"
)

// -display-fps values. Auto is the rule above; the other two fix the rate and switch the guard off.
const (
	displayAuto = "auto"
	display30   = "30"
	display15   = "15"
)

// noTileGuard is a test seam: a test sets it to see the display rate without the guard, whose input is the real time
// drawing takes, which the race detector multiplies.
var noTileGuard bool

// tileByteRate is the bytes a second all tiles may send at rate pictures a second.
func tileByteRate(rate int) int { return tileByteRateLow * rate / tileRateLow }

// tileRule is what pictures for the window are drawn within, from the app's video setting: boxes of that size, its
// half and its quarter, smallest first, and pixelRate, the pixels a second over all tiles at tileRateLow: what one
// camera at that size and rate gives. The sizes do not change with the display rate: the guard keeps the work of
// the higher one in bounds.
type tileRule struct {
	boxes     [3]image.Point
	pixelRate int
}

func tileRuleFor(v videoMode) tileRule {
	return tileRule{
		boxes:     [3]image.Point{{max(1, v.w/4), max(1, v.h/4)}, {max(1, v.w/2), max(1, v.h/2)}, {v.w, v.h}},
		pixelRate: int(float64(v.w*v.h) * v.fps),
	}
}

// defaultTiles is the rule of a budget that was not given one: 1280x720, what a window draws at most with one or two
// pictures. A running service always gives its budget the rule of its video setting.
var defaultTiles = tileRuleFor(videoMode{w: 1280, h: 720, fps: kolovideo.DefaultFrameRate})

// frameEventOverhead is the JSON of a picture event as Wails sends it, without the picture, at its longest.
var frameEventOverhead = func() int {
	b, err := json.Marshal(&application.CustomEvent{Name: "meet:frame", Data: FrameEvent{
		ID: strings.Repeat("f", 2*ed25519.PublicKeySize), Seq: math.MaxUint64, Width: maxFrameWidth, Height: maxFrameHeight}})
	if err != nil {
		panic(err)
	}
	return len(b)
}()

// frameEventBytes is the size of the event that carries a JPEG of n bytes, at most.
func frameEventBytes(n int) int { return frameEventOverhead + base64.StdEncoding.EncodedLen(n) }

// tileSize is the size a fw x fh frame is drawn at for a window whose largest picture is tw x th device pixels,
// while n tiles show pictures: the smallest box of r that covers the picture as shown (object-fit: cover), else the
// largest, within r's pixels a second at tileRateLow; never larger than the frame.
func tileSize(r tileRule, fw, fh, tw, th, n int) (w, h int) {
	tw, th = min(max(tw, 0), maxFrameWidth), min(max(th, 0), maxFrameHeight)
	need := max(tw, (th*fw+fh-1)/fh)
	box := r.boxes[0]
	for _, b := range r.boxes {
		if n*b.X*b.Y*tileRateLow > r.pixelRate {
			break
		}
		box = b
		if fitWidth(b, fw, fh) >= need {
			break
		}
	}
	w = fitWidth(box, fw, fh)
	return w, max(1, fh*w/fw)
}

// fitWidth is the width of a fw x fh frame drawn within box b, keeping its shape.
func fitWidth(b image.Point, fw, fh int) int { return max(1, min(b.X, fw, b.Y*fw/fh)) }

// tileBudget shares its rule's pixels and the byte budget among the tiles that show pictures, and says at what rate
// they draw. Its zero value has the default rule; a nil one is a window with one tile whose size is not known.
type tileBudget struct {
	mu      sync.Mutex
	rule    *tileRule             // nil: defaultTiles
	w, h    int                   // device pixels of the largest picture the window shows; 0: not told
	live    map[string]*tileState // tiles that drew a picture and still show video
	guarded bool                  // the drawing took more than tileCPUMax: tiles draw at tileRateLow
	mode    string                // displayAuto, display30 or display15; "" is displayAuto
}

// tileState is what the budget knows of a tile besides its being there.
type tileState struct {
	cost time.Duration // mean time to draw and compress one picture
	n    int           // pictures in cost
}

// take counts tile id as showing pictures and returns the size to draw a fw x fh frame at, the bytes a second the tile
// may send and the pictures a second it may draw.
func (b *tileBudget) take(id string, fw, fh int) (size image.Point, share, rate int) {
	if b == nil {
		w, h := tileSize(defaultTiles, fw, fh, 0, 0, 1)
		return image.Pt(w, h), tileByteRate(tileRateHigh), tileRateHigh
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.live == nil {
		b.live = map[string]*tileState{}
	}
	if b.live[id] == nil {
		b.live[id] = &tileState{}
	}
	n := len(b.live)
	r := defaultTiles
	if b.rule != nil {
		r = *b.rule
	}
	w, h := tileSize(r, fw, fh, b.w, b.h, n)
	rate = b.rateLocked()
	return image.Pt(w, h), tileByteRate(rate) / n, rate
}

// rateLocked is the rate tiles draw at now: the fixed one of -display-fps 30 or 15, else tileRateHigh with at most
// tileFewRemote other people showing video, unless the guard holds it at tileRateLow, else tileRateLow.
func (b *tileBudget) rateLocked() int {
	switch b.mode {
	case display30:
		return tileRateHigh
	case display15:
		return tileRateLow
	}
	remote := len(b.live)
	if _, ok := b.live[selfTile]; ok {
		remote--
	}
	if remote > tileFewRemote {
		return tileRateLow
	}
	if noTileGuard {
		return tileRateHigh
	}
	var work float64
	warm := true
	for _, t := range b.live {
		work += t.cost.Seconds() * tileRateHigh
		warm = warm && t.n >= tileCostWarm
	}
	switch {
	case !warm:
	case b.guarded && work < tileCPUResume:
		b.guarded = false
	case !b.guarded && work > tileCPUMax:
		b.guarded = true
	}
	if b.guarded {
		return tileRateLow
	}
	return tileRateHigh
}

// noteCost adds the time one picture of tile id took to draw and compress to the tile's mean.
func (b *tileBudget) noteCost(id string, took time.Duration) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	t := b.live[id]
	if t == nil {
		return
	}
	if t.n == 0 {
		t.cost = took
	} else {
		t.cost += time.Duration(float64(took-t.cost) * tileCostWeight)
	}
	t.n++
}

// display is the rate tiles draw at now and the mean time to draw one picture of tile id, 0 before it drew any.
func (b *tileBudget) display(id string) (rate int, cost time.Duration) {
	if b == nil {
		return tileRateHigh, 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if t := b.live[id]; t != nil {
		cost = t.cost
	}
	return b.rateLocked(), cost
}

// release stops counting tile id: its camera is off or it left.
func (b *tileBudget) release(id string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	delete(b.live, id)
	b.mu.Unlock()
}

// setMode sets the -display-fps value; anything but display30 and display15 is auto.
func (b *tileBudget) setMode(m string) {
	b.mu.Lock()
	b.mode = m
	b.guarded = false
	b.mu.Unlock()
}

// modeName is the -display-fps value in use, for the quality report.
func (b *tileBudget) modeName() string {
	if b == nil {
		return displayAuto
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.mode == "" {
		return displayAuto
	}
	return b.mode
}

func (b *tileBudget) setRule(r tileRule) {
	b.mu.Lock()
	b.rule = &r
	b.mu.Unlock()
}

func (b *tileBudget) setView(w, h int) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.w, b.h = min(max(w, 0), maxFrameWidth), min(max(h, 0), maxFrameHeight)
	b.mu.Unlock()
}

// SetTileSize tells the size in device pixels (CSS pixels times devicePixelRatio) of the largest picture the window
// shows, so pictures are drawn as large as they are shown, within the app's pixel and byte budgets.
func (s *MeetService) SetTileSize(width, height int) { s.tiles.setView(width, height) }
