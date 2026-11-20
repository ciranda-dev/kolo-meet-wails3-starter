package main

import (
	"log"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxReportRunes bounds a window's error in the log, message and stack together: 8,192 characters, the bound the
// window also cuts at (frontend/src/report.js MAX_REPORT).
const maxReportRunes = 8192

// secretText matches what must never reach the log even inside an error: an invite, a 64-character hex key and a
// data URL (a picture).
var secretText = regexp.MustCompile(`(?i)koloinv0\w*|[0-9a-f]{64,}|data:[^\s"']+`)

// ReportError writes an error of a window to the log as "frontend error: ...", so a person who started the app from
// a terminal sees why the window shows an error pane. The window sends the error's message and stack only; the text
// is cleaned, invites, keys and pictures are hidden, and it is cut to maxReportRunes characters.
func (s *MeetService) ReportError(message, stack string) {
	text := message
	if stack != "" {
		text += "\n" + stack
	}
	log.Print("frontend error: ", reportText(text))
}

// reportText is a window's error as the log gets it.
func reportText(text string) string {
	text = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || unicode.In(r, unicode.Zl, unicode.Zp):
			return '\n'
		case r == '\t':
			return ' '
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError:
			return -1
		}
		return r
	}, strings.ToValidUTF8(text, ""))
	text = secretText.ReplaceAllStringFunc(text, func(m string) string {
		switch m = strings.ToLower(m); {
		case strings.HasPrefix(m, "koloinv0"):
			return "[invite]"
		case strings.HasPrefix(m, "data:"):
			return "[picture]"
		}
		return "[key]"
	})
	if utf8.RuneCountInString(text) > maxReportRunes {
		text = string([]rune(text)[:maxReportRunes])
	}
	return text
}

// orEmpty is a list for a window: never nil, which JSON would send as null and a window could not read as a list.
func orEmpty[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}
