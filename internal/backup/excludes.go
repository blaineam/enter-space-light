// Package backup turns a sync job into a versioned backup: the wizard's
// templates, default excludes, version history on the destination, run
// manifests compatible with Enter Space for Apple platforms, retention, a
// delete guard, and point-in-time recovery ("Time Portal").
package backup

import (
	"path/filepath"
	"sort"
	"strings"
)

// ExcludeGroup is a named set of rclone filter rules the wizard can show
// ("Skip caches and temporary files") instead of raw patterns.
type ExcludeGroup struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Why         string   `json:"why"`
	Rules       []string `json:"rules"` // rclone filter rules, "- pattern"
	DefaultOn   bool     `json:"defaultOn"`
	Recommended bool     `json:"recommended"`
}

// Rules are relative to the backup source root. A leading "/" anchors to
// it; without one a rule matches at any depth, the top included. Don't write
// "**/name": rclone then requires a folder above "name" (see the test).

func windowsGroups() []ExcludeGroup {
	return []ExcludeGroup{
		{ID: "system", Title: "Windows system files", DefaultOn: true, Recommended: true,
			Why: "Locked or rebuilt by Windows; copying them fails or restores nothing useful.",
			Rules: []string{
				"- /NTUSER.DAT*", "- /ntuser.dat*", "- /ntuser.ini",
				"- /AppData/Local/Microsoft/Windows/UsrClass.dat*",
				"- $RECYCLE.BIN/**", "- System Volume Information/**",
				"- /pagefile.sys", "- /hiberfil.sys", "- /swapfile.sys", "- /DumpStack.log*",
				"- Thumbs.db", "- ~$*", "- *.~tmp",
			}},
		{ID: "temp", Title: "Temporary files and caches", DefaultOn: true, Recommended: true,
			Why: "Programs recreate these; they churn constantly and make every backup slower.",
			Rules: []string{
				"- /AppData/Local/Temp/**",
				"- /AppData/Local/Microsoft/Windows/INetCache/**",
				"- /AppData/Local/Microsoft/Windows/Explorer/thumbcache_*",
				"- /AppData/Local/Microsoft/Windows/WebCache/**",
				"- /AppData/Local/CrashDumps/**",
				"- /AppData/Local/D3DSCache/**",
				"- /AppData/Local/NVIDIA/**Cache/**",
				"- /AppData/Local/Packages/*/AC/**",
				"- /AppData/Local/Packages/*/LocalCache/**",
				"- /AppData/Local/Packages/*/TempState/**",
			}},
		{ID: "browsers", Title: "Web browser caches", DefaultOn: true, Recommended: true,
			Why: "Downloaded web content; bookmarks, passwords and history are still backed up.",
			Rules: []string{
				"- /AppData/Local/Google/Chrome/User Data/*/Cache/**",
				"- /AppData/Local/Google/Chrome/User Data/*/Code Cache/**",
				"- /AppData/Local/Google/Chrome/User Data/*/GPUCache/**",
				"- /AppData/Local/Google/Chrome/User Data/*/Service Worker/CacheStorage/**",
				"- /AppData/Local/Microsoft/Edge/User Data/*/Cache/**",
				"- /AppData/Local/Microsoft/Edge/User Data/*/Code Cache/**",
				"- /AppData/Local/Microsoft/Edge/User Data/*/GPUCache/**",
				"- /AppData/Local/Microsoft/Edge/User Data/*/Service Worker/CacheStorage/**",
				"- /AppData/Local/BraveSoftware/Brave-Browser/User Data/*/Cache/**",
				"- /AppData/Local/Mozilla/Firefox/Profiles/*/cache2/**",
			}},
		{ID: "cloudfolders", Title: "Folders already synced by other cloud apps", DefaultOn: true, Recommended: true,
			Why: "OneDrive, iCloud, Dropbox and Google Drive keep these online already. Backing them up would download every online-only file.",
			Rules: []string{
				"- /OneDrive/**", "- /OneDrive - */**", "- /iCloudDrive/**", "- /iCloud Drive/**",
				"- /Dropbox/**", "- /Google Drive/**", "- /My Drive/**", "- /Box/**",
			}},
		{ID: "devjunk", Title: "Rebuildable developer folders", DefaultOn: false,
			Why: "node_modules, build output and package caches can be downloaded again.",
			Rules: []string{
				"- node_modules/**", "- /.npm/**", "- /.nuget/packages/**", "- /.gradle/caches/**",
				"- /.cargo/registry/**", "- /go/pkg/mod/**", "- /AppData/Local/pip/cache/**",
				"- /AppData/Local/Yarn/Cache/**",
			}},
		{ID: "games", Title: "Installed games", DefaultOn: false,
			Why: "Game stores can reinstall them; they are often hundreds of gigabytes.",
			Rules: []string{
				"- steamapps/common/**", "- steamapps/downloading/**", "- Epic Games/*/**",
				"- /AppData/Local/Packages/Microsoft.GamingApp*/**",
			}},
	}
}

