package main

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	kolodht "github.com/ciranda-dev/kolo-dht"
	koloswarm "github.com/ciranda-dev/kolo-swarm"
)

// Defaults of the settings that have one.
const (
	// kolo-media measured protected group calls of 4, 8 and 20 (docs/limits.md, "Protected group calls: measured");
	// the product asks for 8, and 20 is the most its tests and decision 0010 allow ("Group size").
	defaultMaxParticipants = 8
	maxParticipantsCeiling = 20
	defaultFindTimeout     = time.Minute
	// relayFindTimeout is the default with connection relays: kolo-swarm tries the relays only after kolo-dht's
	// Connect failed, which takes up to its 3 relays (kolo-dht kolodht.go:31) times PunchTimeout 45 s
	// (kolo-dht config.go:43, 135), and one relay dial takes up to 60 s (decision 0013, "Dial").
	relayFindTimeout      = 3*45*time.Second + 60*time.Second
	defaultLookupInterval = 5 * time.Second
	localListen           = "127.0.0.1:0"
	publicListen          = "0.0.0.0:0"
	// defaultLocalPort is where -local runs its entry node, on 127.0.0.1 only. It is fixed so every window of the
	// computer finds it without a flag; next to kolo-node's 7447 and 7448, so it does not clash with a local
	// kolo-node, and below the ephemeral ranges (from 32768 on Linux, 49152 on macOS and Windows).
	defaultLocalPort = 7451
)

// The three networks. The default is the public one.
const (
	modePublic = "public" // the public entry nodes built into kolo-dht
	modeOwn    = "own"    // -bootstrap, KOLO_BOOTSTRAP or -first: only those entry nodes
	modeLocal  = "local"  // -local: an entry node on 127.0.0.1, for the windows of this computer
)

// ErrConfig matches every ConfigError, whatever setting it names.
var ErrConfig = errors.New("invalid configuration")

// Causes of a ConfigError.
var (
	ErrMissing    = errors.New("is required")
	ErrMalformed  = errors.New("is malformed")
	ErrUnreadable = errors.New("cannot be read")
	ErrConflict   = errors.New("cannot be used with")
)

// ConfigError names the setting that is wrong and why. errors.Is(err, ErrConfig) is true for every one.
type ConfigError struct {
	Setting string // the flag name, such as "bootstrap"
	Err     error
}

func (e *ConfigError) Error() string { return fmt.Sprintf("kolo-meet: -%s %v", e.Setting, e.Err) }
func (e *ConfigError) Unwrap() error { return e.Err }
func (e *ConfigError) Is(target error) bool {
	return target == ErrConfig
}

