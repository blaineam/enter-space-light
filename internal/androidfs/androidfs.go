// Package androidfs is how drives work on Android, which has no user-space
// mounts without root. It registers as rclone's "mount" implementation, so
// mount/mount, health checks, tuning and unmounting behave exactly as on the
// desktop, but instead of attaching to a folder it keeps the live VFS here.
// The app's DocumentsProvider serves it to the Files app and every other
// app's file picker.
//
// The package has no Android code in it, so it is tested on any machine.
package androidfs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/rclone/rclone/cmd/mountlib"
	"github.com/rclone/rclone/fs/rc"
	"github.com/rclone/rclone/vfs"
)

// Prefix starts every Android "mount point": Prefix + the drive's ID.
const Prefix = "esl://"

// ErrNotConnected is returned for a drive that isn't connected.
var ErrNotConnected = errors.New("this drive isn't connected")

var (
	mu     sync.RWMutex
	drives = map[string]*vfs.VFS{}

	handlesMu  sync.Mutex
	handles    = map[int64]*open{}
	nextHandle int64

	registerOnce sync.Once
)

type open struct {
	drive string
	h     vfs.Handle
}

// Register makes this package rclone's mount implementation. Only call it
// where no FUSE mount is linked in (Android, and tests).
func Register() {
	registerOnce.Do(func() { mountlib.AddRc("mount", mount) })
}

func mount(v *vfs.VFS, mountpoint string, _ *mountlib.Options) (<-chan error, func() error, string, error) {
	id, ok := strings.CutPrefix(mountpoint, Prefix)
	if !ok || id == "" {
		return nil, nil, "", fmt.Errorf("not an Android drive: %q", mountpoint)
	}
	// Fail now, with a real reason, if the remote can't be listed.
	if _, err := v.ReadDir(""); err != nil {
		return nil, nil, "", fmt.Errorf("listing the remote: %w", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if _, dup := drives[id]; dup {
		return nil, nil, "", fmt.Errorf("drive %s is already connected", id)
	}
	drives[id] = v
	done := make(chan error, 1)
	var once sync.Once
	unmount := func() error {
		once.Do(func() {
			// Close open files first so their writes reach the cache and
			// are queued for upload before the VFS shuts down.
			closeDrive(id)
			mu.Lock()
			if drives[id] == v {
				delete(drives, id)
			}
			mu.Unlock()
			done <- nil
		})
		return nil
	}
	return done, unmount, "", nil
}

func lookup(drive string) (*vfs.VFS, error) {
	mu.RLock()
	defer mu.RUnlock()
	v, ok := drives[drive]
	if !ok {
		return nil, ErrNotConnected
	}
	return v, nil
}

// Connected returns the IDs of connected drives, sorted.
func Connected() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(drives))
	for id := range drives {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Healthy checks that a drive's root can be listed.
func Healthy(drive string) error {
	v, err := lookup(drive)
	if err != nil {
		return err
	}
	_, err = v.ReadDir("")
	return err
}

// Entry is one file or folder.
type Entry struct {
	Name     string `json:"name"`
	Path     string `json:"path"` // relative to the drive root; "" is the root
	Dir      bool   `json:"dir"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"` // Unix milliseconds
}

// Clean turns a document path into the VFS form: no leading or trailing
// slash, "" for the root, and no way out of the drive.
func Clean(p string) string {
	return strings.TrimPrefix(path.Clean("/"+p), "/")
}

func entry(p string, fi os.FileInfo) Entry {
	e := Entry{Name: fi.Name(), Path: p, Dir: fi.IsDir(), Modified: fi.ModTime().UnixMilli()}
	if !e.Dir {
		e.Size = fi.Size()
	}
	return e
}

// Stat describes one path.
func Stat(drive, p string) (Entry, error) {
	v, err := lookup(drive)
	if err != nil {
		return Entry{}, err
	}
	p = Clean(p)
	n, err := v.Stat(p)
	if err != nil {
		return Entry{}, err
	}
	e := entry(p, n)
	if p == "" {
		e.Name = ""
	}
	return e, nil
}

// List returns a folder's contents: folders first, then by name.
func List(drive, dir string) ([]Entry, error) {
	v, err := lookup(drive)
	if err != nil {
		return nil, err
	}
	dir = Clean(dir)
	infos, err := v.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(infos))
	for _, fi := range infos {
		out = append(out, entry(path.Join(dir, fi.Name()), fi))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// Space returns the drive's total and free bytes; -1 where unknown.
func Space(drive string) (total, free int64, err error) {
	v, err := lookup(drive)
	if err != nil {
		return -1, -1, err
	}
	total, _, free = v.Statfs()
	return total, free, nil
}

func checkName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
		return fmt.Errorf("%q isn't a usable name", name)
	}
	return nil
}

func exists(v *vfs.VFS, p string) (bool, error) {
	_, err := v.Stat(p)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, vfs.ENOENT) || errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return false, err
}

