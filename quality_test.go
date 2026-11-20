//go:build finished

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	kolomoq "github.com/ciranda-dev/kolo-moq"
	kolotransport "github.com/ciranda-dev/kolo-transport"
)

func TestQualityConfig(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if c, err := LoadConfig([]string{"-quality-report", "off", "-quality-log"}, env(nil), os.Stderr); err != nil || c.QualityReport != "off" || !c.QualityLog {
		t.Errorf("flags: %+v %v", c, err)
	}
	if c, _ := LoadConfig(nil, env(map[string]string{"KOLO_MEET_QUALITY": "off", "KOLO_MEET_QUALITY_LOG": "1"}), os.Stderr); c.QualityReport != "off" || !c.QualityLog {
		t.Errorf("variables: %+v", c)
	}
	if c, _ := LoadConfig([]string{"-quality-report", "on", "-quality-log=false"}, env(map[string]string{"KOLO_MEET_QUALITY": "off", "KOLO_MEET_QUALITY_LOG": "1"}), os.Stderr); c.QualityReport != "on" || c.QualityLog {
		t.Errorf("a flag wins over its variable: %+v", c)
	}
	if _, err := LoadConfig(nil, env(map[string]string{"KOLO_MEET_QUALITY_LOG": "yes please"}), os.Stderr); !errors.Is(err, ErrMalformed) {
		t.Errorf("a bad KOLO_MEET_QUALITY_LOG: %v", err)
	}
	if s, err := (Config{DataDir: t.TempDir()}).resolve(); err != nil || s.QualityReport != qualityOn || s.QualityLog {
		t.Errorf("default: %+v %v", s, err)
	}
	for _, v := range []string{"yes", "ON", "1", " off"} {
		var ce *ConfigError
		_, err := Config{DataDir: t.TempDir(), QualityReport: v}.resolve()
		if !errors.As(err, &ce) || ce.Setting != "quality-report" || !errors.Is(err, ErrMalformed) {
			t.Errorf("-quality-report %q: %v", v, err)
		}
	}
	if _, err := LoadConfig([]string{"-quality-report"}, env(nil), &bytes.Buffer{}); err == nil || errors.Is(err, flag.ErrHelp) {
		t.Errorf("a flag with no value: %v", err)
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// The report file has mode 0600, even when it was there with another; it starts a new file at the size cap and keeps
// the one before as ".1", so it never holds more than twice the cap; a line is never cut in two.
func TestQualityWriter(t *testing.T) {
	var nilWriter *qualityWriter
	nilWriter.write([]byte("x"))
	nilWriter.close()

	dir := t.TempDir()
	path := filepath.Join(dir, qualityFileName)
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const max = 1000
	w := newQualityWriter(dir, max)
	line := []byte(`{"n":"` + strings.Repeat("x", 90) + `"}`) // 100 bytes with its newline
	for i := range 35 {
		w.write(line)
		if runtime.GOOS != "windows" {
			if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("after line %d: %v %v", i, info, err)
			}
		}
		for _, p := range []string{path, path + ".1"} {
			if info, err := os.Stat(p); err == nil && info.Size() > max {
				t.Fatalf("%s is %d bytes, cap %d", p, info.Size(), max)
			}
		}
	}
	w.close()
	w.write(line) // after close nothing is written
	cur, prev := readLines(t, path), readLines(t, path+".1")
	if len(cur) == 0 || len(prev) == 0 {
		t.Fatalf("current %d lines, previous %d", len(cur), len(prev))
	}
	for _, l := range append(append([]string{}, cur...), prev...) {
		if l != string(line) && l != "old" {
			t.Fatalf("a line was cut: %q", l)
		}
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path + ".1"); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("the previous file: %v %v", info, err)
		}
	}
	if len(cur)+len(prev) < 15 || len(cur)+len(prev) > 20 {
		t.Errorf("%d lines kept of 36, want the last 15 to 20", len(cur)+len(prev))
	}
}

