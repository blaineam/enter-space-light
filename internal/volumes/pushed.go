package volumes

import "sync"

// Android apps can't read the mount table usefully; the app asks
// StorageManager and hands the list over whenever it changes.
var (
	pushedMu   sync.Mutex
	pushedList []Volume
)

// SetAndroid replaces the list of storage volumes.
func SetAndroid(v []Volume) {
	pushedMu.Lock()
	defer pushedMu.Unlock()
	pushedList = append([]Volume(nil), v...)
}

func pushed() []Volume {
	pushedMu.Lock()
	defer pushedMu.Unlock()
	return append([]Volume(nil), pushedList...)
}