// Config is everything the app needs from outside. A zero field takes its default, if it has one.
// LoadConfig fills it from flags and environment variables.
type Config struct {
	DataDir         string        // -data, KOLO_MEET_DATA. Default: <user config dir>/kolo-meet
	Bootstrap       string        // -bootstrap, KOLO_BOOTSTRAP: entry nodes of your own network, a comma list of host:port. Default: the public network
	Local           bool          // -local, KOLO_MEET_LOCAL=1: use this computer only, an entry node on 127.0.0.1. Not with -bootstrap or -first
	First           string        // -first, KOLO_MEET_FIRST: run the first entry node of a local test network on this loopback IP:PORT
	Listen          string        // -listen, KOLO_MEET_LISTEN. Default 127.0.0.1:0 on a loopback network, else 0.0.0.0:0
	MaxParticipants int           // -max-participants, KOLO_MEET_MAX_PARTICIPANTS: people in one call, you included. Default 8, at most 20
	FindTimeout     time.Duration // -find-timeout, KOLO_MEET_FIND_TIMEOUT: how long a join looks for the host. Default 1m, 195s with connection relays
	LookupInterval  time.Duration // -lookup-interval, KOLO_MEET_LOOKUP_INTERVAL: time between lookups of a topic. Default 5s
	// Relays and RelayFiles are the connection relays: -relay (repeatable, a comma list of host:port@key) and
	// -relay-file (repeatable), or KOLO_MEET_RELAYS when neither flag is given.
	Relays     []string
	RelayFiles []string
	// VideoSize is -video-size, KOLO_MEET_VIDEO_SIZE: the ceiling of the picture, WxH, at most 1920x1080, and the size the
	// camera is asked for; the ladder sends less when the path or the processor needs it, and never more than the camera
	// gives. Default 1920x1080.
	// VideoBitrate is -video-bitrate, KOLO_MEET_VIDEO_BITRATE: VP8 bits a second at that size. Default: kolo-video's
	// rule, 0.07 bits per pixel per frame, at most 3.8 Mbit/s.
	VideoSize    string
	VideoBitrate int
	// Congestion is -congestion, KOLO_MEET_CONGESTION: how the connections that carry a call pace what they send, "reno",
	// "cubic" or "bbr". Default reno.
	Congestion string
	// QualityReport is -quality-report, KOLO_MEET_QUALITY: "on" writes the call quality report to the data directory, "off"
	// does not. Default on. QualityLog is -quality-log, KOLO_MEET_QUALITY_LOG=1: the same numbers also go to the log.
	QualityReport string
	QualityLog    bool
	// DisplayFPS is -display-fps, KOLO_MEET_DISPLAY_FPS: the pictures a second each tile of the window draws, "auto",
	// "30" or "15". Default auto.
	DisplayFPS string

	source        entrySource // the entry node of -local; nil: loopbackSource(defaultLocalPort)
	bootstrapFrom string      // "flag", "env" or "" (not given, or set in code)
	relaysFrom    string      // "flag", "env" or "" (not given, or set in code)
	explicitZero  string      // the first size setting given as 0, which has no meaning
}

