//go:build finished

package main

import (
	"fmt"
	"strings"
)

// linkSummary says, for each participant of the call, whether a link and a key are there; for failures of tests.
func (s *MeetService) linkSummary() string {
	c := s.activeCall()
	if c == nil {
		return "no call"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var b strings.Builder
	for _, id := range c.order {
		p := c.people[id]
		fmt.Fprintf(&b, "  %s: state=%s link=%v keyErr=%v keyTries=%d watchErr=%v", p.name, p.state, p.link != nil, p.keyErr, p.keyTries, p.watchErr)
		if p.link != nil {
			fmt.Fprintf(&b, " sessErr=%v", p.link.sess.Err())
		}
		fmt.Fprintln(&b)
	}
	fmt.Fprintf(&b, "  pending=%d admitted=%d\n", len(c.pending), len(c.admitted))
	return b.String()
}