// A write that cannot happen is logged once and the call goes on.
func TestQualityWriterFailureIsLoggedOnce(t *testing.T) {
	var out lockedBuffer
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	w := newQualityWriter(filepath.Join(t.TempDir(), "missing"), 1000)
	for range 3 {
		w.write([]byte("{}"))
	}
	if n := strings.Count(out.String(), "quality report:"); n != 1 {
		t.Errorf("%d log lines:\n%s", n, out.String())
	}
}

func TestRemoteRecord(t *testing.T) {
	prev := peerCounters{audioIn: 100, missed: 5, concealed: 4, audioDropped: 1, videoIn: 60, shown: 30, audioBytes: 1000, videoBytes: 50000}
	cur := peerCounters{audioIn: 350, missed: 12, concealed: 9, audioDropped: 3, videoIn: 210, shown: 105, audioBytes: 21000, videoBytes: 250000}
	r := remoteRecord(prev, cur, 5)
	want := qualityRemote{Kind: "remote", Secs: 5, AudioIn: 250, AudioMissed: 7, AudioConcealed: 5, AudioSilent: 2, AudioDropped: 2,
		VideoDecodedFPS: 30, VideoShownFPS: 15, AudioBytes: 20000, VideoBytes: 200000}
	if r != want {
		t.Errorf("record\n got %+v\nwant %+v", r, want)
	}
	// More made up than missed (the frame before a packet comes from its forward error correction): no silence.
	if r = remoteRecord(peerCounters{}, peerCounters{missed: 1, concealed: 3}, 5); r.AudioSilent != 0 {
		t.Errorf("silent %d", r.AudioSilent)
	}
	// A counter that began again (a new watch) counts from zero.
	if r = remoteRecord(peerCounters{audioIn: 500}, peerCounters{audioIn: 40}, 5); r.AudioIn != 40 {
		t.Errorf("restarted counter: %d", r.AudioIn)
	}
	if r = remoteRecord(peerCounters{}, peerCounters{videoIn: 10}, 0); r.VideoDecodedFPS <= 0 {
		t.Errorf("a zero interval: %+v", r)
	}
}

func TestFreezeWatch(t *testing.T) {
	t0 := time.Unix(1000, 0)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	var f freezeWatch
	f.setSending(true)
	// Nothing was shown yet: no freeze, however long it takes.
	if n, _ := f.take(at(10_000)); n != 0 {
		t.Errorf("before the first picture: %d", n)
	}
	for ms := 0; ms < 1000; ms += 33 { // steady
		f.picture(at(ms))
	}
	f.picture(at(1000))
	if n, l := f.take(at(1000)); n != 0 || l != 0 {
		t.Errorf("steady: %d %v", n, l)
	}
	f.picture(at(1000 + 500)) // exactly 500 ms: not over the limit
	f.picture(at(1500 + 800)) // 800 ms
	if n, l := f.take(at(2400)); n != 1 || l != 800*time.Millisecond {
		t.Errorf("one freeze of 800 ms: %d %v", n, l)
	}
	// A freeze that goes on through two readings is one, and shows its length so far in each.
	if n, l := f.take(at(2300 + 3000)); n != 1 || l != 3*time.Second {
		t.Errorf("open freeze, first reading: %d %v", n, l)
	}
	if n, l := f.take(at(2300 + 8000)); n != 0 || l != 8*time.Second {
		t.Errorf("open freeze, second reading: %d %v", n, l)
	}
	f.picture(at(2300 + 9000))
	if n, l := f.take(at(2300 + 9000)); n != 0 || l != 9*time.Second {
		t.Errorf("the freeze ends: %d %v", n, l)
	}
	if n, l := f.take(at(2300 + 9100)); n != 0 || l != 0 {
		t.Errorf("after it: %d %v", n, l)
	}
	// A camera that is off is no freeze, and neither is the pause before it comes back.
	f.setSending(false)
	if n, _ := f.take(at(60_000)); n != 0 {
		t.Errorf("camera off: %d", n)
	}
	f.setSending(true)
	f.picture(at(70_000))
	if n, _ := f.take(at(70_000)); n != 0 {
		t.Errorf("first picture after the camera came back: %d", n)
	}
}

