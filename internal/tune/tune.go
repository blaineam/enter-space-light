// Package tune turns a mount or sync job into rclone parameters with
// sensible, per-backend defaults applied automatically. Users never need to
// know these flags exist; anything they set in Options still wins.
//
// All keys are rclone's flat option names (the CLI flag without "--", with
// "_" for "-"). The rc layer accepts them at the top level of a call;
// tune_test.go checks every key against rclone's real option registry so a
// rename upstream fails the build instead of silently doing nothing.
package tune

import (
	"fmt"
	"path"
	"strings"

	"github.com/blaineam/enter-space-light/internal/settings"
)

// Profile is the per-backend tuning class.
type Profile string

// Profiles. Cloud drives are API-rate-limited; object stores want
// parallelism; LAN/file protocols want modest parallelism and short caches
// because other machines change them without telling us.
const (
	ProfileDrive  Profile = "drive"  // Google Drive, OneDrive, Dropbox, Box, …
	ProfileObject Profile = "object" // S3 & friends, B2, GCS, Azure
	ProfileFile   Profile = "file"   // SFTP, SMB, FTP, WebDAV, local
	ProfileSlow   Profile = "slow"   // Mega and other strict providers
)

// ProfileFor maps an rclone backend type to a tuning profile.
func ProfileFor(backend string) Profile {
	switch backend {
	case "drive", "onedrive", "dropbox", "box", "pcloud", "yandex", "jottacloud",
		"koofr", "putio", "premiumizeme", "pikpak", "sharefile", "zoho", "hidrive",
		"internxt", "filen", "protondrive", "iclouddrive", "quatrix", "opendrive",
		"seafile", "linkbox", "gofile", "pixeldrain", "uptobox", "filefabric", "storj":
		return ProfileDrive
	case "s3", "b2", "google cloud storage", "gcs", "azureblob", "azurefiles",
		"swift", "qingstor", "oracleobjectstorage", "netstorage", "sia":
		return ProfileObject
	case "mega", "mailru", "1fichier", "fichier":
		return ProfileSlow
	default:
		return ProfileFile
	}
}

// Resolver returns a remote's backend type and, for wrapping backends
// (crypt, alias, chunker, compress, hasher, union, combine), the remote it wraps.
type Resolver func(remoteName string) (backendType, wraps string, ok bool)

// BackendOf follows wrapping backends down to the storage that actually
// receives the requests, so "secret:" (crypt over "b2:bucket") tunes as B2.
func BackendOf(remote string, resolve Resolver) string {
	name := RemoteName(remote)
	seen := map[string]bool{}
	for i := 0; i < 8 && name != "" && !seen[name]; i++ {
		seen[name] = true
		typ, wraps, ok := resolve(name)
		if !ok {
			return ""
		}
		switch typ {
		case "crypt", "alias", "chunker", "compress", "hasher", "cache":
			if wraps == "" {
				return typ
			}
			if !strings.Contains(wraps, ":") {
				return "local" // alias of a local path
			}
			name = RemoteName(wraps)
			continue
		}
		return typ
	}
	return ""
}