// LoadConfig reads the flags in args. A setting that is not given as a flag comes from its environment variable
// (read with getenv, such as os.Getenv), and a flag wins over its variable.
func LoadConfig(args []string, getenv func(string) string, stderr io.Writer) (Config, error) {
	var c Config
	fs := flag.NewFlagSet("kolo-meet", flag.ContinueOnError)
	fs.SetOutput(stderr)
	text := func(p *string) func(string) error {
		return func(v string) error { *p = v; return nil }
	}
	dur := func(p *time.Duration) func(string) error {
		return func(v string) (err error) { *p, err = time.ParseDuration(v); return err }
	}
	count := func(name string, p *int) func(string) error {
		return func(v string) (err error) {
			if *p, err = strconv.Atoi(v); err == nil && *p == 0 && c.explicitZero == "" {
				c.explicitZero = name
			}
			return err
		}
	}
	fs.BoolVar(&c.Local, "local", false, "use this computer only: the first window runs an entry node on 127.0.0.1 and the others join it (KOLO_MEET_LOCAL=1)")
	if v := getenv("KOLO_MEET_LOCAL"); v != "" {
		on, err := strconv.ParseBool(v)
		if err != nil {
			return c, &ConfigError{Setting: "local", Err: fmt.Errorf("%w: KOLO_MEET_LOCAL: want 1 or 0", ErrMalformed)}
		}
		c.Local = on
	}
	fs.BoolVar(&c.QualityLog, "quality-log", false, "also write the call quality report to the log, a line starting quality: every 5 s (KOLO_MEET_QUALITY_LOG=1)")
	if v := getenv("KOLO_MEET_QUALITY_LOG"); v != "" {
		on, err := strconv.ParseBool(v)
		if err != nil {
			return c, &ConfigError{Setting: "quality-log", Err: fmt.Errorf("%w: KOLO_MEET_QUALITY_LOG: want 1 or 0", ErrMalformed)}
		}
		c.QualityLog = on
	}
	for _, s := range []struct {
		name, env, help string
		set             func(string) error
	}{
		{"data", "KOLO_MEET_DATA", "data directory (default <user config dir>/kolo-meet)", text(&c.DataDir)},
		{"bootstrap", "KOLO_BOOTSTRAP", "entry nodes of your own network, a comma list of host:port (default: the public network)", text(&c.Bootstrap)},
		{"first", "KOLO_MEET_FIRST", "run the first entry node of a local test network on this loopback IP:PORT, such as 127.0.0.1:4000", text(&c.First)},
		{"listen", "KOLO_MEET_LISTEN", "local IP:PORT (default 127.0.0.1:0 on a loopback network, else 0.0.0.0:0)", text(&c.Listen)},
		{"max-participants", "KOLO_MEET_MAX_PARTICIPANTS", "people in one call, you included (default 8, at most 20)", count("max-participants", &c.MaxParticipants)},
		{"find-timeout", "KOLO_MEET_FIND_TIMEOUT", "how long a join looks for the host (default 1m, 195s with connection relays)", dur(&c.FindTimeout)},
		{"lookup-interval", "KOLO_MEET_LOOKUP_INTERVAL", "time between lookups of a topic (default 5s)", dur(&c.LookupInterval)},
		{"video-size", "KOLO_MEET_VIDEO_SIZE", "ceiling of the picture and the camera size asked for, WxH, at most 1920x1080; the ladder sends less when the path, the processor or the call needs it (default 1920x1080)", text(&c.VideoSize)},
		{"video-bitrate", "KOLO_MEET_VIDEO_BITRATE", "VP8 bits a second at -video-size, scaled with the pixels for each rung below it (default kolo-video's 0.07 bits per pixel per frame, at most 3800000: 3800000 at 1920x1080, 1935360 at 1280x720, 483840 at 640x360)", count("video-bitrate", &c.VideoBitrate)},
		{"congestion", "KOLO_MEET_CONGESTION", "how the connections of a call pace what they send: reno, cubic or bbr; calls send less than the path carries, and bbr holds a standing queue of 25 to 150 ms, so reno is the default", text(&c.Congestion)},
		{"display-fps", "KOLO_MEET_DISPLAY_FPS", "pictures a second each tile draws: auto, 30 or 15; auto draws 30 up to 4 other people with video and 15 with more, and falls back to 15 when drawing takes over half a processor core; 30 and 15 are fixed and 30 has no fallback (default auto)", text(&c.DisplayFPS)},
		{"quality-report", "KOLO_MEET_QUALITY", "on writes the call quality report, kolo-meet-quality.jsonl, to the data directory; off does not (default on)", text(&c.QualityReport)},
	} {
		if v := getenv(s.env); v != "" {
			if err := s.set(v); err != nil {
				return c, &ConfigError{Setting: s.name, Err: fmt.Errorf("%w: %s: %w", ErrMalformed, s.env, err)}
			}
			if s.name == "bootstrap" {
				c.bootstrapFrom = "env"
			}
		}
		set := s.set
		if s.name == "bootstrap" {
			set = func(v string) error { c.bootstrapFrom = "flag"; return s.set(v) }
		}
		fs.Func(s.name, s.help+" ("+s.env+")", set)
	}
	fs.Func("relay", "connection relay, host:port@key; repeat it or give a comma list (KOLO_MEET_RELAYS when no -relay or -relay-file is given)",
		func(v string) error { c.Relays = append(c.Relays, v); c.relaysFrom = "flag"; return nil })
	fs.Func("relay-file", "file of connection relays, one host:port@key per line; repeatable",
		func(v string) error { c.RelayFiles = append(c.RelayFiles, v); c.relaysFrom = "flag"; return nil })
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if v := getenv("KOLO_MEET_RELAYS"); v != "" && c.relaysFrom == "" {
		c.Relays, c.relaysFrom = []string{v}, "env"
	}
	return c, nil
}