func TestCallQualityTake(t *testing.T) {
	var q callQuality
	q.noteRung("a")
	q.noteRung("b")
	q.noteReconnect()
	q.noteWatchdog()
	q.noteWatchdog()
	if r, why, rc, w := q.take(); r != 2 || why != "b" || rc != 1 || w != 2 {
		t.Errorf("%d %q %d %d", r, why, rc, w)
	}
	if r, why, rc, w := q.take(); r != 0 || why != "" || rc != 0 || w != 0 {
		t.Errorf("a second take: %d %q %d %d", r, why, rc, w)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// quickStats makes the reports come every 300 ms for the test.
func quickStats(t *testing.T) {
	t.Helper()
	old := statsEvery
	statsEvery = 300 * time.Millisecond
	t.Cleanup(func() { statsEvery = old }) // registered first, so it runs after the services shut down
}

// records reads the report file as JSON objects.
func records(t *testing.T, dir string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, qualityFileName))
	if err != nil {
		return nil
	}
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("a line of the report is not JSON: %q", l)
		}
		out = append(out, m)
	}
	return out
}

func num(m map[string]any, k string) float64 {
	v, _ := m[k].(float64)
	return v
}

var longHex = regexp.MustCompile(`[0-9a-fA-F]{32,}`)

// A call writes the report: a line for each remote person and one for the sender, with the numbers the app counts, in a
// file of mode 0600 that holds no name, key, invite or address; with -quality-log the same lines go to the log.
func TestCallWritesTheQualityReport(t *testing.T) {
	quickStats(t)
	var out lockedBuffer
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	before := settledGoroutines()
	func() {
		first := freeUDP(t)
		a := startPeer(t, "Ana", Config{First: first, QualityLog: true, DisplayFPS: display30}, newFakeHW(parties[0].luma, parties[0].freq), nil)
		a.rec.waitNetwork(t, netReady)
		b := startPeer(t, "Bruno", Config{Bootstrap: first}, newFakeHW(parties[1].luma, parties[1].freq), nil)
		b.rec.waitNetwork(t, netReady)
		invite := startCall(t, []*peer{a, b})
		idA, idB := idOf(t, a), idOf(t, b)
		waitUntil(t, "a remote and a local line", func() bool {
			var remote, local bool
			for _, r := range records(t, a.data) {
				remote = remote || (r["kind"] == "remote" && num(r, "audio_in") > 0 && num(r, "video_decoded_fps") > 0 && num(r, "video_shown_fps") > 0)
				local = local || (r["kind"] == "local" && num(r, "rung_h") > 0 && num(r, "sent_kbps") > 0 && num(r, "encoded_fps") > 0)
			}
			return remote && local
		})
		waitUntil(t, "a local line with the transport's numbers", func() bool {
			for _, r := range records(t, a.data) {
				if n, ok := r["net"].(map[string]any); ok && r["kind"] == "local" && n["controller"] == "reno" && num(n, "rtt_ms") > 0 {
					return true
				}
			}
			return false
		})
		path := filepath.Join(a.data, qualityFileName)
		if info, err := os.Stat(path); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
			t.Errorf("the report file: %v %v", info, err)
		}
		for _, r := range records(t, a.data) {
			if r["call"] == "" || num(r, "at") < 1e9 || num(r, "secs") <= 0 {
				t.Errorf("a line lacks its call, time or interval: %v", r)
			}
			switch r["kind"] {
			case "remote":
				for _, k := range []string{"person", "audio_in", "audio_missed", "audio_concealed", "audio_silent", "video_decoded_fps", "video_shown_fps",
					"freezes", "freeze_longest_ms", "audio_bytes", "video_bytes", "playout_delay_ms", "playout_jitter_ms", "decode_ms", "tile_ms", "display_fps"} {
					if _, ok := r[k]; !ok {
						t.Errorf("a remote line lacks %q: %v", k, r)
					}
				}
				if num(r, "person") != 1 || num(r, "display_fps") != tileRateHigh {
					t.Errorf("person %v display %v", r["person"], r["display_fps"])
				}
			case "local":
				for _, k := range []string{"role", "people", "rung_w", "rung_h", "rung_changes", "rate_kbps", "sent_kbps", "encoded_fps", "skipped_frames",
					"encode_ms", "queue_ms", "sessions", "groups_reset", "groups_expired", "fec_loss_pct", "reconnects", "watchdog_resets", "display_mode", "key_frames"} {
					if _, ok := r[k]; !ok {
						t.Errorf("a local line lacks %q: %v", k, r)
					}
				}
				if r["display_mode"] != display30 {
					t.Errorf("display_mode %v, want %s", r["display_mode"], display30)
				}
				if r["role"] != roleHost || num(r, "people") < 1 || num(r, "people") > 2 {
					t.Errorf("role %v people %v", r["role"], r["people"])
				}
			default:
				t.Errorf("kind %v", r["kind"])
			}
		}
		// Nothing that names a person, a key or an address is in the file or in the log.
		raw, _ := os.ReadFile(path)
		text := string(raw)
		for _, l := range strings.Split(out.String(), "\n") {
			if strings.Contains(l, "quality: ") {
				text += l
			}
		}
		for _, secret := range []string{"Ana", "Bruno", idA, idB, invite, "127.0.0.1", first, "koloinv"} {
			if strings.Contains(text, secret) {
				t.Errorf("the report holds %q", secret)
			}
		}
		if h := longHex.FindString(string(raw)); h != "" {
			t.Errorf("a long hex string in the report: %s", h)
		}
		waitUntil(t, "two people in a local line", func() bool {
			for _, r := range records(t, a.data) {
				if r["kind"] == "local" && num(r, "people") == 2 {
					return true
				}
			}
			return false
		})
		for _, l := range readLines(t, path)[len(readLines(t, path))-2:] {
			t.Log("report line: ", l)
		}
		for _, l := range strings.Split(out.String(), "\n") {
			if strings.Contains(l, "quality: ") {
				t.Log("log line: ", l)
				break
			}
		}
		if !strings.Contains(out.String(), "quality: {") {
			t.Errorf("no quality line in the log:\n%s", out.String())
		}
		if n := strings.Count(out.String(), "call quality report is on: "); n != 2 { // one for each app, in this process
			t.Errorf("%d start notices, want 2:\n%s", n, out.String())
		}
		if n := strings.Count(out.String(), "to "+path+"; turn it off with -quality-report off or KOLO_MEET_QUALITY=off"); n != 1 {
			t.Errorf("%d notices say where this report goes and how to stop it:\n%s", n, out.String())
		}
		for _, l := range strings.Split(out.String(), "\n") {
			if i := strings.Index(l, "quality: "); i >= 0 {
				var m map[string]any
				if err := json.Unmarshal([]byte(l[i+len("quality: "):]), &m); err != nil {
					t.Errorf("a quality line of the log is not JSON: %q", l)
				}
			}
		}
		if b.LeaveCall() != nil || a.LeaveCall() != nil {
			t.Error("leave")
		}
		a.ServiceShutdown()
		b.ServiceShutdown()
	}()
	var after int
	for range 100 {
		runtime.GC()
		if after = runtime.NumGoroutine(); after <= before+4 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	buf := make([]byte, 1<<20)
	t.Fatalf("%d goroutines before, %d after:\n%s", before, after, buf[:runtime.Stack(buf, true)])
}

