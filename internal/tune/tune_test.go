package tune

import (
	"testing"

	"github.com/blaineam/enter-space-light/internal/settings"
	"github.com/rclone/rclone/cmd/mountlib"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configstruct"
	"github.com/rclone/rclone/fs/filter"
	"github.com/rclone/rclone/vfs/vfscommon"
)

// knownOptions is every flat option name rclone's rc accepts for a mount
// call (global config + VFS + mount) and for a sync (global config).
func knownOptions(t *testing.T) (mount, sync map[string]bool) {
	t.Helper()
	mount, sync = map[string]bool{}, map[string]bool{}
	add := func(dst map[string]bool, opt any) {
		items, err := configstruct.Items(opt)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			dst[it.Name] = true
		}
	}
	add(mount, &fs.ConfigInfo{})
	add(mount, &vfscommon.Options{})
	add(mount, &mountlib.Options{})
	add(sync, &fs.ConfigInfo{})
	return mount, sync
}

func TestEveryMountOptionIsARealRcloneOption(t *testing.T) {
	mount, _ := knownOptions(t)
	for _, goos := range []string{"windows", "linux", "android"} {
		for _, backend := range []string{"drive", "s3", "sftp", "mega"} {
			p := MountParams(settings.Mount{Name: "X", ReadOnly: true}, backend, Env{GOOS: goos, CacheBudget: "10G"})
			for k := range p {
				if !mount[k] {
					t.Errorf("%s/%s: %q is not an rclone mount/vfs/config option (renamed upstream?)", goos, backend, k)
				}
			}
		}
	}
}

func TestEverySyncOptionIsARealRcloneOption(t *testing.T) {
	_, sync := knownOptions(t)
	callArgs := map[string]map[string]bool{
		"sync/sync":   {"srcFs": true, "dstFs": true, "createEmptySrcDirs": true},
		"sync/copy":   {"srcFs": true, "dstFs": true, "createEmptySrcDirs": true},
		"sync/move":   {"srcFs": true, "dstFs": true, "createEmptySrcDirs": true, "deleteEmptySrcDirs": true},
		"sync/bisync": {"path1": true, "path2": true, "workdir": true, "resilient": true, "recover": true, "maxLock": true, "conflictResolve": true, "createEmptySrcDirs": true, "resync": true},
	}
	for _, kind := range []settings.JobKind{settings.KindSync, settings.KindCopy, settings.KindMove, settings.KindBisync} {
		j := settings.Job{Kind: kind, Source: "a:", Dest: "b:", Filters: []string{"- *.tmp"}}
		c := JobCall(j, "s3", "drive", "", "/w", true)
		for k := range c.Params {
			if k == "_filter" || sync[k] || callArgs[c.Method][k] {
				continue
			}
			t.Errorf("%s: %q is neither an rclone option nor a %s argument", kind, k, c.Method)
		}
	}
}

func TestFilterBlockUsesRealFieldName(t *testing.T) {
	// _filter is decoded as JSON into filter.Options; FilterRule must exist.
	var o filter.Options
	o.FilterRule = []string{"- x"}
	c := JobCall(settings.Job{Kind: settings.KindCopy, Source: "a:", Dest: "b:", Filters: []string{"# note", " ", "+ /keep/**", "- **"}}, "", "", "", "", false)
	got := c.Params["_filter"].(map[string]any)["FilterRule"].([]string)
	if len(got) != 3 || got[0] != AppStateRule || got[1] != "+ /keep/**" || got[2] != "- **" {
		t.Fatalf("own-state rule first, then the job's rules in order, comments/blanks dropped: %v", got)
	}
	bare := JobCall(settings.Job{Kind: settings.KindCopy, Source: "a:", Dest: "b:"}, "", "", "", "", false)
	if r := bare.Params["_filter"].(map[string]any)["FilterRule"].([]string); len(r) != 1 || r[0] != AppStateRule {
		t.Fatalf("a job without rules still protects .enterspace: %v", r)
	}
}

func TestWriteBackIsNeverZero(t *testing.T) {
	p := MountParams(settings.Mount{Name: "x"}, "drive", Env{GOOS: "linux", CacheBudget: "1G"})
	if p["vfs_write_back"] == "0" || p["vfs_write_back"] == "0s" {
		t.Fatal("write-back 0 uploads synchronously inside close() with no retry")
	}
}