// RemoteName returns "gdrive" for "gdrive:Photos", "" for a local path or an
// on-the-fly ":s3,..:" remote.
func RemoteName(remote string) string {
	i := strings.Index(remote, ":")
	if i <= 0 {
		return ""
	}
	// A Windows drive path "C:\x" is local, not a remote called "C".
	if i == 1 && len(remote) > 2 && (remote[2] == '\\' || remote[2] == '/') {
		return ""
	}
	if i == 1 && len(remote) == 2 {
		return ""
	}
	name := remote[:i]
	if strings.ContainsAny(name, `/\`) {
		return ""
	}
	return name
}

// Env describes the machine the daemon runs on.
type Env struct {
	GOOS string
	// CacheBudget is the VFS cache size each mount may use, as an rclone
	// size string ("10G"). Computed by CacheBudget().
	CacheBudget string
}

// Params is a flat rclone parameter map.
type Params map[string]any

// GiB is a convenience size unit.
const GiB = int64(1) << 30

// CacheBudget decides how much disk each mount's VFS cache may use: a fifth
// of the free space split across mounts, clamped to [1 GiB, 50 GiB]. A user
// setting always wins.
func CacheBudget(freeBytes int64, mounts int, userSetting string) string {
	if strings.TrimSpace(userSetting) != "" {
		return userSetting
	}
	if mounts < 1 {
		mounts = 1
	}
	per := freeBytes / 5 / int64(mounts)
	if per < GiB {
		per = GiB
	}
	if per > 50*GiB {
		per = 50 * GiB
	}
	return fmt.Sprintf("%dG", per/GiB)
}

// transfer settings per profile, shared by mounts (write-back uploads) and syncs.
func transferParams(p Profile) Params {
	switch p {
	case ProfileDrive:
		return Params{"transfers": 4, "checkers": 8, "tpslimit": 10.0, "tpslimit_burst": 10}
	case ProfileObject:
		return Params{"transfers": 8, "checkers": 16}
	case ProfileSlow:
		return Params{"transfers": 2, "checkers": 4, "tpslimit": 4.0, "tpslimit_burst": 4}
	default:
		return Params{"transfers": 4, "checkers": 8}
	}
}

// reliability settings every call gets.
func reliabilityParams() Params {
	return Params{
		"low_level_retries": 10,
		"contimeout":        "30s",
		"timeout":           "5m",
		"retries_sleep":     "10s",
	}
}

// MountParams builds the rc mount/mount call for m (without fs/mountPoint).
func MountParams(m settings.Mount, backend string, env Env) Params {
	p := Params{}
	for k, v := range reliabilityParams() {
		p[k] = v
	}
	delete(p, "retries_sleep") // only meaningful for syncs
	prof := ProfileFor(backend)
	for k, v := range transferParams(prof) {
		p[k] = v
	}

	// VFS: full caching so every application sees normal file semantics
	// (random writes, seeks, rename-over, memory-mapped reads).
	p["vfs_cache_mode"] = "full"
	p["vfs_cache_max_size"] = env.CacheBudget
	p["vfs_cache_max_age"] = "168h"
	p["vfs_cache_min_free_space"] = "2G"
	// Never 0: rclone uploads synchronously inside close() at 0, with no
	// retry and no queue. 5s batches editor save storms and keeps retries.
	p["vfs_write_back"] = "5s"
	p["vfs_read_ahead"] = "32M"
	p["vfs_read_chunk_size"] = "32M"
	p["vfs_read_chunk_size_limit"] = "512M"
	p["buffer_size"] = "16M"

	// Directory cache: backends with change notification (drive class)
	// can cache long and be told about changes; the rest must re-list
	// sooner to notice edits made elsewhere.
	if prof == ProfileDrive {
		p["dir_cache_time"] = "1h"
		p["poll_interval"] = "1m"
	} else {
		p["dir_cache_time"] = "2m"
	}
	if prof == ProfileObject {
		p["vfs_fast_fingerprint"] = true
	}

	switch env.GOOS {
	case "windows":
		// Network mode makes Explorer treat the drive like a mapped share:
		// no recycle bin, no thumbnail-cache writes, correct free space,
		// and it shows under "Network locations".
		p["network_mode"] = true
		p["volname"] = `\\enterspace\` + volLabel(m.Name)
		p["vfs_case_insensitive"] = true
	case "linux":
		p["volname"] = m.Name
		// Let the kernel cache attributes briefly; huge win for `ls -l`
		// and IDE file watchers. Matches rclone's own recommendation.
		p["attr_timeout"] = "1s"
	}
	if m.ReadOnly {
		p["read_only"] = true
	}
	for k, v := range m.Options {
		p[k] = v
	}
	return p
}

// volLabel keeps a UNC share name legal.
func volLabel(name string) string {
	r := strings.NewReplacer(`\`, "-", "/", "-", ":", "-", "*", "-", "?", "-", `"`, "", "<", "", ">", "", "|", "-")
	out := strings.TrimSpace(r.Replace(name))
	if out == "" {
		return "remote"
	}
	return out
}

// AppStateRule keeps `.enterspace/` folders (backup run records, app
// caches) out of every sync. The same rule as Enter Space for Apple
// platforms (ScheduledSync.appStateRule).
const AppStateRule = "- .enterspace/**"

// SyncCall is a ready-to-run rc call for a job.
type SyncCall struct {
	Method string
	Params Params
}

// Expand replaces "{volume}" with the triggering volume's root.
func Expand(p, volumeRoot string) string {
	if !strings.Contains(p, "{volume}") {
		return p
	}
	root := strings.TrimRight(volumeRoot, `/\`)
	rest := strings.TrimLeft(strings.ReplaceAll(p, "{volume}", ""), `/\`)
	if rest == "" {
		if len(root) == 2 && root[1] == ':' {
			return root + `\`
		}
		return root
	}
	if strings.Contains(root, `\`) || (len(root) == 2 && root[1] == ':') {
		return root + `\` + strings.ReplaceAll(rest, "/", `\`)
	}
	return path.Join(root, rest)
}

// JobCall builds the rc call for j. firstBisync requests --resync, which
// bisync needs before its first ever run on a pair of paths.
func JobCall(j settings.Job, srcBackend, dstBackend, volumeRoot, bisyncWorkdir string, firstBisync bool) SyncCall {
	src := Expand(j.Source, volumeRoot)
	dst := Expand(j.Dest, volumeRoot)

	p := Params{}
	for k, v := range reliabilityParams() {
		p[k] = v
	}
	p["retries"] = 3

	// The slower side decides parallelism.
	sp, dp := ProfileFor(srcBackend), ProfileFor(dstBackend)
	prof := dp
	if rank(sp) < rank(dp) {
		prof = sp
	}
	for k, v := range transferParams(prof) {
		p[k] = v
	}
	if sp == ProfileObject || dp == ProfileObject {
		p["fast_list"] = true
	}
	for k, v := range j.Options {
		p[k] = v
	}

	// Enter Space's own state (backup run records, app caches) never travels
	// and is never deleted, whatever the job's rules say: first rule, and
	// unanchored so rclone matches it at any depth including the root.
	rules := []string{AppStateRule}
	for _, f := range j.Filters {
		f = strings.TrimSpace(f)
		if f != "" && !strings.HasPrefix(f, "#") {
			rules = append(rules, f)
		}
	}
	p["_filter"] = map[string]any{"FilterRule": rules}

	switch j.Kind {
	case settings.KindBisync:
		p["path1"] = src
		p["path2"] = dst
		p["workdir"] = bisyncWorkdir
		p["resilient"] = true
		p["recover"] = true
		p["maxLock"] = "2m"
		p["conflictResolve"] = "newer"
		p["createEmptySrcDirs"] = true
		if firstBisync {
			p["resync"] = true
		}
		return SyncCall{Method: "sync/bisync", Params: p}
	case settings.KindMove:
		p["srcFs"] = src
		p["dstFs"] = dst
		p["deleteEmptySrcDirs"] = true
		p["createEmptySrcDirs"] = true
		return SyncCall{Method: "sync/move", Params: p}
	case settings.KindCopy:
		p["srcFs"] = src
		p["dstFs"] = dst
		p["createEmptySrcDirs"] = true
		return SyncCall{Method: "sync/copy", Params: p}
	default:
		p["srcFs"] = src
		p["dstFs"] = dst
		p["createEmptySrcDirs"] = true
		return SyncCall{Method: "sync/sync", Params: p}
	}
}

func rank(p Profile) int {
	switch p {
	case ProfileSlow:
		return 0
	case ProfileDrive:
		return 1
	case ProfileFile:
		return 2
	default:
		return 3
	}
}
