package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	kolomoq "github.com/ciranda-dev/kolo-moq"
)

// -quality-report values.
const (
	qualityOn  = "on"
	qualityOff = "off"
)

const (
	// qualityFileName is the call quality report, in the data directory. The file before it is kept as the same name
	// with ".1".
	qualityFileName = "kolo-meet-quality.jsonl"
	// qualityMaxBytes is the size at which the report starts a new file; the old one replaces ".1", so the report takes
	// at most twice this.
	qualityMaxBytes = 2 << 20
	// freezeAfter is how long a person who sends video may go without a picture on the screen before it counts as a
	// freeze.
	freezeAfter = 500 * time.Millisecond
)

// qualityWriter appends the lines of the call quality report to a file of the data directory with mode 0600. It is
// safe for concurrent use; a nil one writes nothing.
type qualityWriter struct {
	mu     sync.Mutex
	path   string
	max    int64
	f      *os.File
	size   int64
	failed bool // a write failed and was logged
	closed bool
}

func newQualityWriter(dir string, max int64) *qualityWriter {
	return &qualityWriter{path: filepath.Join(dir, qualityFileName), max: max}
}

// notice is the line logged once at start while the report is on: where it goes, what it holds and how to stop it.
func (w *qualityWriter) notice() string {
	return "call quality report is on: each call adds counts, sizes and times (no names, keys or media) to " + w.path +
		"; turn it off with -quality-report off or KOLO_MEET_QUALITY=off (docs/security.md)"
}

// write appends line and a newline. When the file would pass the size cap it is renamed to ".1", replacing the one
// there, and the line starts a new file.
func (w *qualityWriter) write(line []byte) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	err := w.writeLocked(line)
	if err != nil && !w.failed {
		w.failed = true
		log.Print("quality report: ", err)
	}
}

func (w *qualityWriter) writeLocked(line []byte) error {
	if w.f == nil {
		f, err := os.OpenFile(w.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
		if err != nil {
			return err
		}
		if runtime.GOOS != "windows" {
			if err := f.Chmod(0o600); err != nil { // a file that was there with a wider mode
				f.Close()
				return err
			}
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return err
		}
		w.f, w.size = f, info.Size()
	}
	if n := int64(len(line)) + 1; w.size > 0 && w.size+n > w.max {
		w.f.Close()
		w.f = nil
		if err := os.Rename(w.path, w.path+".1"); err != nil {
			return err
		}
		return w.writeLocked(line)
	}
	n, err := w.f.Write(append(line[:len(line):len(line)], '\n'))
	w.size += int64(n)
	return err
}

func (w *qualityWriter) close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.f != nil {
		w.f.Close()
		w.f = nil
	}
}

// qualityOn reports whether the call quality report goes anywhere.
func (s *MeetService) qualityOn() bool { return s.quality != nil || s.settingsCopy().QualityLog }

// reportQuality writes one record of the call quality report: a line of the file unless -quality-report is off, and
// a line of the log starting "quality:" with -quality-log.
func (s *MeetService) reportQuality(rec any) {
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	s.quality.write(line)
	if s.settingsCopy().QualityLog {
		log.Print("quality: ", string(line))
	}
}

// qualityRemote is the record of what one remote person delivered in the last interval. Counts are of the interval.
// Nothing in it names a person: Person counts from 1 in the order people were first reported in the call.
type qualityRemote struct {
	Kind   string  `json:"kind"` // "remote"
	At     int64   `json:"at"`   // unix seconds
	T      float64 `json:"t"`    // seconds since the call began
	Secs   float64 `json:"secs"` // length of the interval
	Call   string  `json:"call"`
	Person int     `json:"person"`

	AudioIn        uint64 `json:"audio_in"`        // frames decoded
	AudioMissed    uint64 `json:"audio_missed"`    // frames that did not arrive: gaps in the sequence
	AudioConcealed uint64 `json:"audio_concealed"` // frames made up for them
	AudioSilent    uint64 `json:"audio_silent"`    // missed and not made up: the gap left silent
	AudioDropped   uint64 `json:"audio_dropped"`   // frames the playout dropped for lateness or a full buffer

	VideoDecodedFPS float64 `json:"video_decoded_fps"`
	VideoShownFPS   float64 `json:"video_shown_fps"` // pictures given to the window
	VideoDropped    uint64  `json:"video_dropped"`   // frames the playout dropped for lateness or a full buffer
	VideoW          int     `json:"video_w"`
	VideoH          int     `json:"video_h"`
	Freezes         int     `json:"freezes"`
	FreezeLongestMs int64   `json:"freeze_longest_ms"`

	AudioBytes uint64 `json:"audio_bytes"` // payload bytes of the frames decoded
	VideoBytes uint64 `json:"video_bytes"`

	PlayoutDelayMs  int64   `json:"playout_delay_ms"`  // the jitter buffer's delay now
	PlayoutJitterMs int64   `json:"playout_jitter_ms"` // 95th percentile of arrival lateness
	DecodeMs        float64 `json:"decode_ms"`         // mean decode time of a video frame
	TileMs          float64 `json:"tile_ms"`           // mean time to draw and compress one picture for the window
	DisplayFPS      int     `json:"display_fps"`       // the rate the window's pictures are drawn at now
}

