//go:build finished

package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"image"
	"image/jpeg"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	kolovideo "github.com/ciranda-dev/kolo-video"
	"github.com/wailsapp/wails/v3/pkg/application"
)

var (
	vgaTiles  = tileRuleFor(videoMode{w: 640, h: 480, fps: 30})
	fullTiles = tileRuleFor(videoMode{w: 1920, h: 1080, fps: 30})
)

// The window gets the picture size that covers its largest tile, from the boxes of the video setting, within its
// pixels a second.
func TestTileSizeChoice(t *testing.T) {
	for _, tc := range []struct {
		name           string
		r              tileRule
		fw, fh, tw, th int
		n              int
		w, h           int
	}{
		{"not told yet", vgaTiles, 640, 480, 0, 0, 1, 160, 120},
		{"small tile", vgaTiles, 640, 480, 300, 200, 1, 320, 240},
		{"exactly the middle box", vgaTiles, 640, 480, 320, 240, 1, 320, 240},
		{"tile a bit wider than the middle box", vgaTiles, 640, 480, 400, 300, 1, 640, 480},
		{"two people, 660x620 CSS px at ratio 2", vgaTiles, 640, 480, 1320, 1240, 2, 640, 480},
		{"one person", vgaTiles, 640, 480, 1320, 1240, 1, 640, 480},
		{"three tiles exceed the pixel budget", vgaTiles, 640, 480, 1320, 1240, 3, 320, 240},
		{"eight tiles", vgaTiles, 640, 480, 1320, 1240, 8, 320, 240},
		{"twenty tiles", vgaTiles, 640, 480, 1320, 1240, 20, 160, 120},
		{"cover: a tall tile shows the picture wider than the tile", vgaTiles, 640, 480, 320, 400, 1, 640, 480},
		{"a 16:9 camera fits the box", vgaTiles, 1280, 720, 1320, 1240, 1, 640, 360},
		{"a small camera is never enlarged", vgaTiles, 320, 240, 1320, 1240, 1, 320, 240},
		{"a tall frame stays inside the box", vgaTiles, 320, 1080, 1320, 1240, 1, 142, 479},
		{"a huge request is the largest box", vgaTiles, 1920, 1080, 1 << 30, 1 << 30, 1, 640, 360},
		{"HD, two people", defaultTiles, 1280, 720, 1320, 1240, 2, 1280, 720},
		{"HD, three people", defaultTiles, 1280, 720, 1320, 1240, 3, 640, 360},
		{"HD, eight people", defaultTiles, 640, 360, 1320, 1240, 8, 640, 360},
		{"HD, nine people", defaultTiles, 640, 360, 1320, 1240, 9, 320, 180},
		{"HD, twenty people", defaultTiles, 320, 180, 1320, 1240, 20, 320, 180},
		{"HD, a 4:3 participant", defaultTiles, 640, 480, 1320, 1240, 2, 640, 480},
		{"1080p, two people", fullTiles, 1920, 1080, 1320, 1240, 2, 1920, 1080},
	} {
		w, h := tileSize(tc.r, tc.fw, tc.fh, tc.tw, tc.th, tc.n)
		if w != tc.w || h != tc.h {
			t.Errorf("%s: %dx%d, want %dx%d", tc.name, w, h, tc.w, tc.h)
		}
	}
}

