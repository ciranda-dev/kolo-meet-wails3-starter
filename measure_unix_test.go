//go:build finished && unix

package main

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	kolovideo "github.com/ciranda-dev/kolo-video"
	"github.com/ciranda-dev/kolo-video/vpx"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// cpuTime is the processor time this process used so far.
func cpuTime(t *testing.T) time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		t.Fatal(err)
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

// clip is two seconds of the fake camera's textured picture at w x h, encoded as this app sends it.
func clip(t testing.TB, w, h int) []vpx.Packet {
	enc, err := vpx.NewEncoder(vpx.Config{Codec: vpx.VP8, Width: w, Height: h, FrameRate: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	var out []vpx.Packet
	for i := range 60 {
		f, _ := kolovideo.NewFrame(w, h)
		paintTexture(f, i+1)
		f.Timestamp = time.Duration(i) * time.Second / 30
		pkts, err := enc.Encode(f, i == 0)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, pkts...)
	}
	return out
}

// decodeAt30 decodes pkts in a loop at 30 frames a second until stop, and gives each decoded frame to show.
func decodeAt30(t *testing.T, pkts []vpx.Packet, stop <-chan struct{}, show func(*kolovideo.Frame)) {
	dec, err := vpx.NewDecoder(remoteDecoderConfig())
	if err != nil {
		t.Error(err)
		return
	}
	defer dec.Close()
	tick := time.NewTicker(time.Second / 30)
	defer tick.Stop()
	for k := 0; ; k++ {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		if img, err := dec.Decode(pkts[k%len(pkts)]); err == nil && img != nil && show != nil {
			show(img)
		}
	}
}

func measuring(t *testing.T) {
	if os.Getenv("KOLO_MEET_MEASURE") == "" {
		t.Skip("set KOLO_MEET_MEASURE=1 to measure")
	}
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

// TestMeasureTiles measures what one computer does in a call of 1, 2, 4 and 8 people at the owner's tile size
// (660x620 CSS pixels at ratio 2), with the default video setting: its own camera (fake, textured, 1280x720 at
// 30 fps) halved and VP8-encoded as sendSize says and drawn as the self-view, and each other participant's stream, at
// the size that participant sends, decoded at 30 fps and drawn. It prints pictures a second per tile, bytes and
// pixels a second for the window, and processor use. Run with KOLO_MEET_MEASURE=1, without -race.
func TestMeasureTiles(t *testing.T) {
	measuring(t)
	lim := vpx.Capabilities()
	const run = 5 * time.Second
	for _, n := range []int{1, 2, 4, 8} {
		sw, sh := sendSize(defaultVideo.w, defaultVideo.h, defaultVideo, lim, n-1)
		pkts := clip(t, sw, sh)
		s := &MeetService{}
		s.tiles.setRule(tileRuleFor(defaultVideo))
		s.SetTileSize(1320, 1240)
		var mu sync.Mutex
		events := map[string]int{}
		var bytes, pixels, sent atomic.Int64
		count := func(ev FrameEvent) {
			mu.Lock()
			events[ev.ID]++
			mu.Unlock()
			bytes.Add(int64(frameEventBytes(len(ev.JPEG))))
			pixels.Add(int64(ev.Width * ev.Height))
		}
		hw := newFakeHW(0, 440)
		hw.texture = true
		l := newLocalMedia(hw, defaultVideo, count, nil, nil)
		l.tile.b = &s.tiles
		enc, err := vpx.NewEncoder(vpx.Config{Codec: vpx.VP8, Width: sw, Height: sh, FrameRate: 30})
		if err != nil {
			t.Fatal(err)
		}
		var half publisher
		l.setSink(&sinks{video: func(f *kolovideo.Frame, _ time.Time) {
			if pk, err := enc.Encode(half.scaled(f, sw, sh), false); err == nil {
				for _, p := range pk {
					sent.Add(int64(len(p.Data)))
				}
			}
		}})
		cpu0, wall0 := cpuTime(t), time.Now()
		if err := l.openCamera(t.Context(), ""); err != nil {
			t.Fatal(err)
		}
		stop := make(chan struct{})
		var wg sync.WaitGroup
		for i := range n - 1 {
			id := fmt.Sprintf("remote%d", i)
			tile := tileRenderer{b: &s.tiles, id: id}
			wg.Go(func() {
				decodeAt30(t, pkts, stop, func(img *kolovideo.Frame) {
					if ev, ok := tile.render(img, time.Now()); ok {
						ev.ID = id
						count(ev)
					}
				})
			})
		}
		time.Sleep(run)
		close(stop)
		wg.Wait()
		l.closeCamera()
		enc.Close()
		cpu, wall := cpuTime(t)-cpu0, time.Since(wall0)
		mu.Lock()
		var line string
		for id, k := range events {
			line += fmt.Sprintf(" %s %.1f", id, float64(k)/wall.Seconds())
		}
		mu.Unlock()
		r := tileRuleFor(defaultVideo)
		t.Logf("%d people: sends %dx%d at %.0f bit/s; pictures a second per tile:%s; window %.0f bytes/s (budget %d), %.0f pixels/s (budget %d); processor %.2f cores",
			n, sw, sh, float64(sent.Load()*8)/wall.Seconds(), line, float64(bytes.Load())/wall.Seconds(), tileByteRate(tileRateHigh),
			float64(pixels.Load())/wall.Seconds(), r.pixelRate, cpu.Seconds()/wall.Seconds())
	}
}

// TestMeasureCodecs measures one VP8 encoder at 1280x720 and 1920x1080 (frames as fast as it goes, scaled to 30 a
// second) and 7 decoders at 30 fps at once, of 1280x720 and of 640x360 streams. Run with KOLO_MEET_MEASURE=1.
func TestMeasureCodecs(t *testing.T) {
	measuring(t)
	for _, sz := range [][2]int{{1280, 720}, {1920, 1080}} {
		enc, err := vpx.NewEncoder(vpx.Config{Codec: vpx.VP8, Width: sz[0], Height: sz[1], FrameRate: 30})
		if err != nil {
			t.Fatal(err)
		}
		frames := make([]*kolovideo.Frame, 30)
		for i := range frames {
			frames[i], _ = kolovideo.NewFrame(sz[0], sz[1])
			paintTexture(frames[i], i+1)
		}
		const n = 150
		var sent int
		cpu0, wall0 := cpuTime(t), time.Now()
		for i := range n {
			f := frames[i%len(frames)]
			f.Timestamp = time.Duration(i) * time.Second / 30
			pk, err := enc.Encode(f, false)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range pk {
				sent += len(p.Data)
			}
		}
		cpu, wall := cpuTime(t)-cpu0, time.Since(wall0)
		enc.Close()
		secs := float64(n) / 30
		t.Logf("encode %dx%d: %.2f ms a frame, %.2f cores at 30 fps, %.0f bit/s",
			sz[0], sz[1], float64(wall.Microseconds())/n/1000, cpu.Seconds()/secs, float64(sent*8)/secs)
	}
	for _, sz := range [][2]int{{1280, 720}, {640, 360}} {
		pkts := clip(t, sz[0], sz[1])
		stop := make(chan struct{})
		var wg sync.WaitGroup
		cpu0, wall0 := cpuTime(t), time.Now()
		for range 7 {
			wg.Go(func() { decodeAt30(t, pkts, stop, nil) })
		}
		time.Sleep(5 * time.Second)
		close(stop)
		wg.Wait()
		cpu, wall := cpuTime(t)-cpu0, time.Since(wall0)
		t.Logf("7 decoders of %dx%d at 30 fps: %.2f cores", sz[0], sz[1], cpu.Seconds()/wall.Seconds())
	}
}

// BenchmarkTileStream is the processor cost of one real-size stream at the receiver: decode one 1280x720 VP8 frame
// and, every other frame, draw and compress the tile at each box.
func BenchmarkTileStream(b *testing.B) {
	if err := loadCodecs(b.TempDir()); err != nil {
		b.Fatal(err)
	}
	pkts := clip(b, 1280, 720)
	for _, box := range defaultTiles.boxes {
		b.Run(fmt.Sprintf("%dx%d", box.X, box.Y), func(b *testing.B) {
			dec, err := vpx.NewDecoder(remoteDecoderConfig())
			if err != nil {
				b.Fatal(err)
			}
			defer dec.Close()
			var budget tileBudget
			budget.setView(box.X, box.Y)
			tile := tileRenderer{b: &budget, id: "x"}
			now := time.Unix(0, 0)
			k := 0
			for b.Loop() {
				img, err := dec.Decode(pkts[k%len(pkts)])
				k++
				if err != nil || img == nil {
					continue
				}
				now = now.Add(time.Second / 30)
				tile.render(img, now)
			}
		})
	}
}

// TestMeasureDisplay measures one tile at 1280x720 and 1920x1080 fed at 15 and at 30 pictures a second: the
// processor time to draw and compress a picture, the JPEG and event sizes, and the time the Go side of the Wails
// event bridge takes to turn the event into JSON (CustomEvent.ToJSON, base64 of the JPEG included). The webview's
// side of the bridge (fetch, decode, draw) is not in a test. The pictures are the fake camera's textured one,
// which makes large JPEGs, and a smooth gradient, which makes small ones; a face is between them. Run with
// KOLO_MEET_MEASURE=1, without -race.
func TestMeasureDisplay(t *testing.T) {
	measuring(t)
	const run = 5 * time.Second
	for _, sz := range [][2]int{{1280, 720}, {1920, 1080}} {
		for _, content := range []string{"textured", "smooth"} {
			frames := make([]*kolovideo.Frame, 30)
			for i := range frames {
				frames[i], _ = kolovideo.NewFrame(sz[0], sz[1])
				if content == "textured" {
					paintTexture(frames[i], i+1)
					continue
				}
				for y := range sz[1] {
					for x := range sz[0] {
						frames[i].Y[y*frames[i].YStride+x] = byte(40 + (x+3*i)*160/sz[0] + y*40/sz[1])
					}
				}
				for _, p := range [][]byte{frames[i].U, frames[i].V} {
					for j := range p {
						p[j] = 128
					}
				}
			}
			for _, rate := range []int{tileRateLow, tileRateHigh} {
				var b tileBudget
				b.setRule(tileRuleFor(videoMode{w: sz[0], h: sz[1], fps: 30}))
				b.setView(sz[0], sz[1])
				tr := tileRenderer{b: &b, id: "x"}
				var pics, jpegBytes, evBytes int
				var bridge time.Duration
				cpu0, wall0 := cpuTime(t), time.Now()
				tick := time.NewTicker(time.Second / time.Duration(rate))
				for k := 0; time.Since(wall0) < run; k++ {
					<-tick.C
					ev, ok := tr.render(frames[k%len(frames)], time.Now())
					if !ok {
						continue
					}
					began := time.Now()
					n := len((&application.CustomEvent{Name: "meet:frame", Data: ev}).ToJSON())
					bridge += time.Since(began)
					pics++
					jpegBytes += len(ev.JPEG)
					evBytes += n
				}
				tick.Stop()
				cpu, wall := cpuTime(t)-cpu0, time.Since(wall0)
				_, cost := b.display("x")
				t.Logf("%dx%d %s at %d fps: %.1f pictures/s, %.0f KB JPEG, %.0f KB event JSON, %.0f KB/s on the bridge (budget %.0f KB/s), draw+compress %.2f ms, event JSON %.2f ms, process %.1f%% of a core",
					sz[0], sz[1], content, rate, float64(pics)/wall.Seconds(), float64(jpegBytes)/float64(max(pics, 1))/1000, float64(evBytes)/float64(max(pics, 1))/1000,
					float64(evBytes)/wall.Seconds()/1000, float64(tileByteRate(tileRateHigh))/1000, float64(cost.Microseconds())/1000,
					float64(bridge.Microseconds())/1000/float64(max(pics, 1)), 100*cpu.Seconds()/wall.Seconds())
			}
		}
	}
}

// TestMeasureDisplayTiles measures the processor a window uses to draw k tiles at 30 pictures a second with
// -display-fps 30 (no guard), at the picture size the policy gives for a call of that many people: 720p cameras, one
// tile for each other person, with and without our own tile. It also shows the pictures a second each tile really
// sent, which the byte budget can hold under 30, and the bytes a second of all tiles against that budget. Run with
// KOLO_MEET_MEASURE=1, without -race.
func TestMeasureDisplayTiles(t *testing.T) {
	measuring(t)
	const run = 5 * time.Second
	const w, h = 1280, 720
	for _, content := range []string{"textured", "smooth"} {
		frames := make([]*kolovideo.Frame, 30)
		for i := range frames {
			frames[i], _ = kolovideo.NewFrame(w, h)
			if content == "textured" {
				paintTexture(frames[i], i+1)
				continue
			}
			for y := range h {
				for x := range w {
					frames[i].Y[y*frames[i].YStride+x] = byte(40 + (x+3*i)*160/w + y*40/h)
				}
			}
			for _, p := range [][]byte{frames[i].U, frames[i].V} {
				for j := range p {
					p[j] = 128
				}
			}
		}
		for _, people := range []int{2, 4, 6} {
			for _, withSelf := range []bool{false, true} {
				var b tileBudget
				b.setMode(display30)
				b.setRule(tileRuleFor(videoMode{w: w, h: h, fps: 30}))
				b.setView(maxFrameWidth, maxFrameHeight)
				ids := []string{selfTile}
				for i := 1; i < people; i++ {
					ids = append(ids, fmt.Sprintf("p%d", i))
				}
				for _, id := range ids {
					b.take(id, w, h)
				}
				drawn := ids[1:]
				if withSelf {
					drawn = ids
				}
				size, _, _ := b.take(ids[1], w, h)
				var pics, evBytes atomic.Int64
				var wg sync.WaitGroup
				cpu0, wall0 := cpuTime(t), time.Now()
				for n, id := range drawn {
					wg.Add(1)
					go func() {
						defer wg.Done()
						tr := tileRenderer{b: &b, id: id}
						tick := time.NewTicker(time.Second / tileRateHigh)
						defer tick.Stop()
						for k := n; time.Since(wall0) < run; k++ {
							<-tick.C
							if ev, ok := tr.render(frames[k%len(frames)], time.Now()); ok {
								pics.Add(1)
								evBytes.Add(int64(frameEventBytes(len(ev.JPEG))))
							}
						}
					}()
				}
				wg.Wait()
				cpu, wall := cpuTime(t)-cpu0, time.Since(wall0)
				_, cost := b.display(ids[1])
				t.Logf("%s, %d people, %d tiles (self drawn: %v): %dx%d, %.1f pictures/s each, %.1f KB event, %.0f KB/s of %.0f KB/s budget, draw+compress %.2f ms, process %.1f%% of a core",
					content, people, len(drawn), withSelf, size.X, size.Y, float64(pics.Load())/wall.Seconds()/float64(len(drawn)),
					float64(evBytes.Load())/float64(max(pics.Load(), 1))/1000, float64(evBytes.Load())/wall.Seconds()/1000, float64(tileByteRate(tileRateHigh))/1000,
					float64(cost.Microseconds())/1000, 100*cpu.Seconds()/wall.Seconds())
			}
		}
	}
}
