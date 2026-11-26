# Releasing the Android app

The APK is distributed from GitHub Releases, not Google Play.

## The signing key (once)

Android only installs an update signed with the same key as the app already
on the phone. **If this key is lost, nobody can update**: they have to
uninstall, which removes their drives and syncs. Keep a copy somewhere safe
that isn't this repository.

1. Create it (you'll be asked for a password; use the same one for the key):

   ```bash
   keytool -genkeypair -v -keystore enter-space-light.jks -alias esl -keyalg RSA -keysize 4096 -validity 12000 -dname "CN=Enter Space Light"
   ```

   The certificate name is public: anyone can read it from the APK. Don't
   put a personal name or address in it.

2. Add two repository secrets (GitHub → Settings → Secrets and variables →
   Actions): `ANDROID_KEYSTORE_BASE64` (`base64 -i enter-space-light.jks`) and
   `ANDROID_KEYSTORE_PASSWORD`.

To sign a build on your own machine instead, create
`android/keystore.properties` (ignored by git) with `storeFile`,
`storePassword`, `keyAlias=esl` and `keyPassword`, then run
`scripts/build-android.sh <version> release`.

## Google's developer verification

Google is introducing identity verification for Android developers, including
apps installed outside Google Play. As of November 2026 it applies to apps from
participating stores in Brazil, Indonesia, Singapore and Thailand, not to APKs
downloaded directly, and Google plans a worldwide expansion in 2027. Check the
[current rules](https://developer.android.com/developer-verification) before
relying on this.

## Checklist

1. `node ../../_shared/soren/soren.mjs run "Enter Space Light"` is green
   (the `android` suite runs the app on an emulator).
2. Date the CHANGELOG section, push, wait for CI, then push the `v*` tag.