// Whatever the window asks and whatever the frames are, n tiles never draw more pixels a second than the rule allows
// (for any of the real settings, up to 20 tiles), and their byte shares add up to the byte budget at most.
func TestTileBudgetWorstCase(t *testing.T) {
	for _, r := range []tileRule{defaultTiles, vgaTiles, fullTiles} {
		for n := 1; n <= maxParticipantsCeiling; n++ {
			for _, f := range [][2]int{{640, 480}, {1280, 720}, {1920, 1080}, {maxFrameWidth, 2}, {2, maxFrameHeight}, {320, 240}} {
				for _, want := range [][2]int{{0, 0}, {1, 1}, {320, 240}, {640, 480}, {4000, 4000}, {1 << 30, 1 << 30}} {
					w, h := tileSize(r, f[0], f[1], want[0], want[1], n)
					if px := n * w * h * tileRateLow; px > r.pixelRate {
						t.Errorf("%v: %d tiles of %dx%d for %v: %d pixels a second, bound %d", r.boxes[2], n, w, h, f, px, r.pixelRate)
					}
					if w > f[0] || h > f[1] {
						t.Errorf("%v drawn larger: %dx%d", f, w, h)
					}
				}
			}
		}
	}
	for n := 1; n <= maxParticipantsCeiling; n++ {
		var b tileBudget
		for i := range n {
			b.take(string(rune('a'+i)), 640, 480)
		}
		if _, share, rate := b.take("a", 640, 480); share*n > tileByteRate(rate) {
			t.Errorf("%d tiles: share %d at %d pictures a second", n, share, rate)
		}
	}
	// The default call: 8 tiles of 640x360 at 15 a second is the budget of one 1280x720 camera at 30 fps exactly.
	if got := defaultMaxParticipants * 640 * 360 * tileRateLow; got != defaultTiles.pixelRate || got != 27648000 {
		t.Errorf("8 tiles %d, budget %d", got, defaultTiles.pixelRate)
	}
	if tileByteRateLow != 1789569 || tileByteRate(tileRateLow) != 1789569 {
		t.Errorf("byte budget at %d pictures a second: %d", tileRateLow, tileByteRate(tileRateLow))
	}
}

// The byte budget grows with the rate: twice the pictures a second, twice the bytes, shared by the tiles.
func TestTileByteBudgetFollowsTheRate(t *testing.T) {
	if got, want := tileByteRate(tileRateHigh), 2*tileByteRateLow; got != want {
		t.Errorf("budget at %d pictures a second %d, want %d", tileRateHigh, got, want)
	}
	var b tileBudget
	_, share, rate := b.take("a", 640, 480)
	if rate != tileRateHigh || share != tileByteRate(tileRateHigh) {
		t.Errorf("one tile: %d bytes a second at %d pictures", share, rate)
	}
	b.take("b", 640, 480)
	b.take(selfTile, 640, 480)
	if _, share, rate = b.take("a", 640, 480); rate != tileRateHigh || share != tileByteRate(tileRateHigh)/3 {
		t.Errorf("three tiles: %d bytes a second at %d pictures", share, rate)
	}
	for _, id := range []string{"c", "d"} {
		b.take(id, 640, 480)
	}
	if _, share, rate = b.take("a", 640, 480); rate != tileRateHigh || share != tileByteRate(tileRateHigh)/5 {
		t.Errorf("five tiles, four other people: %d bytes a second at %d pictures", share, rate)
	}
	b.take("e", 640, 480)
	if _, share, rate = b.take("a", 640, 480); rate != tileRateLow || share != tileByteRate(tileRateLow)/6 {
		t.Errorf("six tiles, five other people: %d bytes a second at %d pictures", share, rate)
	}
}

// The display rate follows the number of other people showing video: tileRateHigh for up to tileFewRemote of them,
// tileRateLow above. Our own tile does not count.
func TestTileRatePolicy(t *testing.T) {
	var b tileBudget
	rateOf := func(id string) int {
		_, _, rate := b.take(id, 640, 480)
		return rate
	}
	if r := rateOf(selfTile); r != tileRateHigh {
		t.Errorf("only ourselves: %d", r)
	}
	for i, want := range []int{tileRateHigh, tileRateHigh, tileRateHigh, tileRateHigh, tileRateLow, tileRateLow} {
		if r := rateOf(string(rune('a' + i))); r != want {
			t.Errorf("%d other people: %d pictures a second, want %d", i+1, r, want)
		}
	}
	b.release("f")
	b.release("e")
	if r := rateOf("a"); r != tileRateHigh {
		t.Errorf("back to 4 other people: %d", r)
	}
	b.release(selfTile)
	b.release("a")
	b.release("b")
	if r := rateOf("z"); r != tileRateHigh {
		t.Errorf("one person alone: %d", r)
	}
	var none *tileBudget
	if _, _, r := none.take("x", 640, 480); r != tileRateHigh {
		t.Errorf("no budget: %d", r)
	}
}