// qualityLocal is the record of what this app sent in the last interval, over all its sessions to the relay.
type qualityLocal struct {
	Kind   string  `json:"kind"` // "local"
	At     int64   `json:"at"`
	T      float64 `json:"t"`
	Secs   float64 `json:"secs"`
	Call   string  `json:"call"`
	Role   string  `json:"role"`
	People int     `json:"people"` // people in the call, this one included

	RungW         int     `json:"rung_w"`
	RungH         int     `json:"rung_h"`
	RungChanges   int     `json:"rung_changes"`
	RungWhy       string  `json:"rung_why"`        // the reason of the last change, empty without one
	RateKbps      float64 `json:"rate_kbps"`       // the encoder's target
	SentKbps      float64 `json:"sent_kbps"`       // bytes handed to the transport, all sessions
	EncodedFPS    float64 `json:"encoded_fps"`     // camera frames encoded
	SkippedFrames uint64  `json:"skipped_frames"`  // camera frames not encoded because the queue was too old
	EncodeMs      float64 `json:"encode_ms"`       // mean encode time of a video frame at the rung's size
	QueueMs       int64   `json:"queue_ms"`        // the oldest unsent object, the worst session
	Sessions      int     `json:"sessions"`        // sessions to the relay that carry media
	GroupsReset   uint64  `json:"groups_reset"`    // groups given up: reset
	GroupsExpired uint64  `json:"groups_expired"`  // groups given up: expired
	FECLossPct    int     `json:"fec_loss_pct"`    // the loss the audio encoder expects, 0 with FEC off
	Reconnects    int     `json:"reconnects"`      // sessions to the relay made again
	Watchdogs     int     `json:"watchdog_resets"` // times the no-media watchdog made a new session
	DisplayMode   string  `json:"display_mode"`    // -display-fps in use: auto, 30 or 15
	KeyFrames     uint64  `json:"key_frames"`      // key frames written in the interval

	Net *qualityNet `json:"net,omitempty"` // what the transport measured; absent when no session reports it
}

// qualityNet is what kolo-transport measured on the connection of the session with the highest round-trip time, and the
// loss over all the sessions that report. Nothing in it names an address or a key.
type qualityNet struct {
	RTTMs        float64 `json:"rtt_ms"`            // smoothed round-trip time
	MinRTTMs     float64 `json:"min_rtt_ms"`        // lowest round-trip time on the path, close to the propagation delay
	RTTVarMs     float64 `json:"rtt_var_ms"`        // mean deviation of the samples: jitter
	LossPct      float64 `json:"loss_pct"`          // packets lost over packets sent in the interval, all sessions: an upper bound
	CwndBytes    uint64  `json:"cwnd_bytes"`        // bytes the congestion controller lets be in flight
	InFlightByte uint64  `json:"in_flight_bytes"`   // bytes sent and not yet acknowledged
	Controller   string  `json:"controller"`        // reno, cubic or bbr
	BwKbps       float64 `json:"bw_kbps,omitempty"` // bottleneck bandwidth BBR measured; absent for the others
	PathChanges  uint64  `json:"path_changes"`      // times the connection moved to a new path
}

// sub is a - b for counters that restart at zero: when a is below b the counter began again and a is the count.
func sub(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return a - b
}

// peerCounters are the totals one remote person's watch reads at a moment.
type peerCounters struct {
	audioIn, missed, concealed, audioDropped uint64
	videoIn, shown, videoDropped             uint64
	audioBytes, videoBytes                   uint64
}