// freeName returns name, or "name (2).ext" and so on if it is taken.
func freeName(v *vfs.VFS, dir, name string) (string, error) {
	ext := path.Ext(name)
	if ext == name { // ".profile" has no extension
		ext = ""
	}
	stem := strings.TrimSuffix(name, ext)
	candidate := name
	for i := 2; i < 1000; i++ {
		taken, err := exists(v, path.Join(dir, candidate))
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s (%d)%s", stem, i, ext)
	}
	return "", fmt.Errorf("too many files called %q", name)
}

// Create makes an empty file or a folder in dir and returns its path. It
// never replaces anything: a taken name gets a number.
func Create(drive, dir, name string, folder bool) (string, error) {
	v, err := lookup(drive)
	if err != nil {
		return "", err
	}
	if err := checkName(name); err != nil {
		return "", err
	}
	dir = Clean(dir)
	name, err = freeName(v, dir, name)
	if err != nil {
		return "", err
	}
	p := path.Join(dir, name)
	if folder {
		return p, v.Mkdir(p, 0o777)
	}
	h, err := v.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return "", err
	}
	return p, h.Close()
}

// Delete removes a file, or a folder and everything in it.
func Delete(drive, p string) error {
	v, err := lookup(drive)
	if err != nil {
		return err
	}
	if p = Clean(p); p == "" {
		return errors.New("a whole drive can't be deleted")
	}
	n, err := v.Stat(p)
	if err != nil {
		return err
	}
	return n.RemoveAll()
}

func move(drive, from, to string) (string, error) {
	v, err := lookup(drive)
	if err != nil {
		return "", err
	}
	if from == "" {
		return "", errors.New("a whole drive can't be moved or renamed")
	}
	if to == from {
		return to, nil
	}
	if strings.HasPrefix(to, from+"/") {
		return "", errors.New("a folder can't be moved into itself")
	}
	// A change of capitals only is the same file on some remotes.
	if !strings.EqualFold(from, to) {
		taken, err := exists(v, to)
		if err != nil {
			return "", err
		}
		if taken {
			return "", fmt.Errorf("%q already exists", path.Base(to))
		}
	}
	return to, v.Rename(from, to)
}

// Rename gives p a new name in the same folder, refusing to replace anything.
func Rename(drive, p, name string) (string, error) {
	if err := checkName(name); err != nil {
		return "", err
	}
	p = Clean(p)
	return move(drive, p, Clean(path.Join(path.Dir(p), name)))
}

// Move puts p into the folder newDir, refusing to replace anything.
func Move(drive, p, newDir string) (string, error) {
	p = Clean(p)
	return move(drive, p, path.Join(Clean(newDir), path.Base(p)))
}