// The guard: when drawing the pictures of all tiles at tileRateHigh would take more than tileCPUMax of a core the rate
// is tileRateLow, and it comes back only when that work is under tileCPUResume. Nothing counts before a tile has
// tileCostWarm pictures.
func TestTileCPUGuard(t *testing.T) {
	var b tileBudget
	b.take(selfTile, 640, 480)
	b.take("a", 640, 480)
	rate := func() int { r, _ := b.display("a"); return r }
	feed := func(per time.Duration, n int) {
		for range n {
			b.noteCost(selfTile, per)
			b.noteCost("a", per)
		}
	}
	// Two tiles, each at 30 pictures a second: work is 2 * 30 * cost.
	feed(20*time.Millisecond, tileCostWarm-1)
	if r := rate(); r != tileRateHigh {
		t.Errorf("before the warm-up: %d", r)
	}
	feed(20*time.Millisecond, 1)
	if r := rate(); r != tileRateLow {
		t.Errorf("work 1.2 cores: %d, want %d", r, tileRateLow)
	}
	// Under tileCPUMax and over tileCPUResume it stays where it is.
	feed(7*time.Millisecond, 200) // 2 * 30 * 7 ms = 0.42
	if r := rate(); r != tileRateLow {
		t.Errorf("work 0.42 cores while guarded: %d", r)
	}
	feed(5*time.Millisecond, 200) // 0.30
	if r := rate(); r != tileRateHigh {
		t.Errorf("work 0.30 cores: %d, want %d", r, tileRateHigh)
	}
	feed(7*time.Millisecond, 200) // 0.42: not over tileCPUMax, so no fall
	if r := rate(); r != tileRateHigh {
		t.Errorf("work 0.42 cores while free: %d", r)
	}
	// A cost for a tile that is gone is ignored.
	b.release("a")
	b.noteCost("a", time.Hour)
	var none *tileBudget
	none.noteCost("a", time.Hour)
	if _, c := none.display("a"); c != 0 {
		t.Errorf("no budget: cost %v", c)
	}
}

// The window's size is clamped to what a frame can be, a tile counts from its first picture until it is released,
// and the rule comes from the video setting.
func TestTileBudgetCountsLiveTiles(t *testing.T) {
	s := &MeetService{}
	s.SetTileSize(-5, 1<<40)
	if s.tiles.w != 0 || s.tiles.h != maxFrameHeight {
		t.Errorf("clamped to %dx%d", s.tiles.w, s.tiles.h)
	}
	s.SetTileSize(1320, 1240)
	if size, share, _ := s.tiles.take("self", 1280, 720); share != tileByteRate(tileRateHigh) || size != image.Pt(1280, 720) {
		t.Errorf("one tile: %v, share %d", size, share)
	}
	s.tiles.take("p1", 640, 480)
	s.tiles.take("p2", 640, 480)
	if size, share, _ := s.tiles.take("self", 1280, 720); share != tileByteRate(tileRateHigh)/3 || size != image.Pt(640, 360) {
		t.Errorf("three tiles: %v, share %d", size, share)
	}
	s.tiles.setRule(vgaTiles)
	if size, _, _ := s.tiles.take("self", 640, 480); size != image.Pt(320, 240) {
		t.Errorf("three tiles at 640x480: %v", size)
	}
	s.tiles.release("p1")
	s.tiles.release("p2")
	if size, share, _ := s.tiles.take("self", 640, 480); share != tileByteRate(tileRateHigh) || size != image.Pt(640, 480) {
		t.Errorf("after release: %v, share %d", size, share)
	}
	var none *tileBudget
	if size, share, _ := none.take("x", 640, 480); size != image.Pt(240, 180) || share != tileByteRate(tileRateHigh) {
		t.Errorf("no budget: %v %d", size, share)
	}
	none.release("x")
	none.setView(1, 1)
}

// A renderer reports what each picture cost, and draws at the rate of the guard.
func TestTileRendererFollowsTheGuard(t *testing.T) {
	var b tileBudget
	b.setView(1320, 1240)
	tr := tileRenderer{b: &b, id: "a"}
	flat, _ := kolovideo.NewFrame(640, 480)
	unguarded(t)
	n, _ := runTile(t, &tr, flat, 10*time.Second)
	noTileGuard = false
	if _, cost := b.display("a"); cost <= 0 {
		t.Error("a drawn picture left no cost")
	}
	if n < 10*tileRateHigh-2 {
		t.Errorf("%d pictures in 10 s, want %d", n, 10*tileRateHigh)
	}
	b.mu.Lock()
	b.live["a"].cost, b.live["a"].n = time.Hour, tileCostWarm // far above tileCPUMax, and slow to fall
	b.mu.Unlock()
	tr = tileRenderer{b: &b, id: "a"}
	if n, _ = runTile(t, &tr, flat, 3*time.Second); n < 3*tileRateLow-2 || n > 3*tileRateLow+2 {
		t.Errorf("guarded: %d pictures in 3 s, want %d", n, 3*tileRateLow)
	}
}

