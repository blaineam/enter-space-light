package backup

import (
	"testing"
	"time"

	"github.com/rclone/rclone/fs/filter"
)

// kept reports whether rclone, given these rules, would back up the file.
func kept(t *testing.T, rules []string, file string) bool {
	t.Helper()
	opt := filter.Opt
	opt.FilterRule = append([]string(nil), rules...)
	f, err := filter.NewFilter(&opt)
	if err != nil {
		t.Fatal(err)
	}
	return f.Include(file, 1, time.Now(), nil)
}

// The skip lists are checked with rclone's own filter, at the top of the
// source and further down: "**/name" does not match "name" at the top.
func TestSkipListsMatchAtEveryDepth(t *testing.T) {
	for goos, files := range map[string][]string{
		"windows": {"$RECYCLE.BIN/x", "System Volume Information/x", "Thumbs.db", "~$draft.docx", "a.~tmp", "node_modules/a/b.js", "steamapps/common/g/data.bin", "Epic Games/Title/data.bin"},
		"linux":   {".Trash-1000/files/x", "notes.swp", ".~lock.report.odt#", "node_modules/a/b.js", "steamapps/common/g/data.bin"},
		"android": {".thumbnails/1.jpg", ".thumbdata3-123", ".cache/x", "cache/x", ".trashed-12-a.jpg", ".pending-1-a.mp4", ".Trash/x", ".trash/x", "a.tmp"},
	} {
		var all []string
		for _, g := range Groups(goos) {
			all = append(all, g.ID)
		}
		rules := Rules(goos, all, "/src", nil)
		for _, f := range files {
			for _, p := range []string{f, "deeper/still/" + f} {
				if kept(t, rules, p) {
					t.Errorf("%s: %q should be skipped", goos, p)
				}
			}
		}
		for _, f := range []string{"Documents/report.docx", "Pictures/cache.jpg", "my node_modules notes.txt"} {
			if !kept(t, rules, f) {
				t.Errorf("%s: %q must be backed up", goos, f)
			}
		}
	}
}
