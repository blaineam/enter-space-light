package backup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testdata/apple-written.json.deflate was produced by Enter Space's own
// BackupManifestCodec (scripts/check-apple-compat.sh). Reading it proves a
// Mac/iPhone backup is browsable here.
func TestReadsAManifestWrittenByTheAppleApp(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "apple-written.json.deflate"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if m.SyncID != "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE" || m.SyncName != "Home — Mac" || m.Outcome != CompletedWithErrors || m.TreeCoverage != CoveragePartial {
		t.Fatalf("header: %+v", m)
	}
	if !m.StartedAt.Equal(time.Date(2026, 8, 3, 2, 0, 0, 0, time.UTC)) || m.BackupDirFs == nil || !strings.HasSuffix(*m.BackupDirFs, "2026-08-02_19-00-00") {
		t.Fatalf("times/backupDir: %v %v", m.StartedAt, m.BackupDirFs)
	}
	if len(m.VersionedEntries) != 1 || *m.VersionedEntries[0].Hash != "abc" || len(m.LiveEntries) != 2 {
		t.Fatalf("entries: %+v", m)
	}
	if e := m.LiveEntries[1]; e.Path != "Pictures/ünïcödé.jpg" || e.Presence != PrunedByRetention || e.ModTime.Year() != 1 || e.Hash != nil {
		t.Fatalf("distantPast / unicode / omitted optionals: %+v", e)
	}
	if m.Stats.AbortReason != nil || m.Stats.Errors != 1 || m.Stats.BytesTransferred != 4096 {
		t.Fatalf("stats: %+v", m.Stats)
	}
}

// What we write must satisfy Swift's decoder: whole-second ISO dates, nil
// optionals omitted, empty arrays present, raw DEFLATE.
func TestWritesWhatTheAppleAppReads(t *testing.T) {
	bd := "b2:.previous-versions/Backup-PC/2026-11-23_02-00-00"
	m := &Manifest{FormatVersion: 1, RunID: NewUUID(), SyncID: NewUUID(), SyncName: "PC",
		StartedAt:  T(time.Date(2026, 11, 23, 2, 0, 0, 123456789, time.FixedZone("x", -6*3600))),
		FinishedAt: T(time.Date(2026, 11, 23, 9, 5, 0, 0, time.UTC)), SourceFs: PortableFs(`C:\Users\me`),
		DestinationFs: "b2:Backup/PC", BackupDirFs: &bd, Outcome: Completed, TreeCoverage: CoverageFull,
		LiveEntries: []Entry{{Path: "a.txt", Size: 1, ModTime: T(time.Time{}), Presence: Present}}}
	enc, err := Encode(m)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(enc, []byte("{")) || bytes.HasPrefix(enc, []byte{0x78}) {
		t.Fatal("must be raw DEFLATE, not JSON or zlib-wrapped")
	}
	back, err := Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	js, _ := json.Marshal(back)
	s := string(js)
	for _, want := range []string{`"startedAt":"2026-11-23T08:00:00Z"`, `"modTime":"0001-01-01T00:00:00Z"`, `"versionedEntries":[]`, `"sourceFs":"/C:/Users/me"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
	for _, bad := range []string{`"hash":null`, `"abortReason"`, `.123`} {
		if strings.Contains(s, bad) {
			t.Errorf("Swift would reject/misread %s", bad)
		}
	}
	if !ValidUUID(m.RunID) || strings.ToUpper(m.RunID) != m.RunID {
		t.Fatalf("run IDs must be uppercase UUIDs: %s", m.RunID)
	}
	if out := os.Getenv("ESL_WRITE_GO_MANIFEST"); out != "" { // used by scripts/check-apple-compat.sh
		if err := os.WriteFile(out, enc, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDecodeRejectsNewerFormatAndAcceptsPlainJSON(t *testing.T) {
	if _, err := Decode([]byte(`{"formatVersion":2}`)); err != ErrNewerFormat {
		t.Fatalf("newer format: %v", err)
	}
	m, err := Decode([]byte(`{"formatVersion":1,"syncName":"x","startedAt":"2026-01-01T00:00:00Z","finishedAt":"2026-01-01T00:00:00.5Z","versionedEntries":[],"liveEntries":[],"outcome":"completed","treeCoverage":"none","stats":{"filesTransferred":0,"filesDeleted":0,"filesChecked":0,"bytesTransferred":0,"errors":0}}`))
	if err != nil || m.SyncName != "x" {
		t.Fatalf("plain JSON: %v", err)
	}
}

func TestFsHelpersMatchSwift(t *testing.T) {
	cases := []struct{ fs, rel, want string }{
		{"b2:Backup/Home", "a/b.txt", "b2:Backup/Home/a/b.txt"},
		{"b2:", "/a/", "b2:a"},
		{"/Volumes/X/", "a", "/Volumes/X/a"},
		{"b2:x", "", "b2:x"},
	}
	for _, c := range cases {
		if got := FsJoin(c.fs, c.rel); got != c.want {
			t.Errorf("FsJoin(%q,%q)=%q want %q", c.fs, c.rel, got, c.want)
		}
	}
	if r, p := FsSplit("/C:/Users"); r != "" || p != "/C:/Users" {
		t.Error("portable Windows path must read as local")
	}
	if r, _ := FsSplit("C:/Users"); r != "C" {
		t.Error("(documenting why PortableFs exists)")
	}
	if got := NativeFs(PortableFs(`D:\Backups\PC`), true); got != `D:\Backups\PC` {
		t.Errorf("round trip: %q", got)
	}
	if got := Namespace("Backup/Home", "Home"); got != "Backup-Home" {
		t.Errorf("namespace: %q", got)
	}
	if got := Namespace(" / ", "Home"); got != "Home" {
		t.Errorf("namespace fallback: %q", got)
	}
	if !IsControlPath(".enterspace/manifests/x") || !IsControlPath("a/.pbsdb-wal") || IsControlPath("docs/enterspace.txt") {
		t.Error("control paths")
	}
}

func TestSplitFile(t *testing.T) {
	cases := []struct{ in, dir, leaf string }{
		{"b2:Backup/PC/a.txt", "b2:Backup/PC", "a.txt"},
		{"b2:a.txt", "b2:", "a.txt"},
		{`D:\Backups\PC\a.txt`, `D:\Backups\PC`, "a.txt"},
		{`D:\a.txt`, `D:\`, "a.txt"},
		{"/D:/Backups/a.txt", "/D:/Backups", "a.txt"},
		{"/home/u/a.txt", "/home/u", "a.txt"},
		{"/a.txt", "/", "a.txt"},
	}
	for _, c := range cases {
		if d, l := splitFile(c.in); d != c.dir || l != c.leaf {
			t.Errorf("splitFile(%q) = %q, %q; want %q, %q", c.in, d, l, c.dir, c.leaf)
		}
	}
}