// unguarded makes the display rate independent of how long drawing takes on this machine, for a test of the pacing.
func unguarded(t *testing.T) {
	noTileGuard = true
	t.Cleanup(func() { noTileGuard = false })
}

// noisyFrame is a 640x480 frame of random pixels: the largest JPEG a picture of that size makes.
func noisyFrame(t testing.TB, seed uint64) *kolovideo.Frame {
	f, err := kolovideo.NewFrame(640, 480)
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewPCG(seed, 1))
	for _, p := range [][]byte{f.Y, f.U, f.V} {
		for i := range p {
			p[i] = byte(r.Uint32())
		}
	}
	return f
}

// run feeds a renderer frames 33 ms apart (a 30 fps camera) for d of made-up time and returns the events.
func runTile(t testing.TB, tr *tileRenderer, f *kolovideo.Frame, d time.Duration) (events, bytes int) {
	start := time.Unix(1000, 0)
	for at := time.Duration(0); at < d; at += time.Second / 30 {
		if ev, ok := tr.render(f, start.Add(at)); ok {
			events++
			bytes += frameEventBytes(len(ev.JPEG))
		}
	}
	return events, bytes
}

// A tile sends at most tileRateHigh pictures a second, and fewer when its pictures are larger than its byte share
// allows: the bytes it sends stay within the share.
func TestTileRendererPaces(t *testing.T) {
	unguarded(t)
	flat, _ := kolovideo.NewFrame(640, 480)
	var one tileBudget
	one.setView(1320, 1240)
	tr := tileRenderer{b: &one, id: "self"}
	n, _ := runTile(t, &tr, flat, 10*time.Second)
	if n < 10*tileRateHigh-2 || n > 10*tileRateHigh+2 {
		t.Errorf("flat picture: %d pictures in 10 s", n)
	}
	if tr.img.Bounds().Dx() != 640 {
		t.Errorf("one tile drawn at %d", tr.img.Bounds().Dx())
	}
	// The JPEG is the standard library's default quality.
	probe := tileRenderer{}
	ev, ok := probe.render(noisyFrame(t, 3), time.Unix(0, 0))
	var want bytes.Buffer
	if err := jpeg.Encode(&want, probe.img, &jpeg.Options{Quality: jpeg.DefaultQuality}); err != nil || !ok || !bytes.Equal(ev.JPEG, want.Bytes()) {
		t.Errorf("the JPEG is not at quality %d", jpeg.DefaultQuality)
	}

	var eight tileBudget
	eight.setView(1320, 1240)
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		eight.take(id, 640, 480)
	}
	busy := tileRenderer{b: &eight, id: "self"}
	n, sent := runTile(t, &busy, noisyFrame(t, 1), 10*time.Second)
	share := tileByteRate(tileRateLow) / 8
	if sent > share*10+2*share {
		t.Errorf("8 tiles: %d bytes in 10 s, share %d a second", sent, share)
	}
	if n >= 10*tileRateLow || n == 0 {
		t.Errorf("8 tiles of noise: %d pictures in 10 s, want fewer than %d and some", n, 10*tileRateLow)
	}
	if busy.img.Bounds().Dx() != 480 {
		t.Errorf("8 tiles drawn at %d", busy.img.Bounds().Dx())
	}
}

// A camera whose frames come a little early or late still gives tileRateHigh pictures a second, and a tile that drew
// nothing for a while does not catch up with a burst.
func TestTileRendererKeepsItsRate(t *testing.T) {
	unguarded(t)
	f, _ := kolovideo.NewFrame(640, 480)
	tr := tileRenderer{}
	start := time.Unix(1000, 0)
	n := 0
	for i := range 300 {
		jitter := time.Duration(i%3-1) * time.Millisecond
		if _, ok := tr.render(f, start.Add(time.Duration(i)*time.Second/30+jitter)); ok {
			n++
		}
	}
	if n < 10*tileRateHigh-2 || n > 10*tileRateHigh+2 {
		t.Errorf("%d pictures in 10 s of a jittery camera", n)
	}
	later := start.Add(20 * time.Second)
	n = 0
	for i := range 30 {
		if _, ok := tr.render(f, later.Add(time.Duration(i)*time.Second/30)); ok {
			n++
		}
	}
	if n > tileRateHigh+1 {
		t.Errorf("%d pictures in the second after a pause", n)
	}
}

