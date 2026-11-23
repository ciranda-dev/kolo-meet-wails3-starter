# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- A guest accepts any number of connection relays in a grant, not at most 16: the bound is the size of the invite payload that kolo-invite already enforces (1 MiB), and duplicates are checked in linear time. `-relay` and `-relay-file` no longer stop at 16 relays, as kolo-swarm no longer does.
- The default `-find-timeout` with connection relays is 72 s instead of 195 s: kolo-swarm now tries the relays once kolo-dht's hole punch window (12 s) passed, then one relay dial of up to 60 s.
- The video adapts faster and flaps less on a long relayed path. A throughput sample counts only when measured over at least the connection's shortest round trip; the first time the queue fills (the first key frame on a new connection) gives no estimate unless it lasts 3 s; with no estimate yet a cut follows the congestion controller's window over the round trip (for `bbr` the larger of half of it and its bandwidth), on that first run no lower than the usual 80%; the bitrate grows by 8% of itself a second when that is more than 5% of the full rate. Each rung of the ladder keeps its own count of failed tries (a try lasts 20 s from when the rung is sent, was 10 s), forgotten when the rungs change; a step up that fails far below what the rung needs waits the longest hold (120 s); a step up waits for the encoder's next periodic key frame instead of adding one.
- The "media from" line of the log names the sender's microphone and camera state, so a line of zero audio counters is no longer read as audio lost on the way.

## [0.1.0] - 2026-11-20

### Added

- First release of kolo-meet-wails3-starter.
