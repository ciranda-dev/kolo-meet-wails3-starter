# Stubs and solutions

Every part the guide writes is already in the starter as a stub with the same name and signature. A stub that
returns an error returns `ErrNotInStarter`, wrapped with its step: the window shows "not implemented in the starter
(step N of the guide)". The others do nothing, so the app always builds and starts. Until step 2, `realHardware`
uses the made-up devices of `starter.go`.

The finished code of each stub is the same function in the kolo-meet-wails3-example repository, in the file of the
same name. When step 5 is done, delete `starter.go` and `starter_test.go`: your Go files are then the example's.

| Step of the guide | File | Stub | Until it is written |
|---|---|---|---|
| 2. Devices and the lobby preview | `hw.go` | the import block, `realHardware` and its methods (`Cameras`, `Microphones`, `Speakers`, `MicrophoneAccess`, `RequestMicrophone`, `RequestCamera`, `OpenCamera`, `OpenMicrophone`, `OpenSpeaker`), with `list` and `audioDevices` | the made-up devices of `starter.go` |
| 2 | `hw.go` | `deviceError` | every device error is "Could not use the ..." |
| 3. Start a call | `host.go` | the import block, `startHost`, `newInvite` | Start call ends with step 3 |
| 3 | `member.go` | the import block, `enter` | step 3 |
| 3 | `publisher.go` | `setAudio`, `onAudio` and the constant `audioResync` before it (the guide's block brings it) | step 3; no audio track |
| 4. Join a call and watch the others | `guest.go` | the import block, `joinHost` | Join call ends with step 4 |
| 4 | `member.go` | `watchLoop` | nobody plays |
| 4 | `remote.go` | the import block, `playAudio`, `playVideo` | step 4 |
| 5. Group keys | `member.go` | `keyOwner` | every key is refused |
| 5 | `member.go` | `updateMembers` | nobody gets your key |
| 5 | `member.go` | `startLink` | you fetch nobody's key |

`relayAuthorize`, `pairAuthorize`, `attach`, `superviseRelay`, `adaptLoop`, `watchdogLoop`, `serveConn`, `session`, `relayLoop`, `inviteSwarmLoop`, `openSwarm`, `relayAllow`, `inviteAllow`, `inviteLoop`, `rosterLoop`, `readProfile`, `publishProfiles`,
`onVideo`, the connection relays (`connrelay.go`), removing a participant and the rest of the service are done in
the starter, as in the example. So are the three networks (public, `-bootstrap`, `-local`) and `bootstrapFailure`, which names the network that did not answer: the finished `startHost` returns it when the call relay's network node cannot bootstrap.