// entrySource gives the entry node of -local, and the one this app may run itself when the port is free. The
// startup tries to run host and joins the node when it cannot.
type entrySource func() (nodes []string, host netip.AddrPort)

// loopbackSource is -local: one entry node on 127.0.0.1:port, run by the first window of this computer that takes the
// port. Windows on other computers cannot reach it.
func loopbackSource(port uint16) entrySource {
	return func() ([]string, netip.AddrPort) {
		host := netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), port)
		return []string{host.String()}, host
	}
}

// settings is a Config after defaults and checks: values the code can use as they are.
type settings struct {
	Config
	first      netip.AddrPort    // valid when this app runs the first entry node
	local      netip.AddrPort    // valid with -local: the entry node of this computer
	mode       string            // modePublic, modeOwn or modeLocal
	nodes      []string          // entry nodes of an own or local network; empty on the public one, whose list is kolo-dht's
	nodesText  string            // nodes as a comma list; "" on the public network
	nodesFrom  string            // "public", "flag", "env", "first" or "local"
	relays     []koloswarm.Relay // connection relays, from ParseRelays and LoadRelays
	relaysText string            // relays as a comma list of host:port@key
	relaysFrom string            // "flag", "file", "flag-and-file", "env" or "none"
	video      videoMode         // from -video-size and -video-bitrate; zero when neither is given
}

// entryNodeCount is how many entry nodes the network starts from: the public list on the public network.
func (s settings) entryNodeCount() int {
	if s.mode == modePublic {
		return len(kolodht.PublicServers())
	}
	return len(s.nodes)
}

