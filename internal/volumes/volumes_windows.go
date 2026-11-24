//go:build windows

package volumes

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// List enumerates drive letters with a filesystem. Network drives (which
// includes our own WinFsp network-mode mounts), optical drives and empty
// card readers are skipped.
func List() ([]Volume, error) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil, err
	}
	sysDrive := strings.ToUpper(os.Getenv("SystemDrive")) // "C:"
	var out []Volume
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		rootp, _ := windows.UTF16PtrFromString(root)
		typ := windows.GetDriveType(rootp)
		if typ != windows.DRIVE_REMOVABLE && typ != windows.DRIVE_FIXED {
			continue
		}
		var serial uint32
		label := make([]uint16, windows.MAX_PATH+1)
		fsName := make([]uint16, windows.MAX_PATH+1)
		// Fails for an empty card reader slot: skip it.
		if err := windows.GetVolumeInformation(rootp, &label[0], uint32(len(label)), &serial, nil, nil, &fsName[0], uint32(len(fsName))); err != nil {
			continue
		}
		v := Volume{
			ID:     fmt.Sprintf("%04X-%04X", serial>>16, serial&0xffff),
			Label:  windows.UTF16ToString(label),
			Root:   root,
			System: strings.EqualFold(root[:2], sysDrive),
		}
		v.Removable = typ == windows.DRIVE_REMOVABLE || isUSB(root[:2])
		if v.Label == "" {
			v.Label = "Local Disk (" + root[:2] + ")"
		}
		out = append(out, v)
	}
	return out, nil
}

// isUSB asks the storage stack for the bus type, because USB hard drives
// and many fast USB sticks report DRIVE_FIXED, not DRIVE_REMOVABLE.
func isUSB(letter string) bool {
	path, _ := windows.UTF16PtrFromString(`\\.\` + letter)
	h, err := windows.CreateFile(path, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()

	// STORAGE_PROPERTY_QUERY { PropertyId=StorageDeviceProperty(0), QueryType=PropertyStandardQuery(0) }
	query := [3]uint32{0, 0, 0}
	var desc [1024]byte
	var ret uint32
	const ioctlStorageQueryProperty = 0x2D1400
	if err := windows.DeviceIoControl(h, ioctlStorageQueryProperty,
		(*byte)(unsafe.Pointer(&query[0])), uint32(unsafe.Sizeof(query)),
		&desc[0], uint32(len(desc)), &ret, nil); err != nil {
		return false
	}
	// STORAGE_DEVICE_DESCRIPTOR: BusType is the DWORD at offset 28.
	if ret < 32 {
		return false
	}
	busType := *(*uint32)(unsafe.Pointer(&desc[28]))
	const busTypeUsb, busTypeSd, busTypeMmc = 7, 12, 13
	return busType == busTypeUsb || busType == busTypeSd || busType == busTypeMmc
}