// Flags maps an Android open mode to os flags. "w" truncates: apps that
// save a shorter file with "w" expect no leftover bytes.
func Flags(mode string) (int, error) {
	switch mode {
	case "r":
		return os.O_RDONLY, nil
	case "w", "wt":
		return os.O_WRONLY | os.O_CREATE | os.O_TRUNC, nil
	case "wa":
		return os.O_WRONLY | os.O_CREATE, nil
	case "rw":
		return os.O_RDWR | os.O_CREATE, nil
	case "rwt":
		return os.O_RDWR | os.O_CREATE | os.O_TRUNC, nil
	}
	return 0, fmt.Errorf("unknown open mode %q", mode)
}

// Open opens a file and returns a handle for ReadAt, WriteAt and Close.
func Open(drive, p, mode string) (int64, error) {
	v, err := lookup(drive)
	if err != nil {
		return 0, err
	}
	flags, err := Flags(mode)
	if err != nil {
		return 0, err
	}
	h, err := v.OpenFile(Clean(p), flags, 0o666)
	if err != nil {
		return 0, err
	}
	handlesMu.Lock()
	defer handlesMu.Unlock()
	nextHandle++
	handles[nextHandle] = &open{drive: drive, h: h}
	return nextHandle, nil
}

func handle(id int64) (vfs.Handle, error) {
	handlesMu.Lock()
	defer handlesMu.Unlock()
	o, ok := handles[id]
	if !ok {
		return nil, errors.New("the file is closed")
	}
	return o.h, nil
}

// Size is the open file's current length.
func Size(id int64) (int64, error) {
	h, err := handle(id)
	if err != nil {
		return 0, err
	}
	fi, err := h.Stat()
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// ReadAt fills buf from offset, returning fewer bytes only at end of file.
func ReadAt(id int64, buf []byte, offset int64) (int, error) {
	h, err := handle(id)
	if err != nil {
		return 0, err
	}
	n := 0
	for n < len(buf) {
		m, err := h.ReadAt(buf[n:], offset+int64(n))
		n += m
		if errors.Is(err, io.EOF) {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		if m == 0 {
			break
		}
	}
	return n, nil
}

// WriteAt writes buf at offset.
func WriteAt(id int64, buf []byte, offset int64) (int, error) {
	h, err := handle(id)
	if err != nil {
		return 0, err
	}
	return h.WriteAt(buf, offset)
}

// Sync flushes an open file to the local cache.
func Sync(id int64) error {
	h, err := handle(id)
	if err != nil {
		return err
	}
	return h.Sync()
}

// Close closes a handle. Changed files are then uploaded in the background.
// Closing a closed handle does nothing.
func Close(id int64) error {
	handlesMu.Lock()
	o, ok := handles[id]
	delete(handles, id)
	handlesMu.Unlock()
	if !ok {
		return nil
	}
	return o.h.Close()
}

// OpenCount is how many files other apps have open.
func OpenCount() int {
	handlesMu.Lock()
	defer handlesMu.Unlock()
	return len(handles)
}

func closeDrive(drive string) {
	handlesMu.Lock()
	var closing []vfs.Handle
	for id, o := range handles {
		if o.drive == drive {
			closing = append(closing, o.h)
			delete(handles, id)
		}
	}
	handlesMu.Unlock()
	for _, h := range closing {
		_ = h.Close()
	}
}

// PendingUploads counts files still waiting to reach a remote, across all
// drives. The app stays awake while this isn't zero.
func PendingUploads() int {
	mu.RLock()
	all := make([]*vfs.VFS, 0, len(drives))
	for _, v := range drives {
		all = append(all, v)
	}
	mu.RUnlock()
	n := 0
	for _, v := range all {
		dc, ok := v.Stats()["diskCache"].(rc.Params)
		if !ok {
			continue
		}
		for _, k := range []string{"uploadsInProgress", "uploadsQueued"} {
			c, err := dc.GetInt64(k)
			if err == nil {
				n += int(c)
			}
		}
	}
	return n
}