// resolve applies the defaults and checks every setting. It returns a *ConfigError for the first bad setting.
func (c Config) resolve() (settings, error) {
	s := settings{Config: c}
	if s.DataDir == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return s, &ConfigError{Setting: "data", Err: fmt.Errorf("%w: no user config directory: %w", ErrMissing, err)}
		}
		s.DataDir = filepath.Join(dir, "kolo-meet")
	}
	if c.explicitZero != "" {
		return s, &ConfigError{Setting: c.explicitZero, Err: fmt.Errorf("%w: must be positive; leave it out for the default", ErrMalformed)}
	}
	if s.MaxParticipants == 0 {
		s.MaxParticipants = defaultMaxParticipants
	}
	if s.MaxParticipants < 2 || s.MaxParticipants > maxParticipantsCeiling {
		return s, &ConfigError{Setting: "max-participants", Err: fmt.Errorf("%w: from 2 to %d", ErrMalformed, maxParticipantsCeiling)}
	}
	if s.Congestion == "" {
		s.Congestion = congestionReno
	}
	if !validCongestion(s.Congestion) {
		return s, &ConfigError{Setting: "congestion", Err: fmt.Errorf("%w: want reno, cubic or bbr", ErrMalformed)}
	}
	if s.DisplayFPS == "" {
		s.DisplayFPS = displayAuto
	}
	if s.DisplayFPS != displayAuto && s.DisplayFPS != display30 && s.DisplayFPS != display15 {
		return s, &ConfigError{Setting: "display-fps", Err: fmt.Errorf("%w: want auto, 30 or 15", ErrMalformed)}
	}
	if s.QualityReport == "" {
		s.QualityReport = qualityOn
	}
	if s.QualityReport != qualityOn && s.QualityReport != qualityOff {
		return s, &ConfigError{Setting: "quality-report", Err: fmt.Errorf("%w: want on or off", ErrMalformed)}
	}
	if err := s.resolveRelays(); err != nil {
		return s, err
	}
	if err := s.resolveVideo(); err != nil {
		return s, err
	}
	if s.FindTimeout == 0 {
		s.FindTimeout = defaultFindTimeout
		if len(s.relays) > 0 {
			s.FindTimeout = relayFindTimeout
		}
	}
	if s.FindTimeout < time.Second {
		return s, &ConfigError{Setting: "find-timeout", Err: fmt.Errorf("%w: at least 1s", ErrMalformed)}
	}
	if s.LookupInterval == 0 {
		s.LookupInterval = defaultLookupInterval
	}
	if s.LookupInterval < time.Second || s.LookupInterval > time.Hour {
		return s, &ConfigError{Setting: "lookup-interval", Err: fmt.Errorf("%w: from 1s to 1h", ErrMalformed)}
	}
	if c.Local && (c.Bootstrap != "" || c.First != "") {
		with := "-bootstrap"
		switch {
		case c.First != "" && c.Bootstrap != "":
			with = "-bootstrap or -first"
		case c.First != "":
			with = "-first"
		case c.bootstrapFrom == "env":
			with = "KOLO_BOOTSTRAP"
		}
		return s, &ConfigError{Setting: "local", Err: fmt.Errorf("%w %s: choose one network, the public one by default, -bootstrap for your own or -local for this computer only", ErrConflict, with)}
	}
	if s.First != "" {
		a, err := netip.ParseAddrPort(s.First)
		if err != nil || !a.Addr().Is4() || !a.Addr().IsLoopback() || a.Port() == 0 {
			return s, &ConfigError{Setting: "first", Err: fmt.Errorf("%w: want a loopback IPv4 IP:PORT such as 127.0.0.1:4000, for local testing only", ErrMalformed)}
		}
		s.first = a
	}

	nodes, err := parseEntryNodes(c.Bootstrap)
	if err != nil {
		return s, &ConfigError{Setting: "bootstrap", Err: err}
	}
	s.nodes = nodes
	if s.first.IsValid() && !slices.Contains(nodes, s.first.String()) {
		s.nodes = append([]string{s.first.String()}, nodes...)
	}
	switch {
	case c.Local:
		src := c.source
		if src == nil {
			src = loopbackSource(defaultLocalPort)
		}
		s.nodes, s.local = src()
		s.mode, s.nodesFrom = modeLocal, "local"
	case len(s.nodes) > 0:
		s.mode, s.nodesFrom = modeOwn, c.bootstrapFrom
		if len(nodes) == 0 {
			s.nodesFrom = "first"
		} else if s.nodesFrom == "" {
			s.nodesFrom = "flag" // set in code: the same as a flag
		}
	default:
		s.mode, s.nodesFrom = modePublic, "public"
	}
	s.nodesText = strings.Join(s.nodes, ",")

	if s.Listen == "" {
		s.Listen = publicListen
		if loopbackOnly(s.nodes) {
			s.Listen = localListen
		}
	}
	if a, err := netip.ParseAddrPort(s.Listen); err != nil || !a.Addr().Is4() {
		return s, &ConfigError{Setting: "listen", Err: fmt.Errorf("%w: want an IPv4 IP:PORT such as 0.0.0.0:0", ErrMalformed)}
	}
	return s, nil
}

// resolveVideo reads -video-size and -video-bitrate. The size is at most the largest picture a receiver takes
// (maxFrameWidth x maxFrameHeight); the bitrate fits libvpx's kilobits in a uint32 (kolo-video vpx/config.go:95).
func (s *settings) resolveVideo() error {
	if s.VideoSize == "" && s.VideoBitrate == 0 {
		return nil
	}
	s.video = defaultVideo
	if s.VideoSize != "" {
		ws, hs, ok := strings.Cut(s.VideoSize, "x")
		w, werr := strconv.Atoi(ws)
		h, herr := strconv.Atoi(hs)
		if !ok || werr != nil || herr != nil || w < 1 || h < 1 || w > maxFrameWidth || h > maxFrameHeight {
			return &ConfigError{Setting: "video-size", Err: fmt.Errorf("%w: want WxH from 1x1 to %dx%d, such as 1280x720", ErrMalformed, maxFrameWidth, maxFrameHeight)}
		}
		s.video.w, s.video.h = w, h
	}
	if s.VideoBitrate < 0 || uint64(s.VideoBitrate)/1000 > math.MaxUint32 {
		return &ConfigError{Setting: "video-bitrate", Err: fmt.Errorf("%w: from 1 to %d bits a second", ErrMalformed, uint64(math.MaxUint32)*1000+999)}
	}
	s.video.bitrate = s.VideoBitrate
	return nil
}