// A picture larger than its tile's share of a second is not sent, so no event exceeds that.
func TestTileRendererDropsOversizedPictures(t *testing.T) {
	var crowd tileBudget
	for i := range 400 {
		crowd.take(strings.Repeat("x", i+1), 640, 480)
	}
	tr := tileRenderer{b: &crowd, id: "self"}
	if n, _ := runTile(t, &tr, noisyFrame(t, 2), 2*time.Second); n != 0 {
		t.Errorf("%d pictures over the share were sent", n)
	}
	if tr.next.IsZero() {
		t.Error("a dropped picture did not wait for the next turn")
	}
}

// frameEventBytes is never less than what Wails sends for a picture of that size.
func TestFrameEventBytesIsAnUpperBound(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 1000, 65537} {
		ev := FrameEvent{ID: strings.Repeat("f", 2*ed25519.PublicKeySize), Seq: math.MaxUint64, Width: 640, Height: 480, JPEG: make([]byte, n)}
		b, err := json.Marshal(&application.CustomEvent{Name: "meet:frame", Data: ev})
		if err != nil {
			t.Fatal(err)
		}
		if got := frameEventBytes(n); got < len(b) || got > len(b)+8 {
			t.Errorf("%d bytes of JPEG: bound %d, event %d", n, got, len(b))
		}
	}
}

// The self-view stops counting when the camera closes, so the others get the room back.
func TestSelfTileReleasedWithTheCamera(t *testing.T) {
	s := &MeetService{}
	frames := make(chan FrameEvent, 64)
	l := newLocalMedia(newFakeHW(9, 1), videoMode{w: 640, h: 480, fps: 30}, func(ev FrameEvent) {
		select {
		case frames <- ev:
		default:
		}
	}, nil, nil)
	l.tile.b = &s.tiles
	s.SetTileSize(1320, 1240)
	if err := l.openCamera(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-frames:
		if ev.ID != "self" || ev.Width != 640 || ev.Height != 480 {
			t.Errorf("self-view %s %dx%d", ev.ID, ev.Width, ev.Height)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no self-view")
	}
	l.closeCamera()
	if n := liveTiles(&s.tiles); n != 0 {
		t.Errorf("%d tiles live after the camera closed", n)
	}
	if err := l.openCamera(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for liveTiles(&s.tiles) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if liveTiles(&s.tiles) == 0 {
		t.Fatal("the self-view does not count")
	}
	hw := l.hw.(*fakeHW)
	hw.mu.Lock()
	hw.cam.lose.Store(true)
	hw.mu.Unlock()
	deadline = time.Now().Add(5 * time.Second)
	for liveTiles(&s.tiles) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := liveTiles(&s.tiles); n != 0 {
		t.Errorf("%d tiles live after the camera was lost", n)
	}
}

func hasTile(b *tileBudget, id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.live[id]
	return ok
}

// A participant's tile counts while its camera sends, and no longer once the camera is off or the call is over.
func TestRemoteTileCountsWhileItsCameraIsOn(t *testing.T) {
	ps := network(t, 2)
	startCall(t, ps)
	a, b := ps[0], ps[1]
	idB := idOf(t, b)
	waitUntil(t, "Bruno's tile counts at Ana", func() bool { return hasTile(&a.tiles, idB) && a.rec.frameCount(idB) > 0 })
	if !hasTile(&a.tiles, "self") {
		t.Error("Ana's self-view does not count")
	}
	if _, err := b.SetCamera(false); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "Bruno's tile released", func() bool { return !hasTile(&a.tiles, idB) })
	if _, err := b.SetCamera(true); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "Bruno's tile counts again", func() bool { return hasTile(&a.tiles, idB) })
	if err := b.LeaveCall(); err != nil {
		t.Fatal(err)
	}
	a.rec.waitCall(t, "alone", func(c Call) bool { return c.State == callActive && len(c.Participants) == 1 })
	waitUntil(t, "Bruno's tile gone", func() bool { return !hasTile(&a.tiles, idB) })
}

func liveTiles(b *tileBudget) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.live)
}
