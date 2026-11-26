#!/usr/bin/env python3
"""End-to-end test of the Android app on an emulator.

    scripts/e2e-android.py --serial emulator-5580 [--apk-dir android/app/build/outputs/apk]

It serves a WebDAV remote from this machine (the emulator reaches it at
10.0.2.2), then checks:
  1. the Files integration, with the instrumented DrivesProviderTest, and on
     the server that its changes were uploaded;
  2. a backup of the phone's storage, versions, Time Portal restore and the
     delete guard;
  3. scheduled syncs waiting for Wi-Fi, and running once it is back;
  4. starting again after a reboot, with drives reconnected;
  5. the phone's time zone, at start and when it changes.

It wipes the app's data and reboots the device, so it only runs on emulators.
"""
import argparse, json, os, shutil, socket, subprocess, sys, tempfile, time, urllib.error, urllib.request

PKG = "io.github.blaineam.enterspacelight"
PHONE = "/storage/emulated/0/esl-e2e"


def say(msg):
    print("==", msg, flush=True)


def check(cond, msg):
    if not cond:
        raise SystemExit("FAIL: " + msg)


class Device:
    def __init__(self, serial):
        self.adb = ["adb", "-s", serial]

    def run(self, *args, ok=True, timeout=600):
        p = subprocess.run(self.adb + list(args), capture_output=True, text=True, timeout=timeout)
        if ok and p.returncode != 0:
            raise SystemExit(f"FAIL: adb {' '.join(args)}\n{p.stdout}{p.stderr}")
        return p.stdout

    def sh(self, cmd, ok=True):
        return self.run("shell", cmd, ok=ok)

    def booted(self):
        self.run("wait-for-device")
        wait(300, "the device to boot", lambda: self.sh("getprop sys.boot_completed", ok=False).strip() == "1")
        wait(120, "shared storage", lambda: self.run("shell", "ls /storage/emulated/0/", ok=False) != "")

    def launch(self):
        self.sh(f"am start -W -n {PKG}/.MainActivity")


def wait(seconds, what, fn, every=1.0):
    deadline = time.time() + seconds
    last = None
    while time.time() < deadline:
        try:
            v = fn()
            if v:
                return v
        except Exception as e:  # the engine may still be starting
            last = e
        time.sleep(every)
    raise SystemExit(f"FAIL: timed out waiting for {what}" + (f" ({last})" if last else ""))