// resolveRelays reads the connection relays: the flags and files, else KOLO_MEET_RELAYS.
func (s *settings) resolveRelays() error {
	s.relaysFrom = s.Config.relaysFrom
	if s.relaysFrom != "env" {
		s.relaysFrom = "flag" // set in code: the same as a flag
		switch {
		case len(s.Relays) == 0 && len(s.RelayFiles) > 0:
			s.relaysFrom = "file"
		case len(s.Relays) > 0 && len(s.RelayFiles) > 0:
			s.relaysFrom = "flag-and-file"
		}
	}
	parts := slices.Clone(s.Relays)
	where := ""
	if s.relaysFrom == "env" {
		where = "KOLO_MEET_RELAYS: "
	}
	for _, path := range s.RelayFiles {
		rs, err := koloswarm.LoadRelays(path)
		var pe *fs.PathError
		switch {
		case errors.As(err, &pe): // missing or unreadable, not malformed
			return &ConfigError{Setting: "relay-file", Err: fmt.Errorf("%w: %w", ErrUnreadable, pe)}
		case err != nil:
			return &ConfigError{Setting: "relay-file", Err: plainSwarmErr(err)}
		}
		parts = append(parts, relaysText(rs))
	}
	relays, err := parseRelayList(strings.Join(parts, ","))
	if err != nil {
		if where != "" {
			err = fmt.Errorf("%w: %s%s", ErrMalformed, where, strings.TrimPrefix(err.Error(), ErrMalformed.Error()+": "))
		}
		return &ConfigError{Setting: "relay", Err: err}
	}
	s.relays, s.relaysText = relays, relaysText(relays)
	if len(relays) == 0 {
		s.relaysFrom = "none"
	}
	return nil
}

// parseRelayList parses a comma list with kolo-swarm's ParseRelays and turns its error into a sentence.
func parseRelayList(text string) ([]koloswarm.Relay, error) {
	relays, err := koloswarm.ParseRelays(text)
	if err != nil {
		return nil, plainSwarmErr(err)
	}
	return relays, nil
}

// relaysText writes relays in the form ParseRelays reads.
func relaysText(relays []koloswarm.Relay) string {
	out := make([]string, len(relays))
	for i, r := range relays {
		out[i] = r.Addr + "@" + hex.EncodeToString(r.PublicKey)
	}
	return strings.Join(out, ",")
}

// plainSwarmErr is a kolo-swarm parse error without its prefixes, as ErrMalformed.
func plainSwarmErr(err error) error {
	msg := strings.TrimPrefix(err.Error(), "koloswarm: ")
	msg = strings.TrimPrefix(msg, "invalid argument: ")
	return fmt.Errorf("%w: %s", ErrMalformed, msg)
}

// parseEntryNodes parses a comma list with kolo-swarm's ParseNodes and turns its error into a sentence.
func parseEntryNodes(s string) ([]string, error) {
	nodes, err := koloswarm.ParseNodes(s)
	if err != nil {
		return nil, plainSwarmErr(err)
	}
	return nodes, nil
}

// loopbackOnly reports whether every entry node is a loopback IPv4 literal, so the app can stay on 127.0.0.1.
func loopbackOnly(nodes []string) bool {
	if len(nodes) == 0 {
		return false
	}
	for _, n := range nodes {
		a, err := netip.ParseAddrPort(n)
		if err != nil || !a.Addr().IsLoopback() {
			return false
		}
	}
	return true
}