func TestUserOptionsWin(t *testing.T) {
	m := settings.Mount{Name: "x", Options: map[string]string{"vfs_cache_mode": "writes", "transfers": "1"}}
	p := MountParams(m, "drive", Env{GOOS: "windows", CacheBudget: "1G"})
	if p["vfs_cache_mode"] != "writes" || p["transfers"] != "1" {
		t.Fatalf("user overrides lost: %v", p)
	}
	if p["volname"] != `\\enterspace\x` || p["network_mode"] != true {
		t.Fatalf("windows network drive params missing: %v", p)
	}
}

func TestProfiles(t *testing.T) {
	cases := map[string]Profile{"drive": ProfileDrive, "onedrive": ProfileDrive, "s3": ProfileObject, "b2": ProfileObject, "sftp": ProfileFile, "local": ProfileFile, "mega": ProfileSlow, "unknownthing": ProfileFile}
	for b, want := range cases {
		if got := ProfileFor(b); got != want {
			t.Errorf("%s: got %s want %s", b, got, want)
		}
	}
}

func TestBackendOfFollowsWrappers(t *testing.T) {
	conf := map[string][2]string{
		"secret": {"crypt", "b2:bucket/enc"},
		"b2":     {"b2", ""},
		"photos": {"alias", "secret:Photos"},
		"loop":   {"alias", "loop:"},
		"home":   {"alias", "/home/u"},
	}
	resolve := func(n string) (string, string, bool) {
		v, ok := conf[n]
		return v[0], v[1], ok
	}
	if got := BackendOf("photos:2024", resolve); got != "b2" {
		t.Fatalf("alias→crypt→b2 resolved to %q", got)
	}
	if got := BackendOf("loop:", resolve); got != "" {
		t.Fatalf("a cycle must stop, got %q", got)
	}
	if got := BackendOf("home:", resolve); got != "local" {
		t.Fatalf("alias of a local path is local, got %q", got)
	}
	if got := BackendOf("missing:", resolve); got != "" {
		t.Fatalf("unknown remote, got %q", got)
	}
}

func TestRemoteName(t *testing.T) {
	cases := map[string]string{"gdrive:": "gdrive", "gdrive:Photos/2024": "gdrive", `C:\Users`: "", "C:": "", "/home/u": "", "relative/path": "", ":s3,provider=AWS:bucket": ""}
	for in, want := range cases {
		if got := RemoteName(in); got != want {
			t.Errorf("RemoteName(%q)=%q want %q", in, got, want)
		}
	}
}

func TestCacheBudget(t *testing.T) {
	if got := CacheBudget(500*GiB, 2, ""); got != "50G" {
		t.Errorf("500G free, 2 mounts: %s", got)
	}
	if got := CacheBudget(100*GiB, 4, ""); got != "5G" {
		t.Errorf("100G free, 4 mounts: %s", got)
	}
	if got := CacheBudget(2*GiB, 3, ""); got != "1G" {
		t.Errorf("tiny disk floors at 1G: %s", got)
	}
	if got := CacheBudget(100*GiB, 1, "3G"); got != "3G" {
		t.Errorf("user setting wins: %s", got)
	}
}

func TestExpandVolume(t *testing.T) {
	cases := []struct{ in, root, want string }{
		{"{volume}/Backup/Docs", `E:\`, `E:\Backup\Docs`},
		{"{volume}", `E:\`, `E:\`},
		{"{volume}/Backup", "/media/u/USB", "/media/u/USB/Backup"},
		{"gdrive:Docs", "/media/u/USB", "gdrive:Docs"},
	}
	for _, c := range cases {
		if got := Expand(c.in, c.root); got != c.want {
			t.Errorf("Expand(%q,%q)=%q want %q", c.in, c.root, got, c.want)
		}
	}
}

func TestBisyncFirstRunResyncs(t *testing.T) {
	j := settings.Job{Kind: settings.KindBisync, Source: "a:", Dest: "b:"}
	if c := JobCall(j, "", "", "", "/w", true); c.Params["resync"] != true {
		t.Fatal("first bisync must resync")
	}
	if c := JobCall(j, "", "", "", "/w", false); c.Params["resync"] != nil {
		t.Fatal("later bisyncs must not resync")
	}
}

func TestSlowerSideDecidesParallelism(t *testing.T) {
	c := JobCall(settings.Job{Kind: settings.KindCopy, Source: "s3:x", Dest: "mega:y"}, "s3", "mega", "", "", false)
	if c.Params["transfers"] != 2 || c.Params["fast_list"] != true {
		t.Fatalf("s3→mega should use mega's limits and fast_list: %v", c.Params)
	}
}
