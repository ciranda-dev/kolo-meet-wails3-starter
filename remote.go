package main

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/ciranda-dev/kolo-audio/opus"
	kolomedia "github.com/ciranda-dev/kolo-media"
	"github.com/ciranda-dev/kolo-video/vpx"
)

// The largest picture taken from another participant: 1920x1080, the largest size kolo-video's decoder reaches in real
// time on both engines (README, Platforms). This app sends 640x480; nothing legitimate needs more, and a hostile
// sender must not make every viewer decode and draw giant frames.
const (
	maxFrameWidth  = 1920
	maxFrameHeight = 1080
)

func remoteDecoderConfig() vpx.DecoderConfig {
	return vpx.DecoderConfig{Codec: vpx.VP8, MaxPixels: maxFrameWidth * maxFrameHeight}
}

// watchCapabilities is what this app decodes: Opus and VP8 up to the size above.
func watchCapabilities() kolomedia.Capabilities {
	return kolomedia.Capabilities{Codecs: []kolomedia.Codec{kolomedia.Opus, kolomedia.VP8}, MaxWidth: maxFrameWidth, MaxHeight: maxFrameHeight}
}

// staleAudio is how long after its last audio frame a participant counts as silent: the frames come every 20 ms.
const staleAudio = 400 * time.Millisecond

// playWatcher decodes what w delivers until it ends: audio goes to the mixer, video to the window. A frame that does
// not decode is skipped; the decoders resume at the next keyframe.
func (c *call) playWatcher(p *participant, w *kolomedia.Watcher) {
	ctx, cancel := context.WithCancel(p.ctx)
	defer cancel()
	var wg sync.WaitGroup
	ended := make(chan error, 2)
	wg.Go(func() { ended <- c.playAudio(ctx, p, w) })
	wg.Go(func() { ended <- c.playVideo(ctx, p, w) })
	wg.Go(func() { c.pollCatalog(ctx, p, w) })
	wg.Go(func() { c.logMedia(ctx, p, w) })
	select {
	case <-ended:
	case <-ctx.Done():
	}
	cancel()
	w.Close()
	wg.Wait()
}

// playAudio decodes p's audio for the speaker. Step 4 of the guide writes it.
func (c *call) playAudio(ctx context.Context, p *participant, w *kolomedia.Watcher) error {
	return notInStarter(4)
}

// concealGap fills the miss frames missing before the packet next, for up to maxConceal of them in a row: the frames
// after the last one heard are made up by the decoder, and the one just before next from the forward error correction
// in next when it has it. A longer gap stays silent after that.
func (c *call) concealGap(p *participant, dec *opus.Decoder, next []byte, pcm []int16, miss int) {
	one := pcm[:frameSamples*audioChannels]
	k := min(miss, maxConceal)
	for i := range k {
		var n int
		var err error
		if i == k-1 && miss <= maxConceal {
			n, err = dec.DecodeFEC(next, one)
		} else {
			n, err = dec.DecodeLost(one)
		}
		if err != nil || n == 0 {
			return
		}
		p.concealed.Add(1)
		c.mix.push(p.id, one[:n])
	}
}

// playVideo decodes p's video for the window. Step 4 of the guide writes it.
func (c *call) playVideo(ctx context.Context, p *participant, w *kolomedia.Watcher) error {
	return notInStarter(4)
}

// statsEvery is how often a watch writes its counters to the log and the call quality report. A variable so a test
// can shorten it.
var statsEvery = 5 * time.Second

// logMedia writes what the watch of p received in the last statsEvery: the frames decoded a second, and what was lost
// on the way (groups the sender reset or expired, frames the playout dropped), so a stall can be told from a slow link.
func (c *call) logMedia(ctx context.Context, p *participant, w *kolomedia.Watcher) {
	a0, v0, t0 := p.audioIn.Load(), p.videoIn.Load(), time.Now()
	q0 := p.counters(w.Stats())
	for sleep(ctx, statsEvery) {
		now := time.Now()
		a, v := p.audioIn.Load(), p.videoIn.Load()
		secs := now.Sub(t0).Seconds()
		st, ss := w.Stats(), c.session().Stats()
		q0 = c.reportRemote(p, st, q0, secs, now)
		au, vi := st.Playout.Tracks["audio"], st.Playout.Tracks["video"]
		c.logf("media from %.8s: audio %.1f/s video %.1f/s (missed %d, made up %d); playout delay %v, audio %s, video %s; session reset %d expired %d; track errors audio %v video %v",
			p.id, float64(a-a0)/secs, float64(v-v0)/secs, p.missed.Load(), p.concealed.Load(), st.Playout.Delay, trackLine(au, st.Audio), trackLine(vi, st.Video),
			ss.ResetGroups, ss.ExpiredGroups, st.Audio.Err, st.Video.Err)
		a0, v0, t0 = a, v, now
	}
}