// remoteRecord is what a person delivered between two readings secs apart.
func remoteRecord(prev, cur peerCounters, secs float64) qualityRemote {
	r := qualityRemote{Kind: "remote", Secs: round1(secs)}
	r.AudioIn = sub(cur.audioIn, prev.audioIn)
	r.AudioMissed = sub(cur.missed, prev.missed)
	r.AudioConcealed = sub(cur.concealed, prev.concealed)
	if r.AudioMissed > r.AudioConcealed {
		r.AudioSilent = r.AudioMissed - r.AudioConcealed
	}
	r.AudioDropped = sub(cur.audioDropped, prev.audioDropped)
	secs = max(secs, 0.001)
	r.VideoDecodedFPS = round1(float64(sub(cur.videoIn, prev.videoIn)) / secs)
	r.VideoShownFPS = round1(float64(sub(cur.shown, prev.shown)) / secs)
	r.VideoDropped = sub(cur.videoDropped, prev.videoDropped)
	r.AudioBytes = sub(cur.audioBytes, prev.audioBytes)
	r.VideoBytes = sub(cur.videoBytes, prev.videoBytes)
	return r
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }

func ms(d time.Duration) float64 { return round1(float64(d) / float64(time.Millisecond)) }

// freezeWatch finds the freezes of one person's picture: a time over freezeAfter between two pictures shown while the
// person sends video. A freeze that goes on over several readings is counted once, and shows its length so far in each.
// It is safe for concurrent use.
type freezeWatch struct {
	mu      sync.Mutex
	last    time.Time // when the last picture was shown; zero before the first one and while the camera is off
	sending bool
	open    bool // the pause since last is counted
	count   int
	longest time.Duration
}

// picture notes a picture shown at now.
func (f *freezeWatch) picture(now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if gap := now.Sub(f.last); !f.last.IsZero() && gap > freezeAfter {
		if !f.open {
			f.count++
		}
		f.longest = max(f.longest, gap)
	}
	f.last, f.open = now, false
}

// setSending tells whether the person's catalog lists a video track. A camera that is off is no freeze, and the pause
// before it comes back is not one.
func (f *freezeWatch) setSending(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sending = on
	if !on {
		f.last, f.open = time.Time{}, false
	}
}

// take returns the freezes and the longest one since the last take, counting the pause that goes on at now.
func (f *freezeWatch) take(now time.Time) (count int, longest time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if gap := now.Sub(f.last); f.sending && !f.last.IsZero() && gap > freezeAfter {
		if !f.open {
			f.count++
			f.open = true
		}
		f.longest = max(f.longest, gap)
	}
	count, longest = f.count, f.longest
	f.count, f.longest = 0, 0
	return count, longest
}

// callQuality is what a call counts for its own record: the events that have no counter elsewhere.
type callQuality struct {
	mu          sync.Mutex
	rungChanges int
	rungWhy     string
	reconnects  int
	watchdogs   int
}

func (q *callQuality) noteRung(why string) {
	q.mu.Lock()
	q.rungChanges++
	q.rungWhy = why
	q.mu.Unlock()
}

func (q *callQuality) noteReconnect() {
	q.mu.Lock()
	q.reconnects++
	q.mu.Unlock()
}

func (q *callQuality) noteWatchdog() {
	q.mu.Lock()
	q.watchdogs++
	q.mu.Unlock()
}

// take returns the counts since the last take.
func (q *callQuality) take() (rung int, why string, reconnects, watchdogs int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	rung, why, reconnects, watchdogs = q.rungChanges, q.rungWhy, q.reconnects, q.watchdogs
	q.rungChanges, q.rungWhy, q.reconnects, q.watchdogs = 0, "", 0, 0
	return rung, why, reconnects, watchdogs
}

// sentStats is what the sessions of the sender showed at one reading, by session: counters restart with a session.
type sentStats map[*kolomoq.Session]kolomoq.SessionStats

// sinceLast adds up what each session carried since prev, and the oldest queue among them.
func (cur sentStats) sinceLast(prev sentStats) (sent, reset, expired uint64, queue time.Duration) {
	for s, st := range cur {
		p := prev[s]
		sent += sub(st.SentBytes, p.SentBytes)
		reset += sub(st.ResetGroups, p.ResetGroups)
		expired += sub(st.ExpiredGroups, p.ExpiredGroups)
		queue = max(queue, st.QueueDelay)
	}
	return sent, reset, expired, queue
}

