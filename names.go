package main

import (
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxNameRunes bounds a display name: it fits a tile and a notch line. It is the app's choice, not a library's.
const maxNameRunes = 48

// cleanName checks a display name that a person typed: valid UTF-8, no control, format or separator characters,
// blanks collapsed, 1 to maxNameRunes characters. It returns the cleaned name.
func cleanName(s string) (string, error) {
	name := tidyName(s)
	switch {
	case name == "":
		return "", meetErr("name-empty", ErrBadName, "")
	case utf8.RuneCountInString(name) > maxNameRunes:
		return "", meetErr("name-too-long", ErrBadName, fmt.Sprint(maxNameRunes))
	}
	return name, nil
}

// peerName makes a display name of what another participant sent: the same cleaning, cut at maxNameRunes, and a
// short form of its key when nothing is left. A peer's name is never trusted to be well formed.
func peerName(s string, key []byte) string {
	name := tidyName(s)
	if utf8.RuneCountInString(name) > maxNameRunes {
		name = string([]rune(name)[:maxNameRunes])
	}
	if name == "" {
		return "Guest " + hex.EncodeToString(key[:min(len(key), 2)])
	}
	return name
}

// tidyName drops what is not printable text and collapses blanks.
func tidyName(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = true
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), unicode.Is(unicode.Co, r), r == utf8.RuneError:
		default:
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		}
	}
	return b.String()
}
