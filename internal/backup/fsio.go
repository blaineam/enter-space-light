package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/cache"
	"github.com/rclone/rclone/fs/filter"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/fs/operations"
	"github.com/rclone/rclone/fs/walk"
)

// Listing caps, as the Apple app's recorder: past either the tree is
// recorded as partial rather than taking unbounded time.
const (
	maxLiveEntries   = 400_000
	liveListBudget   = 15 * time.Minute
	versionedBudget  = 5 * time.Minute
	errListTruncated = "listing stopped early"
)

var errStop = errors.New(errListTruncated)

// unfiltered returns ctx with no filter rules: listings of the destination
// must see everything, never the job's include/exclude rules.
func unfiltered(ctx context.Context) context.Context {
	fi, _ := filter.NewFilter(nil)
	return filter.ReplaceConfig(ctx, fi)
}

// listFiles lists every file under fsStr recursively. complete is false
// if a cap or budget cut it short. A missing directory is an empty,
// complete listing.
func listFiles(ctx context.Context, fsStr string, limit int, budget time.Duration) (entries []Entry, complete bool, err error) {
	ctx = unfiltered(ctx)
	f, err := cache.Get(ctx, fsStr)
	if err != nil && !errors.Is(err, fs.ErrorIsFile) {
		if errors.Is(err, fs.ErrorDirNotFound) {
			return nil, true, nil
		}
		return nil, false, err
	}
	ht := hash.None
	if !f.Features().SlowHash {
		ht = f.Hashes().GetOne()
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	complete = true
	err = walk.ListR(ctx, f, "", true, -1, walk.ListObjects, func(des fs.DirEntries) error {
		for _, de := range des {
			o, ok := de.(fs.Object)
			if !ok || IsControlPath(o.Remote()) {
				continue
			}
			en := Entry{Path: o.Remote(), Size: o.Size(), ModTime: T(o.ModTime(ctx)), Presence: Present}
			if ht != hash.None {
				if h, herr := o.Hash(ctx, ht); herr == nil && h != "" {
					kind := ht.String()
					en.Hash, en.HashKind = &h, &kind
				}
			}
			entries = append(entries, en)
			if limit > 0 && len(entries) >= limit {
				return errStop
			}
		}
		return nil
	})
	switch {
	case errors.Is(err, errStop), errors.Is(err, context.DeadlineExceeded):
		return entries, false, nil
	case errors.Is(err, fs.ErrorDirNotFound):
		return nil, true, nil
	case err != nil:
		return entries, false, err
	}
	return entries, complete, nil
}

// listDirNames returns the immediate subdirectory names of fsStr.
func listDirNames(ctx context.Context, fsStr string) ([]string, error) {
	ctx = unfiltered(ctx)
	f, err := cache.Get(ctx, fsStr)
	if err != nil && !errors.Is(err, fs.ErrorIsFile) {
		if errors.Is(err, fs.ErrorDirNotFound) {
			return nil, nil
		}
		return nil, err
	}
	des, err := f.List(ctx, "")
	if errors.Is(err, fs.ErrorDirNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, de := range des {
		if _, ok := de.(fs.Directory); ok {
			out = append(out, path.Base(de.Remote()))
		}
	}
	return out, nil
}

// listFileNames returns the immediate file names of fsStr.
func listFileNames(ctx context.Context, fsStr string) ([]string, error) {
	ctx = unfiltered(ctx)
	f, err := cache.Get(ctx, fsStr)
	if err != nil && !errors.Is(err, fs.ErrorIsFile) {
		if errors.Is(err, fs.ErrorDirNotFound) {
			return nil, nil
		}
		return nil, err
	}
	des, err := f.List(ctx, "")
	if errors.Is(err, fs.ErrorDirNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, de := range des {
		if _, ok := de.(fs.Object); ok {
			out = append(out, path.Base(de.Remote()))
		}
	}
	return out, nil
}

// splitFile turns "remote:dir/file" into ("remote:dir", "file").
func splitFile(fsFile string) (string, string) {
	if isLocal(fsFile) && !strings.HasPrefix(fsFile, ":") {
		// Local, including Windows "D:\dir\file" and portable "/D:/dir/file".
		clean := strings.TrimRight(fsFile, `/\`)
		i := strings.LastIndexAny(clean, `/\`)
		if i < 0 {
			return ".", clean
		}
		dir := clean[:i]
		if dir == "" || (len(dir) == 2 && dir[1] == ':') {
			dir = clean[:i+1]
		}
		return dir, clean[i+1:]
	}
	r, p := FsSplit(fsFile)
	p = strings.TrimSuffix(p, "/")
	dir, leaf := path.Split(p)
	dir = strings.TrimSuffix(dir, "/")
	if r == "" {
		if dir == "" {
			dir = "/"
		}
		return dir, leaf
	}
	return r + ":" + dir, leaf
}

func readFile(ctx context.Context, dirFs, name string) ([]byte, error) {
	f, err := cache.Get(ctx, dirFs)
	if err != nil && !errors.Is(err, fs.ErrorIsFile) {
		return nil, err
	}
	o, err := f.NewObject(ctx, name)
	if err != nil {
		return nil, err
	}
	rc, err := o.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(io.LimitReader(rc, 1<<30))
}

func writeFile(ctx context.Context, dirFs, name string, data []byte) error {
	f, err := cache.Get(ctx, dirFs)
	if err != nil && !errors.Is(err, fs.ErrorIsFile) && !errors.Is(err, fs.ErrorDirNotFound) {
		return err
	}
	_, err = operations.Rcat(unfiltered(ctx), f, name, io.NopCloser(bytes.NewReader(data)), time.Now(), nil)
	return err
}

func purge(ctx context.Context, dirFs string) error {
	f, err := cache.Get(ctx, dirFs)
	if err != nil && !errors.Is(err, fs.ErrorIsFile) {
		return err
	}
	return operations.Purge(unfiltered(ctx), f, "")
}

// count returns the number of files under fsStr that pass rules.
func count(ctx context.Context, fsStr string, rules []string) (int64, error) {
	opt := filter.Opt // rclone's defaults; a zero Options has invalid age limits
	opt.FilterRule = append([]string(nil), rules...)
	fi, err := filter.NewFilter(&opt)
	if err != nil {
		return 0, err
	}
	ctx = filter.ReplaceConfig(ctx, fi)
	f, err := cache.Get(ctx, fsStr)
	if err != nil && !errors.Is(err, fs.ErrorIsFile) {
		if errors.Is(err, fs.ErrorDirNotFound) {
			return 0, nil
		}
		return 0, err
	}
	n, _, _, err := operations.Count(ctx, f)
	if errors.Is(err, fs.ErrorDirNotFound) {
		return 0, nil
	}
	return n, err
}

// copyFile copies srcFile ("remote:dir/file") to dstRoot/dstRel and checks
// the size arrived intact.
func copyFile(ctx context.Context, srcFile, dstRoot, dstRel string, wantSize int64) error {
	ctx = unfiltered(ctx)
	sDir, sLeaf := splitFile(srcFile)
	fsrc, err := cache.Get(ctx, sDir)
	if err != nil && !errors.Is(err, fs.ErrorIsFile) {
		return err
	}
	fdst, err := cache.Get(ctx, dstRoot)
	if err != nil && !errors.Is(err, fs.ErrorIsFile) && !errors.Is(err, fs.ErrorDirNotFound) {
		return err
	}
	if err := operations.CopyFile(ctx, fdst, fsrc, dstRel, sLeaf); err != nil {
		return err
	}
	o, err := fdst.NewObject(ctx, dstRel)
	if err != nil {
		return err
	}
	if wantSize >= 0 && o.Size() != wantSize {
		return errors.New("restored size doesn't match the backup")
	}
	return nil
}

// exists reports whether dstRoot/rel is a file.
func exists(ctx context.Context, dstRoot, rel string) bool {
	f, err := cache.Get(unfiltered(ctx), dstRoot)
	if err != nil && !errors.Is(err, fs.ErrorIsFile) {
		return false
	}
	_, err = f.NewObject(ctx, rel)
	return err == nil
}
