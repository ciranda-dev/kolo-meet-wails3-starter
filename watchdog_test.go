//go:build finished

package main

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)

// lookEvery feeds the watchdog the people at each step of 500 ms from t0 until d has passed, with count(i) frames
// for person id, and returns the first time it says dead, or -1.
func lookEvery(w *watchdog, d time.Duration, people func(i int) []heard) time.Duration {
	for i := 0; time.Duration(i)*watchdogEvery <= d; i++ {
		now := t0.Add(time.Duration(i) * watchdogEvery)
		if w.receiving(now, people(i)) {
			return now.Sub(t0)
		}
	}
	return -1
}

func TestWatchdogDeadAfterNoData(t *testing.T) {
	w := newWatchdog()
	// Ana sends audio and video, as her catalog says, and 40 frames arrive in the first second and none after.
	got := lookEvery(w, 20*time.Second, func(i int) []heard {
		n := uint64(min(i, 2) * 25)
		return []heard{{id: "ana", audioOn: true, videoOn: true, audioIn: n, videoIn: n}}
	})
	if want := 1*time.Second + noDataAfter; got != want {
		t.Fatalf("dead at %v, want %v: noDataAfter after the last frame", got, want)
	}
}

func TestWatchdogQuietWhileMediaFlows(t *testing.T) {
	w := newWatchdog()
	if got := lookEvery(w, 60*time.Second, func(i int) []heard {
		return []heard{{id: "ana", audioOn: true, videoOn: true, audioIn: uint64(i * 25), videoIn: uint64(i * 15)}}
	}); got != -1 {
		t.Fatalf("dead at %v with media arriving", got)
	}
}

// A person who turned the microphone and the camera off sends nothing, and that is not a dead path.
func TestWatchdogNothingExpected(t *testing.T) {
	w := newWatchdog()
	if got := lookEvery(w, 60*time.Second, func(int) []heard { return []heard{{id: "ana"}, {id: "bruno"}} }); got != -1 {
		t.Fatalf("dead at %v with nobody sending", got)
	}
}

// Video that stops while audio goes on is a path that drops the large datagrams: the call counts it dead.
func TestWatchdogVideoOnly(t *testing.T) {
	w := newWatchdog()
	got := lookEvery(w, 30*time.Second, func(i int) []heard {
		return []heard{{id: "ana", audioOn: true, videoOn: true, audioIn: uint64(i * 25), videoIn: 40}}
	})
	if got < noDataAfter || got > noDataAfter+2*time.Second {
		t.Fatalf("dead at %v, want near %v", got, noDataAfter)
	}
}

// One person silent while another one is heard is that person's own trouble: this path is fine.
func TestWatchdogOneOfTwo(t *testing.T) {
	w := newWatchdog()
	if got := lookEvery(w, 60*time.Second, func(i int) []heard {
		return []heard{{id: "ana", audioOn: true, audioIn: 5}, {id: "bruno", audioOn: true, audioIn: uint64(i * 25)}}
	}); got != -1 {
		t.Fatalf("dead at %v with one person heard", got)
	}
}

// A reset that brings nothing back doubles the wait, up to noDataMaxDoublings times, and media heard clears it.
func TestWatchdogStrikes(t *testing.T) {
	w := newWatchdog()
	silent := []heard{{id: "ana", audioOn: true, audioIn: 1}}
	if !w.receiving(t0, silent) {
		w.receiving(t0.Add(noDataAfter), silent)
	}
	now := t0.Add(noDataAfter)
	var waits []time.Duration
	for range 5 {
		w.fired(now)
		start := now
		for !w.receiving(now, silent) {
			now = now.Add(watchdogEvery)
		}
		waits = append(waits, now.Sub(start))
	}
	want := []time.Duration{2 * noDataAfter, 4 * noDataAfter, 8 * noDataAfter, 8 * noDataAfter, 8 * noDataAfter}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatalf("waits %v, want %v", waits, want)
		}
	}
	w.receiving(now, []heard{{id: "ana", audioOn: true, audioIn: 2}})
	if w.strikes != 0 {
		t.Fatalf("strikes %d after media came back", w.strikes)
	}
}

func TestWatchdogSending(t *testing.T) {
	w := newWatchdog()
	// the queue is old and almost nothing leaves for the threshold: dead
	var got time.Duration = -1
	for i := 0; i < 100; i++ {
		now := t0.Add(time.Duration(i) * watchdogEvery)
		if w.sending(now, uint64(i*10), true) {
			got = now.Sub(t0)
			break
		}
	}
	if got != noDataAfter {
		t.Fatalf("dead at %v, want %v", got, noDataAfter)
	}
	// a queue that is old but moves is only slow
	w = newWatchdog()
	for i := 0; i < 200; i++ {
		if w.sending(t0.Add(time.Duration(i)*watchdogEvery), uint64(i)*4096, true) {
			t.Fatalf("dead at step %d with bytes leaving", i)
		}
	}
	// a queue that is not old is no trouble, whatever left
	w = newWatchdog()
	for i := 0; i < 200; i++ {
		if w.sending(t0.Add(time.Duration(i)*watchdogEvery), 0, false) {
			t.Fatalf("dead at step %d with nothing waiting", i)
		}
	}
}

func TestBackoff(t *testing.T) {
	prev := time.Duration(0)
	for n := range 12 {
		lo, hi := backoff(n, 0), backoff(n, 1)
		if lo >= hi {
			t.Fatalf("n=%d: no jitter: %v %v", n, lo, hi)
		}
		mid := backoff(n, 0.5)
		if mid < prev {
			t.Fatalf("n=%d: %v after %v: not growing", n, mid, prev)
		}
		prev = mid
		if hi > reconnectMax*5/4 || lo < reconnectMin*3/4 {
			t.Fatalf("n=%d: %v to %v out of bounds", n, lo, hi)
		}
	}
	if backoff(0, 0.5) != reconnectMin || backoff(30, 0.5) != reconnectMax {
		t.Fatalf("ends: %v %v", backoff(0, 0.5), backoff(30, 0.5))
	}
	// the sum of the waits up to the window is bounded: a call does not knock more than a few dozen times
	var total time.Duration
	n := 0
	for total < reconnectWindow {
		total += backoff(n, 0.5)
		n++
	}
	if n > 20 {
		t.Fatalf("%d attempts in %v", n, reconnectWindow)
	}
}
