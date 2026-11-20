# Build and test

Only Wails needs cgo, on macOS and Linux, for the native window. The audio, video and network libraries are pure Go
and need no codec, no sound library and no compiler. The repository has no `go.work` and no `replace`: every Kolo
library comes from a published version. Inside a Go workspace, set `GOWORK=off`.

## Checks

```sh
go vet ./...
go test ./...                    # the starter's tests: made-up devices, 127.0.0.1, temporary folders
go vet -tags finished .
staticcheck ./...                # staticcheck.conf leaves out unused code: the stubs leave some until their step is written
go test -tags finished .         # the finished app's tests; they pass once every step of the guide is written
```

No test opens a real camera, microphone or speaker. `TestStartACallSaysStep3` skips itself once step 3 is written. The three networks (public by default, `-bootstrap`, `-local`) are tested without the tag by their settings and by two `-local` windows; no test sends a packet to a public address.

## Builds

- `wails3 build` builds `bin/kolo-meet` for this computer. The first build takes a few minutes: the video codec is a
  large Go package.
- A Linux build needs cgo with GTK 4.10 or later and WebKitGTK 6.0, for example Ubuntu 24.04 with `libgtk-4-dev` and
  `libwebkitgtk-6.0-dev`; `wails3 doctor` lists what is missing.

## Version

The version the app shows (Settings, `Info().Version`) is the `version` variable of `version.go`. A build sets it from the git tag: the Taskfile builds `-X main.version=<tag>` into the app, and it says `dev` for a development build and for a build with no tag. Set `KOLO_VERSION` to name a version yourself. The packaging files in `build/` (`config.yml`, both `Info.plist` files, the deb, the Windows version info, manifests and MSIX files, and the NSIS defaults) keep the placeholder `0.0.0`; `scripts/stamp-version.sh <version> [dir]` writes a version into a copy of them before you package. `scripts/release-local.sh [output dir]` builds the release files of the committed HEAD on a Mac (needs Go, Node.js, the `wails3` CLI and Docker; unsigned, not notarized) and runs `stamp-version.sh` on its copy. When you make your own app from this starter, change the names in `build/config.yml` and your own tag becomes the version.