// With -quality-report off nothing is written to the data directory; -quality-log still prints the lines.
func TestQualityReportOff(t *testing.T) {
	quickStats(t)
	var out lockedBuffer
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	first := freeUDP(t)
	a := startPeer(t, "Ana", Config{First: first, QualityReport: qualityOff, QualityLog: true}, newFakeHW(parties[0].luma, parties[0].freq), nil)
	a.rec.waitNetwork(t, netReady)
	b := startPeer(t, "Bruno", Config{Bootstrap: first, QualityReport: qualityOff}, newFakeHW(parties[1].luma, parties[1].freq), nil)
	b.rec.waitNetwork(t, netReady)
	startCall(t, []*peer{a, b})
	waitUntil(t, "a quality line in the log", func() bool { return strings.Contains(out.String(), "quality: {") })
	time.Sleep(3 * statsEvery)
	for _, p := range []*peer{a, b} {
		entries, err := os.ReadDir(p.data)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "kolo-meet-quality") {
				t.Errorf("%s was written with -quality-report off", e.Name())
			}
		}
	}
	if b.quality != nil || a.quality != nil {
		t.Error("a writer exists with the report off")
	}
}

// By default, with no -quality-log, the file is written and the log has no quality line.
func TestQualityReportDefaultIsOnWithoutLog(t *testing.T) {
	quickStats(t)
	var out lockedBuffer
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	ps := network(t, 2)
	startCall(t, ps)
	waitUntil(t, "report lines", func() bool { return len(records(t, ps[1].data)) >= 2 })
	if strings.Contains(out.String(), "quality:") {
		t.Error("a quality line in the log without -quality-log")
	}
}

