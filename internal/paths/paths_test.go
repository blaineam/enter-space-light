package paths

import (
	"path/filepath"
	"testing"
)

// A sandbox (ESL_HOME) must never use the user's real rclone.conf, even
// when RCLONE_CONFIG is set in their shell.
func TestSandboxConfigBeatsRcloneConfigEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RCLONE_CONFIG", "/real/user/rclone.conf")
	t.Setenv("ESL_HOME", home)
	if got := RcloneConfig(); got != filepath.Join(home, "rclone.conf") {
		t.Fatalf("sandbox leaked to %q", got)
	}
	for _, d := range []string{ConfigDir(), StateDir(), CacheDir(), LogDir()} {
		if rel, err := filepath.Rel(home, d); err != nil || rel == ".." || filepath.IsAbs(rel) || rel[:2] == ".." {
			t.Fatalf("%s escapes the sandbox", d)
		}
	}
	t.Setenv("ESL_HOME", "")
	if got := RcloneConfig(); got != "/real/user/rclone.conf" {
		t.Fatalf("RCLONE_CONFIG ignored outside a sandbox: %q", got)
	}
}

func TestSandboxMountRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ESL_HOME", home)
	if got := MountRoot(""); got != filepath.Join(home, "mnt") {
		t.Fatalf("sandbox mounts escape to %q", got)
	}
	if got := MountRoot("/srv/cloud"); got != "/srv/cloud" {
		t.Fatalf("explicit preference ignored: %q", got)
	}
	t.Setenv("ESL_HOME", "")
	if got := MountRoot(""); filepath.Base(got) != "Cloud" {
		t.Fatalf("default mount root %q", got)
	}
}