// net is the transport's view of the sessions that report it, since prev; nil when none does. A session of a connection
// without statistics, such as one through a connection relay, has an empty controller name and is left out.
func (cur sentStats) net(prev sentStats) *qualityNet {
	var worst *kolomoq.SessionStats
	var sent, lost uint64
	for s, st := range cur {
		if st.Transport.CongestionController == "" {
			continue
		}
		p := prev[s].Transport
		sent += sub(st.Transport.PacketsSent, p.PacketsSent)
		lost += sub(st.Transport.PacketsLost, p.PacketsLost)
		if worst == nil || st.Transport.SmoothedRTT > worst.Transport.SmoothedRTT {
			worst = &st
		}
	}
	if worst == nil {
		return nil
	}
	t := worst.Transport
	n := &qualityNet{
		RTTMs: ms(t.SmoothedRTT), MinRTTMs: ms(t.MinRTT), RTTVarMs: ms(t.RTTVariation),
		CwndBytes: t.CongestionWindow, InFlightByte: t.BytesInFlight, Controller: t.CongestionController,
		BwKbps: round1(float64(t.BandwidthEstimate) * 8 / 1000), PathChanges: t.PathChanges,
	}
	if sent > 0 {
		n.LossPct = round1(float64(min(lost, sent)) * 100 / float64(sent))
	}
	return n
}

// qualityLoop writes the record of what this app sends every statsEvery until the call ends.
func (c *call) qualityLoop() {
	if !c.svc.qualityOn() {
		return
	}
	var prev sentStats
	var prevEnc, prevSkip, prevKeys uint64
	at := time.Now()
	t := time.NewTicker(statsEvery)
	defer t.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case now := <-t.C:
			secs := now.Sub(at).Seconds()
			at = now
			pub := c.publisher()
			if pub == nil {
				continue
			}
			cur := sentStats{}
			for _, s := range c.mediaSessions() {
				cur[s] = s.Stats()
			}
			sent, reset, expired, queue := cur.sinceLast(prev)
			net := cur.net(prev)
			prev = cur
			ps := pub.quality()
			rung, why, reconnects, watchdogs := c.q.take()
			c.mu.Lock()
			people := len(c.people) + 1
			c.mu.Unlock()
			secs = max(secs, 0.001)
			c.svc.reportQuality(qualityLocal{
				Kind: "local", At: now.Unix(), T: round1(now.Sub(c.began).Seconds()), Secs: round1(secs), Call: c.id, Role: c.role, People: people,
				RungW: ps.w, RungH: ps.h, RungChanges: rung, RungWhy: why, RateKbps: round1(ps.rate / 1000),
				SentKbps: round1(float64(sent) * 8 / 1000 / secs), EncodedFPS: round1(float64(sub(ps.encoded, prevEnc)) / secs),
				SkippedFrames: sub(ps.skipped, prevSkip), EncodeMs: ms(ps.encode), QueueMs: queue.Milliseconds(), Sessions: len(cur),
				GroupsReset: reset, GroupsExpired: expired, FECLossPct: ps.lossPct, Reconnects: reconnects, Watchdogs: watchdogs,
				DisplayMode: c.svc.tiles.modeName(), KeyFrames: sub(ps.keys, prevKeys), Net: net,
			})
			prevEnc, prevSkip, prevKeys = ps.encoded, ps.skipped, ps.keys
		}
	}
}

// qualityState is what the publisher shows of itself for the record.
type qualityState struct {
	w, h                   int
	rate                   float64 // target bits a second
	encoded, skipped, keys uint64
	encode                 time.Duration
	lossPct                int
}

// quality reads the publisher's state for the call quality record.
func (p *publisher) quality() qualityState {
	p.amu.Lock()
	loss := p.lossPct
	p.amu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	st := qualityState{w: p.vw, h: p.vh, encoded: p.nenc, skipped: p.nskip, keys: p.nkey, lossPct: loss}
	if p.venc != nil {
		st.rate = float64(p.rateLocked(p.vw, p.vh))
		st.encode = time.Duration(p.cost * float64(p.vw*p.vh))
	}
	return st
}
