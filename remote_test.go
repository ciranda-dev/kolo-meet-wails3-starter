//go:build finished

package main

import (
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// mediaLine matches the counters of the playout's audio in a "media from" line of the log.
var mediaLine = regexp.MustCompile(`media from (\w{8}) \(microphone (on|off), camera (on|off)\): audio ([0-9.]+)/s .*?audio \{in (\d+) played (\d+)`)

// lastMediaLine returns, for the newest "media from" line about the person id, the state of the microphone, the audio
// frames decoded a second, and the frames the playout received and played.
func lastMediaLine(out, id string) (mic string, rate float64, in, played uint64, ok bool) {
	for _, m := range mediaLine.FindAllStringSubmatch(out, -1) {
		if !strings.HasPrefix(id, m[1]) {
			continue
		}
		mic, ok = m[2], true
		rate, _ = strconv.ParseFloat(m[4], 64)
		in, _ = strconv.ParseUint(m[5], 10, 64)
		played, _ = strconv.ParseUint(m[6], 10, 64)
	}
	return mic, rate, in, played, ok
}

// The media line of the log counts the audio a person plays: with the other person's microphone on, the frames decoded
// a second and the playout's counters grow; with it off, the line says so, so a line of zeros is not read as audio lost
// on the way.
func TestMediaLogCountsTheAudio(t *testing.T) {
	quickStats(t)
	var out lockedBuffer
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	ps := network(t, 2)
	startCall(t, ps)
	guest := idOf(t, ps[1])
	waitUntil(t, "a media line with the guest's audio played", func() bool {
		mic, rate, in, played, ok := lastMediaLine(out.String(), guest)
		return ok && mic == "on" && rate > 0 && in > 0 && played > 0
	})
	if _, err := ps[1].SetMicrophone(false); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "a media line that says the guest's microphone is off", func() bool {
		mic, _, _, _, ok := lastMediaLine(out.String(), guest)
		return ok && mic == "off"
	})
}