func sessionWith(st kolotransport.ConnStats) (*kolomoq.Session, kolomoq.SessionStats) {
	return new(kolomoq.Session), kolomoq.SessionStats{Transport: st}
}

// The net part of the local line comes from the session with the highest round trip, counts loss over every session
// that reports it, leaves out a session with no statistics, and is absent when none reports.
func TestQualityNetFromTheTransportStats(t *testing.T) {
	ms := time.Millisecond
	s1, a1 := sessionWith(kolotransport.ConnStats{SmoothedRTT: 20 * ms, MinRTT: 10 * ms, RTTVariation: 3 * ms, PacketsSent: 1000, PacketsLost: 10,
		CongestionWindow: 60000, BytesInFlight: 12000, CongestionController: "reno", PathChanges: 1})
	s2, a2 := sessionWith(kolotransport.ConnStats{SmoothedRTT: 85*ms + 400*time.Microsecond, MinRTT: 40 * ms, RTTVariation: 12 * ms, PacketsSent: 500, PacketsLost: 30,
		CongestionWindow: 30000, BytesInFlight: 29000, CongestionController: "bbr", BandwidthEstimate: 250000})
	s3, a3 := sessionWith(kolotransport.ConnStats{}) // a connection relay: no statistics
	prev := sentStats{s1: {Transport: kolotransport.ConnStats{PacketsSent: 900, PacketsLost: 10, CongestionController: "reno"}}}
	cur := sentStats{s1: a1, s2: a2, s3: a3}
	got := cur.net(prev)
	want := &qualityNet{RTTMs: 85.4, MinRTTMs: 40, RTTVarMs: 12, LossPct: 5, CwndBytes: 30000, InFlightByte: 29000, Controller: "bbr", BwKbps: 2000}
	if got == nil || *got != *want {
		t.Fatalf("net %+v, want %+v", got, want)
	}
	// Reno measures no bandwidth, so the field is left out; a new session counts whole.
	only := sentStats{s1: a1}
	b, err := json.Marshal(only.net(nil))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "bw_kbps") || !strings.Contains(string(b), `"controller":"reno"`) || !strings.Contains(string(b), `"loss_pct":1`) {
		t.Errorf("reno line: %s", b)
	}
	if (sentStats{s3: a3}).net(nil) != nil || (sentStats{}).net(nil) != nil {
		t.Error("a net part for sessions that report nothing")
	}
	// A counter that restarted (a session made again) counts as itself, and loss never passes 100%.
	n := sentStats{s1: {Transport: kolotransport.ConnStats{PacketsSent: 10, PacketsLost: 50, CongestionController: "reno"}}}.net(prev)
	if n == nil || n.LossPct != 100 {
		t.Errorf("restarted counters: %+v", n)
	}
	// The record carries it as "net", and the whole line holds no address or key.
	line, err := json.Marshal(qualityLocal{Kind: "local", Net: got})
	if err != nil || !strings.Contains(string(line), `"net":{"rtt_ms":85.4,"min_rtt_ms":40,"rtt_var_ms":12,"loss_pct":5,"cwnd_bytes":30000,"in_flight_bytes":29000,"controller":"bbr","bw_kbps":2000,"path_changes":0}`) {
		t.Errorf("line %s %v", line, err)
	}
	if line, _ := json.Marshal(qualityLocal{Kind: "local"}); strings.Contains(string(line), `"net"`) {
		t.Errorf("a line without statistics has net: %s", line)
	}
}
