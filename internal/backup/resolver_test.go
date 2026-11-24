package backup

import (
	"testing"
	"time"
)

var day0 = time.Date(2026, 11, 1, 2, 0, 0, 0, time.UTC)

func at(d int) time.Time { return day0.Add(time.Duration(d) * 24 * time.Hour) }

func e(p string, size int64, mod time.Time) Entry {
	return Entry{Path: p, Size: size, ModTime: T(mod), Presence: Present}
}

func run(n int, live []Entry, versioned []Entry, cov Coverage) *Manifest {
	bd := "b2:.previous-versions/Backup-PC/run" + string(rune('0'+n))
	return &Manifest{FormatVersion: 1, RunID: "RUN" + string(rune('0'+n)), SyncID: "S", SyncName: "PC",
		StartedAt: T(at(n).Add(-time.Minute)), FinishedAt: T(at(n)), DestinationFs: "b2:Backup/PC",
		BackupDirFs: &bd, Outcome: Completed, LiveEntries: live, VersionedEntries: versioned, TreeCoverage: cov}
}

func find(l Listing, name string) *Item {
	for i := range l.Items {
		if l.Items[i].Name == name {
			return &l.Items[i]
		}
	}
	return nil
}

// A file copied once and never modified must appear at every instant after
// it existed — the bug the Apple app's rebuild fixed.
func TestUnchangedFileAppearsEverywhere(t *testing.T) {
	ix := NewIndex([]*Manifest{
		run(1, []Entry{e("keep.txt", 5, at(0))}, nil, CoverageFull),
		run(2, []Entry{e("keep.txt", 5, at(0))}, nil, CoverageFull),
		run(3, []Entry{e("keep.txt", 5, at(0))}, nil, CoverageFull),
	}, nil)
	it := find(ix.List("", at(1)), "keep.txt")
	if it == nil || it.State != UnchangedSince || it.Confidence != Inferred || it.ContentFs != "b2:Backup/PC/keep.txt" {
		t.Fatalf("%+v", it)
	}
}

func TestModifiedFileComesFromTheNextRunsVersions(t *testing.T) {
	ix := NewIndex([]*Manifest{
		run(1, []Entry{e("doc.txt", 5, at(0))}, nil, CoverageFull),
		run(2, []Entry{e("doc.txt", 9, at(2).Add(-time.Hour))}, []Entry{e("doc.txt", 5, at(0))}, CoverageFull),
	}, nil)
	old := find(ix.List("", at(1)), "doc.txt")
	if old == nil || old.State != ModifiedSince || old.Confidence != Authoritative || old.Size != 5 || old.ContentFs != "b2:.previous-versions/Backup-PC/run2/doc.txt" {
		t.Fatalf("at T between runs, the displaced version is the content: %+v", old)
	}
	now := find(ix.List("", at(2)), "doc.txt")
	if now == nil || now.State != UnchangedSince || now.Size != 9 {
		t.Fatalf("after run 2, live copy: %+v", now)
	}
}

func TestDeletedFileAndFolderShowAsDeleted(t *testing.T) {
	ix := NewIndex([]*Manifest{
		run(1, []Entry{e("a.txt", 1, at(0)), e("Old/x.txt", 1, at(0)), e("Old/y.txt", 1, at(0))}, nil, CoverageFull),
		run(2, []Entry{e("a.txt", 1, at(0))}, []Entry{e("Old/x.txt", 1, at(0)), e("Old/y.txt", 1, at(0))}, CoverageFull),
	}, nil)
	l := ix.List("", at(1))
	if d := find(l, "Old"); d == nil || !d.IsDir || d.State != DeletedSince {
		t.Fatalf("deleted folder: %+v", d)
	}
	in := find(ix.List("Old", at(1)), "x.txt")
	if in == nil || in.State != DeletedSince || in.Confidence != Authoritative {
		t.Fatalf("deleted file: %+v", in)
	}
	if find(ix.List("", at(2)), "Old") != nil {
		t.Fatal("after the deletion the folder must not exist")
	}
}