// reportRemote writes the quality record of p for the interval since q0 and returns the counters it read.
func (c *call) reportRemote(p *participant, st kolomedia.WatchStats, q0 peerCounters, secs float64, now time.Time) peerCounters {
	q := p.counters(st)
	if !c.svc.qualityOn() {
		return q
	}
	r := remoteRecord(q0, q, secs)
	r.At, r.T, r.Call, r.Person = now.Unix(), round1(now.Sub(c.began).Seconds()), c.id, p.person
	size := p.videoSize.Load()
	r.VideoW, r.VideoH = int(size>>32), int(size&0xffffffff)
	n, longest := p.freeze.take(now)
	r.Freezes, r.FreezeLongestMs = n, longest.Milliseconds()
	r.PlayoutDelayMs, r.PlayoutJitterMs = st.Playout.Delay.Milliseconds(), st.Playout.Jitter.Milliseconds()
	r.DecodeMs = ms(time.Duration(p.decodeNs.Load()))
	rate, cost := c.svc.tiles.display(p.id)
	r.DisplayFPS, r.TileMs = rate, ms(cost)
	c.svc.reportQuality(r)
	return q
}

// counters reads the totals of p's watch for the quality record: the playout's drops come from st.
func (p *participant) counters(st kolomedia.WatchStats) peerCounters {
	dropped := func(t kolomedia.TrackStats) uint64 { return t.Late + t.Skipped + t.Overflow }
	return peerCounters{
		audioIn: p.audioIn.Load(), missed: p.missed.Load(), concealed: p.concealed.Load(), audioDropped: dropped(st.Playout.Tracks["audio"]),
		videoIn: p.videoIn.Load(), shown: p.videoShown.Load(), videoDropped: dropped(st.Playout.Tracks["video"]),
		audioBytes: p.audioBytes.Load(), videoBytes: p.videoBytes.Load(),
	}
}

// noteDecode adds the time a video frame took to decode to the mean the quality record shows. Only the video loop calls it.
func (p *participant) noteDecode(took time.Duration) {
	cur := time.Duration(p.decodeNs.Load())
	if cur == 0 {
		cur = took
	} else {
		cur += time.Duration(float64(took-cur) * tileCostWeight)
	}
	p.decodeNs.Store(int64(cur))
}

// trackLine is the counters of one track: the playout's (frames pushed, played and dropped for each reason, waiting)
// and the subscription's (what the transport dropped, what did not open, groups cut short).
func trackLine(p kolomedia.TrackStats, w kolomedia.TrackWatchStats) string {
	return fmt.Sprintf("{in %d played %d late %d skipped %d ahead %d overflow %d dup %d no-key-frame %d waiting %d | link-dropped %d gaps %d malformed %d no-key %d retired %d unauthenticated %d}",
		p.Received, p.Played, p.Late, p.Skipped, p.Ahead, p.Overflow, p.Duplicate, p.NoKeyframe, p.Buffered, w.Dropped, w.Gaps, w.Malformed, w.NoKey, w.Retired, w.Unauthenticated)
}

// pollCatalog keeps p.audioOn and p.videoOn, which come from the catalog the sender sealed: a track exists while its
// device is on.
func (c *call) pollCatalog(ctx context.Context, p *participant, w *kolomedia.Watcher) {
	for {
		tracks := w.Stats().Playout.Tracks
		p.audioRecv.Store(tracks["audio"].Received)
		p.videoRecv.Store(tracks["video"].Received)
		cat := w.Catalog()
		audio, video := false, false
		if cat != nil {
			for _, t := range cat.Tracks {
				audio = audio || t.Kind == kolomedia.Audio
				video = video || t.Kind == kolomedia.Video
			}
		}
		p.freeze.setSending(video)
		c.mu.Lock()
		changed := p.audioOn != audio || p.videoOn != video
		p.audioOn, p.videoOn = audio, video
		c.mu.Unlock()
		if changed {
			c.changed()
		}
		if !video {
			c.svc.tiles.release(p.id)
		}
		if !sleep(ctx, pollCatalog) {
			return
		}
	}
}

// remoteLevels lists the level of every participant but ourselves.
func (c *call) remoteLevels() []Level {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Level, 0, len(c.people))
	for _, id := range c.order {
		p := c.people[id]
		if p == nil {
			continue
		}
		lv := Level{ID: p.id}
		if time.Since(time.Unix(0, p.lastAudio.Load())) < staleAudio {
			lv.Level, lv.Speaking = meter(math.Float64frombits(p.level.Load())), p.speaking.Load()
		}
		out = append(out, lv)
	}
	return out
}
