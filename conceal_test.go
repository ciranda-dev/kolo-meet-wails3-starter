//go:build finished

package main

import (
	"math"
	"testing"

	"github.com/ciranda-dev/kolo-audio/opus"
)

func TestGaps(t *testing.T) {
	var g gaps
	step := uint64(frameSamples)
	for _, tc := range []struct {
		ts   uint64
		want int
	}{
		{1000, 0},              // the first frame: nothing before it
		{1000 + step, 0},       // the next one
		{1000 + 2*step, 0},     //
		{1000 + 5*step, 2},     // two lost
		{1000 + 5*step, 0},     // the same again: not newer
		{1000 + 4*step, 0},     // older: not newer
		{1000 + 6*step, 0},     // the next after the newest
		{1000 + 7*step + 7, 0}, // a few samples of drift are not a loss
		{1000 + 20*step, 12},
	} {
		if got := g.missing(tc.ts); got != tc.want {
			t.Fatalf("ts %d: %d missing, want %d", tc.ts, got, tc.want)
		}
	}
}

func sine(n, offset int, amp float64) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(amp * math.Sin(2*math.Pi*440*float64(i+offset)/sampleRate))
	}
	return out
}

// Two frames that follow each other become one that starts as the first and ends as the second: a splice with no jump
// at either edge, bigger than the steps of the tone itself by nothing.
func TestCrossfadeHasNoClick(t *testing.T) {
	a, b := sine(frameSamples, 0, 12000), sine(frameSamples, frameSamples, 12000)
	c := sine(frameSamples, 2*frameSamples, 12000)
	x := crossfade(a, b)
	if len(x) != frameSamples {
		t.Fatalf("%d samples", len(x))
	}
	// the tone's own largest step between samples
	var natural float64
	all := sine(4*frameSamples, 0, 12000)
	for i := 1; i < len(all); i++ {
		natural = math.Max(natural, math.Abs(float64(all[i])-float64(all[i-1])))
	}
	z := sine(frameSamples, -frameSamples, 12000) // what was played before a
	out := append(append(append([]int16{}, z[frameSamples-1]), x...), c[0])
	var worst float64
	for i := 1; i < len(out); i++ {
		worst = math.Max(worst, math.Abs(float64(out[i])-float64(out[i-1])))
	}
	if worst > 1.5*natural {
		t.Fatalf("largest step %.0f in the splice, the tone's own is %.0f", worst, natural)
	}
}

// A burst of 400 ms arrives at once; the mixer plays it down to its target in under 2 s, never takes more than a
// frame out of a queue in a tick, and keeps every frame's order.
func TestMixerCatchesUpAfterABurst(t *testing.T) {
	m := newMixer(nil)
	for i := range mixQueueFrames {
		m.push("ana", sine(frameSamples, i*frameSamples, 8000))
	}
	dst := make([]int16, frameSamples)
	ticks := 0
	for ; ticks < 200; ticks++ {
		m.mu.Lock()
		n := len(m.queues["ana"])
		m.mu.Unlock()
		if n <= mixTarget {
			break
		}
		before := n
		if !m.mix(dst) {
			t.Fatal("a queue with frames mixed nothing")
		}
		m.mu.Lock()
		after := len(m.queues["ana"])
		m.mu.Unlock()
		if before-after > 2 {
			t.Fatalf("a tick took %d frames out", before-after)
		}
	}
	if d := ticks * 20; d >= 2000 {
		t.Fatalf("caught up after %d ms", d)
	}
	t.Logf("400 ms burst: queue back to %d frames after %d ms of ticks", mixTarget, ticks*20)
	// at or under the target the mixer plays one frame a tick and compresses nothing
	m.mu.Lock()
	n := len(m.queues["ana"])
	m.mu.Unlock()
	for range 3 {
		m.mix(dst)
	}
	m.mu.Lock()
	left := len(m.queues["ana"])
	m.mu.Unlock()
	if n-left != 3 {
		t.Fatalf("under the target %d frames went in 3 ticks", n-left)
	}
}

// The audio that comes out of the catch-up is continuous: no step between samples bigger than the tone's own.
func TestMixerCatchUpOutputIsContinuous(t *testing.T) {
	m := newMixer(nil)
	for i := range mixQueueFrames {
		m.push("ana", sine(frameSamples, i*frameSamples, 12000))
	}
	var out []int16
	dst := make([]int16, frameSamples)
	for range 30 {
		if m.mix(dst) {
			out = append(out, dst...)
		}
	}
	var natural, worst float64
	all := sine(4*frameSamples, 0, 12000)
	for i := 1; i < len(all); i++ {
		natural = math.Max(natural, math.Abs(float64(all[i])-float64(all[i-1])))
	}
	for i := 1; i < len(out); i++ {
		worst = math.Max(worst, math.Abs(float64(out[i])-float64(out[i-1])))
	}
	if worst > 1.5*natural {
		t.Fatalf("largest step %.0f after the catch-up, the tone's own is %.0f", worst, natural)
	}
}

// With in-band FEC on, a packet is no bigger than without it: the microphone's packets must not follow speech.
func TestFECKeepsPacketSizeConstant(t *testing.T) {
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, pct := range []int{0, 5, 20} {
		enc, err := opus.NewEncoder(audioEncoderConfig())
		if err != nil {
			t.Fatal(err)
		}
		setLoss(enc, pct)
		sizes := map[int]int{}
		out := make([]byte, opus.PacketBufferSize)
		for f := range 200 {
			amp := 0.0
			if f%40 > 10 { // speech and silence
				amp = 9000
			}
			n, err := enc.Encode(sine(frameSamples, f*frameSamples, amp), out)
			if err != nil {
				t.Fatal(err)
			}
			sizes[n]++
		}
		enc.Close()
		if len(sizes) != 1 {
			t.Fatalf("loss %d%%: packet sizes %v, want one", pct, sizes)
		}
	}
}

// A frame lost between two others is made up: the decoder makes the frames after the last one heard, and the one before
// the next from its forward error correction, up to maxConceal in a row.
func TestConcealGap(t *testing.T) {
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	enc, err := opus.NewEncoder(audioEncoderConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	setLoss(enc, 10)
	dec, err := opus.NewDecoder(sampleRate, audioChannels)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	packets := make([][]byte, 12)
	for i := range packets {
		out := make([]byte, opus.PacketBufferSize)
		n, err := enc.Encode(sine(frameSamples, i*frameSamples, 9000), out)
		if err != nil {
			t.Fatal(err)
		}
		packets[i] = out[:n]
	}
	pcm := make([]int16, int(opus.MaxPacketDuration.Seconds()*sampleRate)*audioChannels)
	for i := range 4 { // the first packets arrive, and the decoder settles
		dec.Decode(packets[i], pcm)
	}
	for _, miss := range []int{1, 2, 3, 6} {
		c := &call{mix: newMixer(nil)}
		p := &participant{id: "ana"}
		c.concealGap(p, dec, packets[4+miss], pcm, miss)
		want := min(miss, maxConceal)
		if got := int(p.concealed.Load()); got != want {
			t.Fatalf("%d lost: %d frames made up, want %d", miss, got, want)
		}
		c.mix.mu.Lock()
		queued := len(c.mix.queues["ana"])
		c.mix.mu.Unlock()
		if queued != want {
			t.Fatalf("%d lost: %d frames queued, want %d", miss, queued, want)
		}
		dec.Decode(packets[4+miss], pcm)
	}
}
