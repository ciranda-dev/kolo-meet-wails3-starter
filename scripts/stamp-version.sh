#!/usr/bin/env bash
# Writes a release version into the packaging files of a copy of the app: build/config.yml, both Info.plist files,
# the deb, the Windows version info, manifests and MSIX files, and the NSIS defaults. The files in git keep the
# placeholder 0.0.0, so only a release copy carries a version.
# Usage: scripts/stamp-version.sh VERSION [DIR]    VERSION is a git tag such as v0.1.0 or v0.1.0-3-gabc1234.
set -euo pipefail

version=${1:?usage: stamp-version.sh VERSION [DIR]}
dir=${2:-$(cd "$(dirname "$0")/.." && pwd)}
full=${version#v}
if [[ $full =~ ^([0-9]+)\.([0-9]+)\.([0-9]+) ]]; then
  core="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}.${BASH_REMATCH[3]}"
else
  core=0.0.0
fi

# sub FILE FROM TO: replaces FROM with TO in FILE, and fails when FROM is not there (a changed template).
sub() {
  local file=$dir/$1
  grep -qF -- "$2" "$file" || { echo "stamp-version: $1 has no $2" >&2; exit 1; }
  sed "s|$2|$3|g" "$file" > "$file.tmp" && mv "$file.tmp" "$file"
}

sub build/config.yml 'version: "0.0.0"' "version: \"$full\""
for plist in build/darwin/Info.plist build/darwin/Info.dev.plist; do
  sub "$plist" '<string>0.0.0</string>' "<string>$core</string>"
done
sub build/linux/nfpm/nfpm.yaml 'version: "0.0.0"' "version: \"$full\""
sub build/windows/info.json '"file_version": "0.0.0"' "\"file_version\": \"$core\""
sub build/windows/info.json '"ProductVersion": "0.0.0"' "\"ProductVersion\": \"$full\""
for manifest in "$dir"/build/windows/*.exe.manifest; do
  sub "${manifest#"$dir"/}" 'version="0.0.0.0"' "version=\"$core.0\""
done
for msix in build/windows/msix/app_manifest.xml build/windows/msix/template.xml; do
  sub "$msix" 'Version="0.0.0.0"' "Version=\"$core.0\""
done
sub build/windows/nsis/wails_tools.nsh '!define INFO_PRODUCTVERSION "0.0.0"' "!define INFO_PRODUCTVERSION \"$core\""