class API:
    """The engine's own API, reached through adb."""

    def __init__(self, dev):
        self.dev = dev
        self.hs = None

    def connect(self):
        hs = json.loads(self.dev.sh(f"run-as {PKG} cat files/state/api.json"))
        port = hs["url"].rsplit(":", 1)[1]
        self.dev.run("forward", "tcp:" + port, "tcp:" + port)
        self.hs = hs
        self.call("GET", "/api/status")
        return True

    def call(self, method, path, body=None):
        req = urllib.request.Request(
            self.hs["url"] + path, method=method,
            data=None if body is None else json.dumps(body).encode(),
            headers={"Authorization": "Bearer " + self.hs["token"], "X-ESL": "1", "Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=120) as r:
                return json.load(r)
        except urllib.error.HTTPError as e:
            raise RuntimeError(f"{method} {path}: {e.code} {e.read().decode()}")

    def status(self):
        return self.call("GET", "/api/status")

    def job(self, jid):
        return next(j for j in self.status()["jobs"] if j["id"] == jid)

    def run_job(self, jid, want_ok=True):
        before = (self.job(jid)["state"].get("last") or {}).get("started")
        self.call("POST", f"/api/jobs/{jid}/run")

        def done():
            st = self.job(jid)["state"]
            return not st.get("running") and (st.get("last") or {}).get("started") not in (None, before) and st["last"]
        last = wait(180, "a sync to finish", done)
        check(last["ok"] == want_ok, f"run ok={last['ok']}, wanted {want_ok}: {last.get('error')}")
        return last


def read(path):
    try:
        with open(path) as f:
            return f.read()
    except OSError:
        return None


def files_under(root, skip=".enterspace"):
    out = []
    for d, _, names in os.walk(root):
        out += [os.path.relpath(os.path.join(d, n), root) for n in names if skip not in d]
    return sorted(out)


def test_files(dev, served, dav_url):
    say("Files integration (instrumented test)")
    dev.sh(f"pm clear {PKG}")
    out = dev.run("shell", f"am instrument -w -e davURL {dav_url} {PKG}.test/androidx.test.runner.AndroidJUnitRunner")
    print(out.strip())
    check("OK (1 test)" in out, "DrivesProviderTest failed")
    e2e = os.path.join(served, "e2e")
    check(read(os.path.join(e2e, "sub", "renamed.txt")) == "short and appended", "the edited note didn't reach the server")
    import hashlib
    with open(os.path.join(e2e, "big.bin"), "rb") as f:
        check(hashlib.sha256(f.read()).hexdigest() == read(os.path.join(e2e, "big.sha256")), "big file differs on the server")
    check(files_under(e2e) == ["big.bin", "big.sha256", "sub/renamed.txt"], f"server has {files_under(e2e)}")
    check(read(os.path.join(served, "docs", "hello.txt")) == "hello from the server\n", "the read-only drive changed a file")


def setup_app(dev, api, dav_url):
    dev.sh(f"pm clear {PKG}")
    dev.sh(f"appops set {PKG} MANAGE_EXTERNAL_STORAGE allow")
    dev.sh(f"pm grant {PKG} android.permission.POST_NOTIFICATIONS", ok=False)
    dev.launch()
    wait(90, "the engine", api.connect)
    api.call("POST", "/api/remotes/config", {"name": "e2edav", "type": "webdav", "parameters": {"url": dav_url, "vendor": "rclone"}})


def test_backup(dev, api, served):
    say("Backup of the phone's storage, Time Portal and the delete guard")
    src = PHONE + "/source"
    dev.sh(f"rm -rf {PHONE}; mkdir -p '{src}/docs' '{src}/many folder/ünïcode' '{src}/.thumbnails'")
    dev.sh(f"printf 'version one' > {src}/a.txt; printf 'beta original' > {src}/docs/b.txt; printf x > {src}/.thumbnails/t.bin")
    dev.sh(f"i=0; while [ $i -lt 130 ]; do printf $i > \"{src}/many folder/ünïcode/file $i.txt\"; i=$((i+1)); done")
    setup = api.call("GET", "/api/backup/setup")
    check(setup["home"] == "/storage/emulated/0" and setup["os"] == "android", f"wizard setup: {setup['home']}")
    check([g["id"] for g in setup["groups"]][:2] == ["system", "temp"], "wrong skip lists")
    job = api.call("POST", "/api/jobs", {
        "name": "Phone backup", "kind": "sync", "enabled": True, "source": src, "dest": "e2edav:Backup/Phone",
        "triggers": {"daily": "02:30"},
        "backup": {"template": "folder", "retentionDays": 30, "excludeGroups": ["system", "temp"]}})
    jid, bid = job["id"], job["backup"]["id"]
    api.run_job(jid)
    dest = os.path.join(served, "Backup", "Phone")
    check(read(os.path.join(dest, "a.txt")) == "version one", "first backup incomplete")
    check(read(os.path.join(dest, "many folder", "ünïcode", "file 129.txt")) == "129", "non-ASCII names lost")
    check(not os.path.exists(os.path.join(dest, ".thumbnails")), "thumbnails were backed up")
    records = lambda: len(os.listdir(os.path.join(dest, ".enterspace", "manifests", bid)))
    check(records() == 1, "no run record on the destination")

    time.sleep(1.2)
    dev.sh(f"printf 'version two' > {src}/a.txt; rm -rf {src}/docs")
    api.run_job(jid)
    check(records() == 2, "both run records must remain")
    versions = [f for f in files_under(os.path.join(served, ".previous-versions", "Backup-Phone")) if f.endswith("a.txt")]
    check(len(versions) == 1, f"old version not kept: {versions}")

    timeline = api.call("GET", f"/api/portal/timeline?refresh=1&job={jid}")["timeline"]
    check(len(timeline) == 2, f"timeline {timeline}")
    from datetime import datetime
    first = int(datetime.fromisoformat(timeline[1].replace("Z", "+00:00")).timestamp())
    items = {i["name"]: i["state"] for i in api.call("GET", f"/api/portal/list?job={jid}&at={first}")["items"]}
    check(items.get("a.txt") == "modifiedSince" and items.get("docs") == "deletedSince", f"point-in-time view: {items}")

    def restore(body):
        api.call("POST", f"/api/portal/restore?job={jid}", body)

        def done():
            r = api.call("GET", "/api/portal/restore").get("restore")
            return r and r["finished"] and r
        report = wait(120, "the restore", done)
        check(not report.get("failed"), f"restore failures: {report.get('failed')}")
    restore({"paths": ["docs"], "at": first, "target": "folder", "folder": "~/esl-e2e/restored"})
    check(dev.sh(f"cat {PHONE}/restored/docs/b.txt") == "beta original", "deleted folder not restored to shared storage")
    restore({"paths": ["a.txt"], "at": first, "target": "original", "keepBoth": True})
    check(dev.sh(f"cat {src}/a.txt") == "version two", "restore overwrote the newer file")
    check(dev.sh(f"cat {src}/a\\ \\(restored*").strip() == "version one", "keep-both copy missing")

    dev.sh(f"rm -rf '{src}/many folder'")
    msg = api.run_job(jid, want_ok=False)["error"]
    check("would delete" in msg, f"guard message: {msg}")
    check(os.path.exists(os.path.join(dest, "many folder", "ünïcode", "file 0.txt")), "the guard let files be deleted")

    say("Backup into a folder on the phone")
    local = api.call("POST", "/api/jobs", {
        "name": "Local copy", "kind": "sync", "enabled": True, "source": src, "dest": PHONE + "/local-backup",
        "triggers": {}, "backup": {"template": "folder", "retentionDays": 0, "excludeGroups": ["system"]}})
    api.run_job(local["id"])
    dev.sh(f"printf 'version three' > {src}/a.txt")
    time.sleep(1.2)
    api.run_job(local["id"])
    check(dev.sh(f"cat {PHONE}/local-backup/a.txt") == "version three", "local backup not updated")
    found = dev.sh(f"find {PHONE}/.previous-versions -name a.txt")
    check(found.strip(), "local backup kept no old version")


def test_wifi_hold(dev, api, served):
    say("Scheduled syncs wait for Wi-Fi (the emulator's mobile data is metered)")
    dev.sh(f"mkdir -p {PHONE}/hold && printf held > {PHONE}/hold/h.txt")
    dev.sh("svc wifi disable")
    try:
        time.sleep(8)
        job = api.call("POST", "/api/jobs", {
            "name": "Held", "kind": "copy", "enabled": True, "source": PHONE + "/hold", "dest": "e2edav:held",
            "triggers": {"every": "1m"}})
        wait(150, "the sync to be held", lambda: api.job(job["id"]).get("waiting"), every=5)
        why = api.job(job["id"])["waiting"]
        check(why == "waiting for Wi-Fi", f"waiting reason: {why!r}")
        check(not os.path.exists(os.path.join(served, "held", "h.txt")), "ran over mobile data")
    finally:
        dev.sh("svc wifi enable")
    wait(90, "the held sync to run on Wi-Fi", lambda: read(os.path.join(served, "held", "h.txt")) == "held", every=3)
    api.call("DELETE", f"/api/jobs/{job['id']}")


def test_reboot_and_timezone(dev, api, served):
    say("A reboot: the app starts by itself and reconnects drives")
    mount = api.call("POST", "/api/mounts", {"name": "After reboot", "remote": "e2edav:", "mountPoint": "", "autoMount": True, "readOnly": False})
    state = lambda: next(m["state"] for m in api.status()["mounts"] if m["id"] == mount["id"])
    wait(60, "the drive", lambda: state()["status"] == "mounted")
    dev.run("reboot")
    time.sleep(10)
    dev.booted()
    # Nothing opens the app here: only the boot receiver can have started it.
    def back():
        api.connect()
        return state()["status"] == "mounted"
    wait(240, "the drive to reconnect after reboot", back, every=5)
    services = dev.sh(f"dumpsys activity services {PKG}")
    check("EngineService" in services and "isForeground=true" in services, "the background service isn't running in the foreground")

    say("The engine starts on the phone's time zone")
    # (An emulator takes the host's zone again at each boot, so set it now.)
    dev.sh("settings put global auto_time_zone 0")
    dev.sh("cmd alarm set-timezone Asia/Kolkata")
    dev.sh(f"am force-stop {PKG}")
    dev.launch()
    wait(90, "the engine", api.connect)
    wait(60, "the drive", lambda: state()["status"] == "mounted")
    since = state()["since"]
    check(since.endswith("+05:30"), f"engine isn't on the phone's time zone: {since}")

    say("A daily schedule follows a change of zone while running")
    dev.sh("cmd alarm set-timezone UTC")
    time.sleep(5)
    from datetime import datetime, timedelta, timezone
    due = datetime.now(timezone.utc) + timedelta(minutes=2)
    dev.sh(f"mkdir -p {PHONE}/zone && printf zoned > {PHONE}/zone/z.txt")
    api.call("POST", "/api/jobs", {"name": "Zoned", "kind": "copy", "enabled": True, "source": PHONE + "/zone",
                                   "dest": "e2edav:zoned", "triggers": {"daily": due.strftime("%H:%M")}})
    wait(240, "the daily run at the UTC time", lambda: read(os.path.join(served, "zoned", "z.txt")) == "zoned", every=5)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--serial", required=True)
    ap.add_argument("--apk-dir", default=os.path.join(os.path.dirname(__file__), "..", "android", "app", "build", "outputs", "apk"))
    args = ap.parse_args()
    check(args.serial.startswith("emulator-"), "this test wipes app data and reboots: emulators only")
    app = os.path.join(args.apk_dir, "debug", "app-debug.apk")
    tests = os.path.join(args.apk_dir, "androidTest", "debug", "app-debug-androidTest.apk")
    dev = Device(args.serial)
    dev.booted()

    work = tempfile.mkdtemp(prefix="esl-android-e2e-")
    served = os.path.join(work, "served")
    os.makedirs(os.path.join(served, "docs"))
    with open(os.path.join(served, "docs", "hello.txt"), "w") as f:
        f.write("hello from the server\n")
    empty = os.path.join(work, "empty.conf")
    open(empty, "w").close()
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        port = s.getsockname()[1]
    server = subprocess.Popen(["rclone", "serve", "webdav", served, "--addr", f"127.0.0.1:{port}"],
                              env={**os.environ, "RCLONE_CONFIG": empty},
                              stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        wait(30, "the WebDAV server", lambda: urllib.request.urlopen(f"http://127.0.0.1:{port}/docs/hello.txt").status == 200)
        dav = f"http://10.0.2.2:{port}"
        say("Install")
        dev.run("install", "-r", "-d", app)
        dev.run("install", "-r", "-t", tests)
        dev.run("logcat", "-c")
        test_files(dev, served, dav)
        api = API(dev)
        setup_app(dev, api, dav)
        test_backup(dev, api, served)
        test_wifi_hold(dev, api, served)
        test_reboot_and_timezone(dev, api, served)
        crashes = dev.run("logcat", "-d", "-b", "crash")
        check(PKG not in crashes, "the app crashed:\n" + crashes[-3000:])
        say("PASS")
    finally:
        server.terminate()
        dev.sh("svc wifi enable", ok=False)
        dev.sh(f"rm -rf {PHONE}", ok=False)
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())
