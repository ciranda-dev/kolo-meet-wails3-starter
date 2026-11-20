# Security

The finished app's security notes, with the full text of the call's protections, are in `docs/security.md` of kolo-meet-wails3-example. What the starter itself does:

## Memory

A peer on a connection makes the app hold memory: kolo-transport keeps about 96 heap bytes for each byte of an open stream, so one connection that opens 16 streams (its default) can hold up to about 856 MiB, and one that opens 1000 up to about 47 GiB. The limit of a connection is fixed when it is made, so the host's app serves a stranger and a person in the call on two network nodes: the invite node, which anyone with the invite can reach, keeps 16 incoming streams (`inviteStreams`); the relay node admits only the keys the host let in (`relayAllow`) and takes 1000 (`maxStreams`), which every subscription and group of a call needs. `transportConfig`, `inviteTransport`, `openSwarm`, `relayAllow` and `inviteAllow` are in `host.go`; the guide's `startHost` of step 3 opens both swarms, and its invite is served under a key of its own, which is why the grant (`parseGrant`) no longer requires the relay's key to be the one that served the invite. The finished example's `docs/security.md` has the table of numbers.

At start the app sets a soft memory limit of 2 GiB (`debug.SetMemoryLimit`) unless the environment sets `GOMEMLIMIT`, which then decides. The limit makes the garbage collector work harder as the heap nears it; it does not stop an allocation, so it does not replace the limits of the libraries. The app logs one line at start when it chose the limit. The default is never below 512 MiB, the least a call needs.

## Files

The identity is `identity.key` in the data directory (kolo-id writes it with mode 0600 and flushes the file and the directory). The call history and the settings are replaced atomically: a temporary file is written, flushed to disk, renamed over the old one and the directory is flushed, so after a power loss the file holds the old content or the new one, never a part.

## The invite

The invite is a bearer secret: whoever has it can ask to join. When the app copies it to the clipboard, it empties the clipboard at the end of the call if it still holds that invite; a clipboard the person changed is left alone. The pasted text of Join with an invite is bounded to 1024 bytes before it is read.

## The call quality report

The report is on by default, so that a test on a real network leaves data, and the app logs one line at each start that says where it goes and how to stop it (`quality.go`, `qualityWriter`). Each call adds lines to `kolo-meet-quality.jsonl` in the data directory (mode 0600; at most 2 MiB, then the file becomes `kolo-meet-quality.jsonl.1`, replacing the one there). The starter writes nothing until the guide's code runs: `enter` (step 3) starts the line for what you send, and `watchLoop` (step 4) the line for each other person. A line holds counts, sizes and times: audio frames decoded, missed and made up, pictures decoded and shown, freezes, bytes, the jitter buffer's delay, decode and draw times, the picture size and bit rate sent and why it changed, and what kolo-transport measured on the connections to the relay (round-trip time, loss, the congestion window, the controller). The other people are numbered from 1 in the order they were first reported. It holds no name, key, invite, address, file name or media. It is still a durable record of when you were in calls and how many people were in them. `-quality-report off` (or `KOLO_MEET_QUALITY=off`) writes nothing; delete the file and its `.1` copy to remove what is there. `-quality-log` (or `KOLO_MEET_QUALITY_LOG=1`) also prints the same lines to the log. The finished example's `docs/security.md` and `docs/how-it-works.md` list every field.

## Reporting a problem

Report a security issue as [SECURITY.md](../SECURITY.md) says.
