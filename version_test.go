package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestInfoReportsTheBuildVersion(t *testing.T) {
	if version != "dev" {
		t.Fatalf("a build without -ldflags says %q, want dev", version)
	}
	s := NewMeetService(Config{}, nil)
	if got := s.Info().Version; got != "dev" {
		t.Errorf("Info().Version = %q, want dev", got)
	}
	old := version
	t.Cleanup(func() { version = old })
	version = "9.8.7"
	if got := s.Info().Version; got != "9.8.7" {
		t.Errorf("Info().Version = %q, want 9.8.7", got)
	}
}

// versionFiles are the packaging files that carry the version, each with the pattern its version sits in.
var versionFiles = map[string]*regexp.Regexp{
	"build/config.yml":                   regexp.MustCompile(`(?m)^  version: "([^"]*)"`),
	"build/darwin/Info.plist":            regexp.MustCompile(`<string>(\d+\.\d+\.\d+)</string>\s*<key>CFBundleVersion`),
	"build/darwin/Info.dev.plist":        regexp.MustCompile(`<string>(\d+\.\d+\.\d+)</string>\s*<key>CFBundleVersion`),
	"build/linux/nfpm/nfpm.yaml":         regexp.MustCompile(`(?m)^version: "([^"]*)"`),
	"build/windows/info.json":            regexp.MustCompile(`"file_version": "([^"]*)"`),
	"build/windows/msix/template.xml":    regexp.MustCompile(`\bVersion="([^"]*)"\s+PackageDescription`),
	"build/windows/nsis/wails_tools.nsh": regexp.MustCompile(`INFO_PRODUCTVERSION "([^"]*)"`),
}

func TestPackagingFilesHoldNoVersion(t *testing.T) {
	for file, re := range versionFiles {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		m := re.FindSubmatch(b)
		if m == nil {
			t.Errorf("%s: no version found", file)
		} else if v := string(m[1]); v != "0.0.0" && v != "0.0.0.0" {
			t.Errorf("%s holds the version %s, want the placeholder 0.0.0", file, v)
		}
	}
}

func TestBuildsSetTheVersionFromTheTag(t *testing.T) {
	for _, file := range []string{"build/darwin/Taskfile.yml", "build/linux/Taskfile.yml", "build/windows/Taskfile.yml"} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "-X main.version={{.VERSION}}") {
			t.Errorf("%s does not build the version in", file)
		}
	}
	b, err := os.ReadFile("scripts/release-local.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"KOLO_VERSION=$VERSION", "stamp-version.sh", "-X main.version=", "stash create"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("release-local.sh lacks %q", want)
		}
	}
}

func TestStampVersionWritesTheTag(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	for file := range versionFiles {
		copyTestFile(t, file, filepath.Join(dir, file))
	}
	for _, file := range []string{"build/windows/kolo-meet.exe.manifest", "build/windows/msix/app_manifest.xml"} {
		copyTestFile(t, file, filepath.Join(dir, file))
	}
	if out, err := exec.Command("bash", "scripts/stamp-version.sh", "v1.2.3-4-gabc1234", dir).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	want := map[string]string{
		"build/config.yml":                   "1.2.3-4-gabc1234",
		"build/darwin/Info.plist":            "1.2.3",
		"build/darwin/Info.dev.plist":        "1.2.3",
		"build/linux/nfpm/nfpm.yaml":         "1.2.3-4-gabc1234",
		"build/windows/info.json":            "1.2.3",
		"build/windows/msix/template.xml":    "1.2.3.0",
		"build/windows/nsis/wails_tools.nsh": "1.2.3",
	}
	for file, v := range want {
		b, _ := os.ReadFile(filepath.Join(dir, file))
		if m := versionFiles[file].FindSubmatch(b); m == nil || string(m[1]) != v {
			t.Errorf("%s: version %q, want %q", file, m, v)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "build/windows/msix/app_manifest.xml"))
	if !strings.Contains(string(b), `Version="1.2.3.0"`) {
		t.Error("app_manifest.xml was not stamped")
	}
	b, _ = os.ReadFile(filepath.Join(dir, "build/windows/kolo-meet.exe.manifest"))
	if !strings.Contains(string(b), `name="dev.ciranda.kolomeet" version="1.2.3.0"`) || !strings.Contains(string(b), `version="6.0.0.0"`) {
		t.Errorf("the manifest was stamped wrongly:\n%s", b)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "build/windows/info.json"))
	if !strings.Contains(string(b), `"ProductVersion": "1.2.3-4-gabc1234"`) {
		t.Error("info.json ProductVersion was not stamped")
	}
}

func copyTestFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The release copy has no .git, so the Taskfile takes the version from KOLO_VERSION, and without either says dev. Task
// runs the command with errexit, as bash -e does here.
func TestTaskfileVersionWithoutGit(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	b, err := os.ReadFile("Taskfile.yml")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^  VERSION:\n    sh: '(.*)'$`).FindSubmatch(b)
	if m == nil {
		t.Fatal("the Taskfile has no VERSION command")
	}
	for env, want := range map[string]string{"v1.2.3": "1.2.3", "1.2.3": "1.2.3", "": "dev"} {
		dir := t.TempDir()
		cmd := exec.Command(bash, "-ec", string(m[1]))
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "KOLO_VERSION="+env, "GIT_DIR="+filepath.Join(dir, "none"))
		out, err := cmd.Output()
		if got := strings.TrimSpace(string(out)); err != nil || got != want {
			t.Errorf("VERSION with KOLO_VERSION=%q: %q, %v; want %q", env, got, err, want)
		}
	}
}

// The release script checks each binary for the version with its carries function.
func TestReleaseFindsTheVersionInABinary(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	b, err := os.ReadFile("scripts/release-local.sh")
	if err != nil {
		t.Fatal(err)
	}
	fn := regexp.MustCompile(`(?m)^carries\(\) \{.*\}$`).Find(b)
	if fn == nil {
		t.Fatal("scripts/release-local.sh has no carries function")
	}
	dir := t.TempDir()
	for name, tc := range map[string]struct {
		ver, body string
		want      bool
	}{
		"injected":      {"1.2.3", "\x00\x00go1.26\x00\x001.2.3\x00\x00", true},
		"module only":   {"1.2.3", "github.com/ciranda-dev/kolo-dht\tv1.2.3\th1:abc", false},
		"other version": {"1.2.3", "\x00\x001.2.2\x00", false},
		"dirty":         {"1.2.3-dirty", "\x001.2.3-dirty\x00", true},
	} {
		f := filepath.Join(dir, strings.ReplaceAll(name, " ", "-"))
		if err := os.WriteFile(f, []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		err := exec.Command(bash, "-c", "set -euo pipefail; ver=$1; "+string(fn)+"; carries \"$2\"", "bash", tc.ver, f).Run()
		if got := err == nil; got != tc.want {
			t.Errorf("%s: carries = %v, want %v", name, got, tc.want)
		}
	}
}