func TestPartialListingNeverClaimsADeletion(t *testing.T) {
	ix := NewIndex([]*Manifest{
		run(1, []Entry{e("db.sqlite", 1, at(0))}, nil, CoverageFull),
		run(2, []Entry{}, []Entry{e("db.sqlite", 1, at(0))}, CoveragePartial),
	}, nil)
	it := find(ix.List("", at(1)), "db.sqlite")
	if it == nil || it.State != ModifiedSince || it.Confidence != Uncertain || it.ContentFs == "" {
		t.Fatalf("truncated listing: right bytes, no deletion claim: %+v", it)
	}
}

func TestCreatedAfterTIsOmitted(t *testing.T) {
	ix := NewIndex([]*Manifest{
		run(1, []Entry{e("a.txt", 1, at(0))}, nil, CoverageFull),
		run(3, []Entry{e("a.txt", 1, at(0)), e("new.txt", 1, at(3).Add(-time.Hour))}, nil, CoverageFull),
	}, nil)
	if find(ix.List("", at(1)), "new.txt") != nil {
		t.Fatal("a file written after T must not appear at T")
	}
}

func TestPrunedVersionsAreReportedNotHidden(t *testing.T) {
	ms := []*Manifest{
		run(1, []Entry{e("doc.txt", 5, at(0))}, nil, CoverageFull),
		run(2, []Entry{e("doc.txt", 9, at(2).Add(-time.Hour))}, []Entry{e("doc.txt", 5, at(0))}, CoverageFull),
	}
	ix := NewIndex(ms, func(bd string) bool { return bd == *ms[1].BackupDirFs })
	it := find(ix.List("", at(1)), "doc.txt")
	if it == nil || it.State != PrunedUnavailable || it.ContentFs != "" {
		t.Fatalf("pruned version: %+v", it)
	}
	ok, missing := ix.Files([]string{"doc.txt"}, at(1))
	if len(ok) != 0 || len(missing) != 1 {
		t.Fatalf("restore plan must list it as unrecoverable: %v %v", ok, missing)
	}
}

func TestStaleSightingBeforeTIsOmitted(t *testing.T) {
	ix := NewIndex([]*Manifest{
		run(1, []Entry{e("gone.txt", 1, at(0)), e("a", 1, at(0))}, nil, CoverageFull),
		run(2, []Entry{e("a", 1, at(0))}, nil, CoverageFull), // gone without history catching it
	}, nil)
	if find(ix.List("", at(3)), "gone.txt") != nil {
		t.Fatal("a full run between the sighting and T saw it gone")
	}
	// Between its last sighting and the run that missed it, we can't know.
	if it := find(ix.List("", at(1).Add(time.Hour)), "gone.txt"); it == nil || it.State != Unknown {
		t.Fatalf("want unknown, got %+v", it)
	}
}

func TestHistoryThatStartsAfterTIsFlagged(t *testing.T) {
	ix := NewIndex([]*Manifest{run(5, []Entry{e("a", 1, at(-30))}, nil, CoverageFull)}, nil)
	l := ix.List("", at(1))
	if l.Note == "" {
		t.Fatal("the view must say history doesn't reach back that far")
	}
	if it := find(l, "a"); it == nil || it.Confidence != Uncertain {
		t.Fatalf("%+v", it)
	}
}

func TestFilesExpandsFoldersForRestore(t *testing.T) {
	ix := NewIndex([]*Manifest{run(1, []Entry{e("P/1.jpg", 1, at(0)), e("P/sub/2.jpg", 1, at(0)), e("Q.txt", 1, at(0))}, nil, CoverageFull)}, nil)
	ok, missing := ix.Files([]string{"P"}, at(1))
	if len(ok) != 2 || len(missing) != 0 {
		t.Fatalf("%v %v", ok, missing)
	}
	if tl := ix.Timeline(); len(tl) != 1 || !tl[0].Equal(at(1)) {
		t.Fatalf("timeline %v", tl)
	}
}
