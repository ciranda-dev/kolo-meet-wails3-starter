//go:build finished

package main

import "testing"

// A test sets the emit hook in the middle of a call, while the call's goroutines send its changes: setting it must not
// race with them (go test -race).
func TestEmitHookSetDuringACall(t *testing.T) {
	c := bareCall(t, roleGuest, 8)
	s := c.svc
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			s.callChanged(c)
		}
	}()
	hook := func() {}
	for range 200 {
		s.emitHook.Store(&hook)
	}
	<-done
}
