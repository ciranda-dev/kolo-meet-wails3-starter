# Kolo Meet, Wails 3 starter

[![status: beta](https://img.shields.io/badge/status-beta-orange)](CHANGELOG.md)
[![license: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
[![go: 1.26+](https://img.shields.io/badge/go-1.26%2B-00ADD8)](go.mod)
[![wails: 3](https://img.shields.io/badge/wails-3-DF0000)](go.mod)

The starting point of the "Build a video call app" guide in the [Kolo docs](https://docs.kolo.ciranda.dev/build/video-call/). The window of Kolo Meet and most of its Go service are done; the parts that use Kolo are stubs that the guide fills in, one step at a time. The finished app is the kolo-meet-wails3-example repository.

## Run it

You need Go 1.26 or later, Node.js with npm, and the Wails 3 CLI (`go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0`). Every Kolo library comes from a published version (among them kolo-media v0.1.4, kolo-moq v0.1.5, kolo-audio v0.1.6, kolo-video v0.1.2 and kolo-swarm v0.1.7), so `GOWORK=off` builds work.

```sh
GOWORK=off wails3 build                 # build bin/kolo-meet
GOWORK=off go test ./...                # the starter's tests: made-up devices, 127.0.0.1 only
```

The camera and the microphone are made up by `starter.go` until step 2, so nothing real is opened. Start a call says which step of the guide writes that part. When the guide is done, `GOWORK=off go test -tags finished .` runs the finished app's tests on your code.

## Try it

Each window needs its own data folder; the app creates it. Calls work once step 4 of the guide is written, and the two windows see each other after step 5.

The app has three networks, chosen with flags. With none, it uses the public network: the entry nodes Kolo ships, run by Ciranda, free, with no uptime guarantee while Kolo is a beta. `-bootstrap HOST:PORT[,HOST:PORT]` (or `KOLO_BOOTSTRAP`) is a network of your own, used alone. `-local` (or `KOLO_MEET_LOCAL=1`) is this computer only: the first window runs an entry node on `127.0.0.1:7451` and the others join it. `-local` cannot be used with `-bootstrap` or `-first`. If the public network cannot be reached, the window says so and names the three options; it never switches network by itself.

The guide's test network on one computer uses `-first`, which runs the first entry node on a loopback address you choose, and `-bootstrap` for the others. Terminal 1: the host.

```sh
./bin/kolo-meet -data /tmp/kolo-meet-demo/host -first 127.0.0.1:4000
```

Terminal 2: the guest.

```sh
./bin/kolo-meet -data /tmp/kolo-meet-demo/guest -bootstrap 127.0.0.1:4000
```

The host clicks Start a call and copies the invite; the guest clicks Join with an invite and pastes it. With `-local` on both windows instead, no address is needed. Two computers on different networks need no flag at all (the public network); a network of your own uses `-bootstrap`, and connection relays, needed only behind strict NATs and run by you, use `-relay` (see the example's "Try it").

The window draws each person at 30 pictures a second in a call of up to 5 people and 15 with more; `-display-fps 30` or `15` fixes the rate. While a call runs the app writes a call quality report, numbers about the call and no name, key, invite, address or media, to `kolo-meet-quality.jsonl` in the data directory; `-quality-report off` (or `KOLO_MEET_QUALITY=off`) turns it off and `-quality-log` also prints it ([what it holds](docs/security.md#the-call-quality-report)). Calls use Reno; `-congestion bbr` is there to compare on a lossy path.

## How it uses Kolo

| Kolo library | What you write with it | Step |
|---|---|---|
| [kolo-video](https://github.com/ciranda-dev/kolo-video), [kolo-audio](https://github.com/ciranda-dev/kolo-audio) | listing and opening cameras, microphones and speakers, the microphone permission; VP8 and Opus | 2, 3, 4 |
| [kolo-moq](https://github.com/ciranda-dev/kolo-moq) | the relay the host runs, the sessions, the keys (`secobj`) | 3, 5 |
| [kolo-invite](https://github.com/ciranda-dev/kolo-invite) | the invite, and joining with it | 3, 4 |
| [kolo-media](https://github.com/ciranda-dev/kolo-media) | your protected broadcast, and watching the others | 3, 4 |
| [kolo-swarm](https://github.com/ciranda-dev/kolo-swarm), [kolo-dht](https://github.com/ciranda-dev/kolo-dht), [kolo-secure](https://github.com/ciranda-dev/kolo-secure) | the relay's network node and the host's own session; the network (public, your own or this computer) is chosen in `config.go` and is done | 3 |
| [kolo-relay](https://github.com/ciranda-dev/kolo-relay) | nothing: the connection relays (`-relay`) and their key check are done | |

## Project layout

```text
starter.go, starter_test.go   only in the starter: made-up devices and ErrNotInStarter; deleted at the end
hw.go                         step 2: cameras, microphones, speakers
host.go, publisher.go         step 3: the relay, the invite, your audio track
guest.go, remote.go           step 4: joining, decoding what the others send
member.go                     steps 3 to 5: entering the call, watching, the group keys
main.go, meetservice.go, ...  done: the window, the network (public, your own, this computer), connection relays, the call's states, notch and tray
adapt.go, ladder.go           done: the camera's quality ladder (360p, 720p, the ceiling) that follows the connection to the relay
reconnect.go, watchdog.go     done: a new session to the relay when the old one ends or the media stops arriving
quality.go, tiles.go          done: the call quality report; the size and rate of the pictures given to the window
conceal.go                    done: audio that arrives late or not at all is made up or played faster
frontend/src/                 done: the window (React)
```

## Learn more

- [Each stub, its step and its finished file](docs/solutions.md), [build and test](docs/build-and-test.md), [security notes](docs/security.md)
- [Changelog](CHANGELOG.md), [security policy](SECURITY.md), [license](LICENSE) (Apache-2.0)
