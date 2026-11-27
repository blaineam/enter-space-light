#!/usr/bin/env bash
# Builds the Android app: the Go engine as an .aar (gomobile), then the APK.
#
#   scripts/build-android.sh [VERSION] [debug|release]
#   ABIS=android/arm64,android/amd64 scripts/build-android.sh 0.2.0 release
#
# Needs the Android SDK and NDK (ANDROID_HOME) and a JDK 17 (JAVA_HOME).
# A release build is signed only if android/keystore.properties exists.
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${1:-0.0.0-dev}"
VERSION="${VERSION#v}"
TYPE="${2:-debug}"
ABIS="${ABIS:-android/arm64}"
: "${ANDROID_HOME:=/opt/homebrew/share/android-commandlinetools}"
export ANDROID_HOME
if [ -z "${ANDROID_NDK_HOME:-}" ]; then
  ANDROID_NDK_HOME="$(ls -d "$ANDROID_HOME"/ndk/* | sort -V | tail -1)"
fi
export ANDROID_NDK_HOME
if [ -z "${JAVA_HOME:-}" ] && [ -d /opt/homebrew/opt/openjdk@17 ]; then
  export JAVA_HOME=/opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home
fi
[ -n "${JAVA_HOME:-}" ] && export PATH="$JAVA_HOME/bin:$PATH"

# gomobile at exactly the version go.mod pins, kept out of the user's GOBIN.
TOOLS="$(mktemp -d)"
trap 'rm -rf "$TOOLS"' EXIT
GOBIN="$TOOLS" go install golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind
export PATH="$TOOLS:$PATH"

COMMIT="$(git rev-parse --short HEAD 2>/dev/null || true)"
RCLONE_VERSION="$(go list -m -f '{{.Version}}' github.com/rclone/rclone)"
PKG=github.com/blaineam/enter-space-light
LDFLAGS="-s -w -X $PKG/internal/version.Version=$VERSION -X $PKG/internal/version.Commit=$COMMIT -X github.com/rclone/rclone/fs.Version=$RCLONE_VERSION"

# Start from empty build output. In a folder that a file-sync service watches
# (iCloud Drive), it can gain "name 2.jar" copies that break the next build
# with duplicate classes.
rm -rf android/app/build
mkdir -p android/app/libs
# 16 KB page alignment: required by newer devices, harmless elsewhere.
CGO_LDFLAGS="-Wl,-z,max-page-size=16384" gomobile bind \
  -target="$ABIS" -androidapi 26 -trimpath -ldflags "$LDFLAGS" \
  -javapkg io.github.blaineam.esl -o android/app/libs/engine.aar ./mobile
rm -f android/app/libs/engine-sources.jar

TASK="assemble$(printf '%s' "${TYPE:0:1}" | tr '[:lower:]' '[:upper:]')${TYPE:1}"
(cd android && ESL_VERSION="$VERSION" ./gradlew --no-daemon --max-workers=4 -Pkotlin.compiler.execution.strategy=in-process -q "$TASK")
ls -l android/app/build/outputs/apk/"$TYPE"/*.apk
