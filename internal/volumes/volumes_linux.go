//go:build linux

package volumes

import (
	"os"
	"path/filepath"
	"strings"
)

// List reads /proc/self/mountinfo and keeps real block-device filesystems
// mounted where desktops put user drives (/media, /run/media, /mnt) plus
// anything mounted under the user's home.
func List() ([]Volume, error) {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	return parseMountinfo(string(b), home, byUUID(), byLabel(), removable), nil
}

// parseMountinfo is pure for tests. uuids/labels map a resolved device path
// (/dev/sdb1) to its UUID/label.
func parseMountinfo(s, home string, uuids, labels map[string]string, isRemovable func(dev string) bool) []Volume {
	var out []Volume
	seen := map[string]bool{}
	for _, line := range strings.Split(s, "\n") {
		// 36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw,errors=continue
		pre, post, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		pf := strings.Fields(pre)
		qf := strings.Fields(post)
		if len(pf) < 5 || len(qf) < 2 {
			continue
		}
		mnt := unescape(pf[4])
		fstype, src := qf[0], qf[1]
		if !strings.HasPrefix(src, "/dev/") || strings.HasPrefix(fstype, "fuse") {
			continue
		}
		system := mnt == "/" || mnt == "/boot" || mnt == "/boot/efi" || mnt == "/home"
		userPlace := strings.HasPrefix(mnt, "/media/") || strings.HasPrefix(mnt, "/run/media/") ||
			strings.HasPrefix(mnt, "/mnt/") || (home != "" && strings.HasPrefix(mnt, home+"/"))
		if !userPlace && !system {
			continue
		}
		if seen[mnt] {
			continue
		}
		seen[mnt] = true
		dev := src
		if r, err := filepath.EvalSymlinks(src); err == nil {
			dev = r
		}
		v := Volume{
			ID:        uuids[dev],
			Label:     labels[dev],
			Root:      mnt,
			System:    system,
			Removable: !system && isRemovable(dev),
		}
		if v.Label == "" {
			v.Label = filepath.Base(mnt)
		}
		out = append(out, v)
	}
	return out
}

// unescape reverses mountinfo's octal escaping (\040 = space).
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			n := 0
			ok := true
			for _, c := range s[i+1 : i+4] {
				if c < '0' || c > '7' {
					ok = false
					break
				}
				n = n*8 + int(c-'0')
			}
			if ok {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func linkMap(dir string) map[string]string {
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if r, err := filepath.EvalSymlinks(p); err == nil {
			out[r] = unescape(strings.ReplaceAll(e.Name(), `\x20`, " "))
		}
	}
	return out
}

func byUUID() map[string]string  { return linkMap("/dev/disk/by-uuid") }
func byLabel() map[string]string { return linkMap("/dev/disk/by-label") }

// removable checks sysfs: the disk's removable flag, or a USB/MMC parent.
func removable(dev string) bool {
	name := filepath.Base(dev) // sdb1
	sys, err := filepath.EvalSymlinks("/sys/class/block/" + name)
	if err != nil {
		return false
	}
	if strings.Contains(sys, "/usb") || strings.Contains(sys, "/mmc") {
		return true
	}
	// Partition → parent disk holds the flag.
	for _, p := range []string{filepath.Join(sys, "removable"), filepath.Join(filepath.Dir(sys), "removable")} {
		if b, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(b)) == "1" {
			return true
		}
	}
	return false
}
