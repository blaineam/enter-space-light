package backup

import (
	"sort"
	"time"
)

// Run-folder name formats retention understands (the Apple app's list).
// Names that parse as none of them are never deleted.
var runDirFormats = []string{"2006-01-02_15-04-05", "20060102-150405", "2006-01-02", "01-02-2006_15-04", "02-01-2006_15-04"}

// ParseRunDir reads a versions-folder name in local time.
func ParseRunDir(name string, loc *time.Location) (time.Time, bool) {
	for _, f := range runDirFormats {
		if t, err := time.ParseInLocation(f, name, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// Snapshot is one dated versions folder and the files in it.
type Snapshot struct {
	Name  string
	At    time.Time
	Paths []string
}

// PlanRetention decides which expired snapshots can go. Walking expired
// snapshots newest first, one is deleted only if every file in it still
// exists in the live backup or in a snapshot already kept; otherwise it is
// kept, and its files count as survivors so older copies can go. The newest
// copy of a file that no longer exists anywhere else is never deleted.
func PlanRetention(expired []Snapshot, live map[string]bool) (prune []string) {
	surv := make(map[string]bool, len(live))
	for p := range live {
		surv[p] = true
	}
	sort.Slice(expired, func(i, j int) bool { return expired[i].At.After(expired[j].At) })
	for _, s := range expired {
		all := true
		for _, p := range s.Paths {
			if !surv[p] {
				all = false
				break
			}
		}
		if all {
			prune = append(prune, s.Name)
			continue
		}
		for _, p := range s.Paths {
			surv[p] = true
		}
	}
	return prune
}
