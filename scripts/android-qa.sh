#!/usr/bin/env bash
# Soren's android suite: build the app, boot the QA emulator, run
# scripts/e2e-android.py, and always shut down an emulator it started.
#
#   AVD=esl_qa scripts/android-qa.sh
# Create the AVD once (Android 15, API 35):
#   avdmanager create avd -n esl_qa -k "system-images;android-35;google_apis;arm64-v8a" -d pixel_6
set -euo pipefail
cd "$(dirname "$0")/.."
: "${ANDROID_HOME:=/opt/homebrew/share/android-commandlinetools}"
export ANDROID_HOME
AVD="${AVD:-esl_qa}"
SERIAL=emulator-5580
if [ -z "${JAVA_HOME:-}" ]; then
  export JAVA_HOME=/opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home
fi
# The shared build queue, where this machine has one.
QUEUE=()
[ -x "$HOME/.local/bin/buildq" ] && QUEUE=("$HOME/.local/bin/buildq" --gradle --)

${QUEUE[@]+"${QUEUE[@]}"} scripts/build-android.sh 0.0.0-qa debug
(cd android && ESL_VERSION=0.0.0-qa ${QUEUE[@]+"${QUEUE[@]}"} ./gradlew --no-daemon --max-workers=4 \
  -Pkotlin.compiler.execution.strategy=in-process -q assembleDebugAndroidTest lintDebug)

started=
if ! adb -s "$SERIAL" get-state >/dev/null 2>&1; then
  "$ANDROID_HOME/emulator/emulator" -avd "$AVD" -port "${SERIAL#emulator-}" -no-window -no-audio -no-boot-anim -no-snapshot -memory 3072 >/dev/null 2>&1 &
  started=$!
fi
cleanup() { [ -z "$started" ] || adb -s "$SERIAL" emu kill >/dev/null 2>&1 || kill "$started" || true; }
trap cleanup EXIT
scripts/e2e-android.py --serial "$SERIAL"
