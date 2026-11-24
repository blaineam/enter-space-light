//go:build linux

package volumes

import "testing"

func TestParseMountinfo(t *testing.T) {
	info := `22 1 8:2 / / rw,relatime shared:1 - ext4 /dev/sda2 rw
30 22 8:17 / /media/me/My\040Drive rw,nosuid shared:2 - exfat /dev/sdb1 rw
31 22 0:50 / /home/me/Cloud/Drive rw,nosuid - fuse.rclone gdrive: rw
32 22 0:22 / /proc rw - proc proc rw
33 22 8:33 / /run/media/me/CARD rw - vfat /dev/sdc1 rw
`
	uuids := map[string]string{"/dev/sda2": "root-uuid", "/dev/sdb1": "1234-ABCD", "/dev/sdc1": "CARD-0001"}
	labels := map[string]string{"/dev/sdb1": "My Drive"}
	vols := parseMountinfo(info, "/home/me", uuids, labels, func(dev string) bool { return dev != "/dev/sda2" })
	if len(vols) != 3 {
		t.Fatalf("want root + 2 user drives, got %+v", vols)
	}
	if !vols[0].System || vols[0].Removable {
		t.Fatalf("root is the system volume: %+v", vols[0])
	}
	if vols[1].Root != "/media/me/My Drive" || vols[1].ID != "1234-ABCD" || vols[1].Label != "My Drive" || !vols[1].Removable {
		t.Fatalf("escaped mount path / uuid / label wrong: %+v", vols[1])
	}
	if vols[2].Label != "CARD" {
		t.Fatalf("label falls back to the folder name: %+v", vols[2])
	}
}