func linuxGroups() []ExcludeGroup {
	return []ExcludeGroup{
		{ID: "system", Title: "Trash and session files", DefaultOn: true, Recommended: true,
			Why: "Deleted files and per-login state that is rebuilt automatically.",
			Rules: []string{
				"- /.local/share/Trash/**", "- .Trash-*/**",
				"- /.Xauthority", "- /.ICEauthority", "- /.xsession-errors*",
				"- /.local/share/recently-used.xbel*", "- /.local/state/wireplumber/**",
				"- /.local/share/gvfs-metadata/**", "- /.gvfs/**", "- /.dbus/**",
				"- *.swp", "- .~lock.*#",
			}},
		{ID: "temp", Title: "Caches and thumbnails", DefaultOn: true, Recommended: true,
			Why: "Programs recreate these; they churn constantly and make every backup slower.",
			Rules: []string{
				"- /.cache/**", "- /.thumbnails/**",
				"- /snap/*/*/.cache/**", "- /.var/app/*/cache/**",
				"- /.local/share/flatpak/repo/**",
			}},
		{ID: "browsers", Title: "Web browser caches", DefaultOn: true, Recommended: true,
			Why: "Downloaded web content; bookmarks, passwords and history are still backed up.",
			Rules: []string{
				"- /.config/google-chrome/*/Service Worker/CacheStorage/**",
				"- /.config/google-chrome/*/GPUCache/**", "- /.config/google-chrome/*/Code Cache/**",
				"- /.config/chromium/*/Service Worker/CacheStorage/**",
				"- /.config/chromium/*/GPUCache/**", "- /.config/chromium/*/Code Cache/**",
				"- /.config/BraveSoftware/*/*/Service Worker/CacheStorage/**",
				"- /.mozilla/firefox/*/cache2/**", "- /.mozilla/firefox/*/startupCache/**",
			}},
		{ID: "cloudfolders", Title: "Folders already synced by other cloud apps", DefaultOn: true, Recommended: true,
			Why:   "Already kept online by their own apps.",
			Rules: []string{"- /Dropbox/**", "- /Nextcloud/**", "- /ownCloud/**", "- /pCloudDrive/**", "- /MEGA/**", "- /Insync/**"}},
		{ID: "devjunk", Title: "Rebuildable developer folders", DefaultOn: false,
			Why: "node_modules, build output and package caches can be downloaded again.",
			Rules: []string{
				"- node_modules/**", "- /.npm/**", "- /.cargo/registry/**", "- /go/pkg/mod/**",
				"- /.gradle/caches/**", "- /.m2/repository/**", "- /.rustup/toolchains/**",
				"- /.local/share/containers/**", "- /.docker/**",
			}},
		{ID: "games", Title: "Installed games", DefaultOn: false,
			Why: "Game stores can reinstall them; they are often hundreds of gigabytes.",
			Rules: []string{
				"- steamapps/common/**", "- steamapps/downloading/**", "- steamapps/shadercache/**",
				"- /.local/share/lutris/runners/**", "- /Games/**",
			}},
	}
}

// androidGroups' rules are relative to shared storage.
func androidGroups() []ExcludeGroup {
	return []ExcludeGroup{
		{ID: "system", Title: "Other apps' private folders", DefaultOn: true, Recommended: true,
			Why:   "Android doesn't let any app read these, so including them only causes errors.",
			Rules: []string{"- /Android/data/**", "- /Android/obb/**"}},
		{ID: "temp", Title: "Thumbnails, caches and recycle bins", DefaultOn: true, Recommended: true,
			Why: "Rebuilt automatically, or files you already deleted.",
			Rules: []string{
				"- .thumbnails/**", "- .thumbdata*", "- .cache/**", "- cache/**",
				"- .trashed-*", "- .pending-*", "- .Trash/**", "- .trash/**", "- /LOST.DIR/**",
				"- *.tmp",
			}},
		{ID: "appmedia", Title: "Other apps' downloaded media", DefaultOn: false,
			Why:   "Messaging and streaming apps keep large copies of media here.",
			Rules: []string{"- /Android/media/**"}},
	}
}

// Groups returns the exclude groups for goos.
func Groups(goos string) []ExcludeGroup {
	switch goos {
	case "windows":
		return windowsGroups()
	case "android":
		return androidGroups()
	}
	return linuxGroups()
}

// Rules builds the filter rule list for the chosen groups, plus "- " rules
// for any of our own mount points that sit inside source (so a home backup
// never descends into a mounted cloud drive). Rules for groups not listed
// in enabled are dropped. Order is stable.
func Rules(goos string, enabled []string, source string, mountPoints []string) []string {
	on := map[string]bool{}
	for _, id := range enabled {
		on[id] = true
	}
	var out []string
	for _, g := range Groups(goos) {
		if on[g.ID] {
			out = append(out, g.Rules...)
		}
	}
	var inside []string
	for _, mp := range mountPoints {
		if rel, ok := within(source, mp, goos); ok {
			inside = append(inside, "- /"+rel+"/**")
		}
	}
	sort.Strings(inside)
	return append(out, inside...)
}

// within reports whether p is strictly inside root, returning the
// slash-separated relative path. Case-insensitive on Windows.
func within(root, p, goos string) (string, bool) {
	if root == "" || p == "" {
		return "", false
	}
	norm := func(s string) string {
		s = strings.ReplaceAll(s, `\`, "/")
		s = strings.TrimRight(s, "/")
		if goos == "windows" {
			s = strings.ToLower(s)
		}
		return s
	}
	r, q := norm(root), norm(p)
	if !strings.HasPrefix(q, r+"/") {
		return "", false
	}
	rel := strings.ReplaceAll(filepath.ToSlash(p), `\`, "/")
	rel = strings.TrimRight(rel, "/")
	return rel[len(r)+1:], true
}
