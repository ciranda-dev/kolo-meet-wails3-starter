#!/usr/bin/env bash
# Copyright (c) 2018-Present Lea Anthony
# SPDX-License-Identifier: MIT

# Fail script on any error
set -euxo pipefail

# linuxdeploy is pinned to one release and checked against its SHA-256 before it runs, so a changed or replaced
# download stops the build. To update, take the new release's checksums from its assets and change all three lines.
LINUXDEPLOY_RELEASE="1-alpha-20251107-1"
LINUXDEPLOY_SHA256_X86_64="c20cd71e3a4e3b80c3483cef793cda3f4e990aca14014d23c544ca3ce1270b4d"
LINUXDEPLOY_SHA256_AARCH64="620095110d693282b8ebeb244a95b5e911cf8f65f76c88b4b47d16ae6346fcff"

# Define variables
APP_DIR="${APP_NAME}.AppDir"

# Create AppDir structure
mkdir -p "${APP_DIR}/usr/bin"
cp -r "${APP_BINARY}" "${APP_DIR}/usr/bin/"
cp "${ICON_PATH}" "${APP_DIR}/"
# ship the whole hicolor set (ICON_PATH is <hicolor>/512x512/apps/<name>.png)
HICOLOR="$(dirname "$(dirname "$(dirname "$(dirname "${ICON_PATH}")")")")"
mkdir -p "${APP_DIR}/usr/share/icons"
cp -r "${HICOLOR}" "${APP_DIR}/usr/share/icons/"
cp "${DESKTOP_FILE}" "${APP_DIR}/"

case "$(uname -m)" in
    x86_64) LINUXDEPLOY_ARCH="x86_64"; LINUXDEPLOY_SHA256="${LINUXDEPLOY_SHA256_X86_64}" ;;
    aarch64|arm64) LINUXDEPLOY_ARCH="aarch64"; LINUXDEPLOY_SHA256="${LINUXDEPLOY_SHA256_AARCH64}" ;;
    *) echo "no pinned linuxdeploy for $(uname -m)" >&2; exit 1 ;;
esac
LINUXDEPLOY="linuxdeploy-${LINUXDEPLOY_ARCH}.AppImage"

# Download linuxdeploy, refuse it unless the checksum matches (set -e stops the script), and make it executable
wget -q -4 -O "${LINUXDEPLOY}" "https://github.com/linuxdeploy/linuxdeploy/releases/download/${LINUXDEPLOY_RELEASE}/${LINUXDEPLOY}"
echo "${LINUXDEPLOY_SHA256}  ${LINUXDEPLOY}" | sha256sum -c -
chmod +x "${LINUXDEPLOY}"

# Run linuxdeploy to bundle the application
"./${LINUXDEPLOY}" --appdir "${APP_DIR}" --output appimage

# Rename the generated AppImage
mv "${APP_NAME}*.AppImage" "${APP_NAME}.AppImage"
