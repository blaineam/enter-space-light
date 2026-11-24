package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validDoc() Settings {
	return Settings{
		Version: CurrentVersion,
		Mounts: []Mount{
			{ID: "m1", Name: "Drive", Remote: "gdrive:", AutoMount: true},
			{ID: "m2", Name: "NAS", Remote: "nas:/srv", MountPoint: "N:"},
		},
		Jobs: []Job{
			{ID: "j1", Name: "Photos", Kind: KindCopy, Enabled: true, Source: "/home/u/Pictures", Dest: "b2:photos",
				Triggers: Triggers{Every: "1h", Daily: "02:30", OnStart: true}, Filters: []string{"- *.tmp", "+ **"}},
			{ID: "j2", Name: "USB", Kind: KindSync, Enabled: true, Source: "gdrive:Docs", Dest: "{volume}/Docs",
				Triggers: Triggers{OnVolume: &VolumeMatch{ID: "ABCD-1234"}}},
		},
	}
}

func goodBackup() *BackupOptions {
	return &BackupOptions{ID: "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE", Namespace: "Backup-PC", RetentionDays: 30}
}

func TestValidateAcceptsAGoodDocument(t *testing.T) {
	d := validDoc()
	d.Jobs[0].Backup = goodBackup()
	if err := d.Validate(); err != nil {
		t.Fatalf("valid document rejected: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*Settings){
		"duplicate id":             func(s *Settings) { s.Jobs[0].ID = "m1" },
		"no remote colon":          func(s *Settings) { s.Mounts[0].Remote = "gdrive" },
		"bad name char":            func(s *Settings) { s.Mounts[0].Name = "a/b" },
		"same mount point":         func(s *Settings) { s.Mounts[0].MountPoint = "n:\\" },
		"short interval":           func(s *Settings) { s.Jobs[0].Triggers.Every = "30s" },
		"bad interval":             func(s *Settings) { s.Jobs[0].Triggers.Every = "often" },
		"bad daily":                func(s *Settings) { s.Jobs[0].Triggers.Daily = "25:00" },
		"unknown kind":             func(s *Settings) { s.Jobs[0].Kind = "mirror" },
		"same src dst":             func(s *Settings) { s.Jobs[0].Dest = s.Jobs[0].Source },
		"volume without trigger":   func(s *Settings) { s.Jobs[1].Triggers.OnVolume = nil },
		"volume with interval":     func(s *Settings) { s.Jobs[1].Triggers.Every = "1h" },
		"empty volume match":       func(s *Settings) { s.Jobs[1].Triggers.OnVolume = &VolumeMatch{} },
		"filter without sign":      func(s *Settings) { s.Jobs[0].Filters = []string{"*.tmp"} },
		"option with dashes":       func(s *Settings) { s.Mounts[0].Options = map[string]string{"--vfs-cache-mode": "full"} },
		"missing job name":         func(s *Settings) { s.Jobs[0].Name = " " },
		"missing job destination":  func(s *Settings) { s.Jobs[0].Dest = "" },
		"missing mount identifier": func(s *Settings) { s.Mounts[1].ID = "" },
		"bisync backup":            func(s *Settings) { s.Jobs[0].Kind = KindBisync; s.Jobs[0].Backup = goodBackup() },
		"backup bad uuid":          func(s *Settings) { b := goodBackup(); b.ID = "nope"; s.Jobs[0].Backup = b },
		"backup lowercase uuid": func(s *Settings) {
			b := goodBackup()
			b.ID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
			s.Jobs[0].Backup = b
		},
		"backup slash namespace": func(s *Settings) { b := goodBackup(); b.Namespace = "a/b"; s.Jobs[0].Backup = b },
		"backup to remote root":  func(s *Settings) { s.Jobs[0].Dest = "b2:"; s.Jobs[0].Backup = goodBackup() },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := validDoc()
			mutate(&d)
			if err := d.Validate(); err == nil {
				t.Fatalf("expected rejection")
			}
		})
	}
}

func TestStoreRoundTripAndAtomicUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Get(); len(got.Mounts) != 0 || got.Mounts == nil {
		t.Fatalf("fresh store should have an empty, non-nil mount list: %#v", got.Mounts)
	}
	if _, err := s.Update(func(d *Settings) error { *d = validDoc(); return nil }); err != nil {
		t.Fatal(err)
	}
	// An invalid update must neither be saved nor published.
	if _, err := s.Update(func(d *Settings) error { d.Mounts[0].Remote = "nope"; return nil }); err == nil {
		t.Fatal("invalid update accepted")
	}
	if s.Get().Mounts[0].Remote != "gdrive:" {
		t.Fatal("invalid update leaked into memory")
	}
	re, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := re.Get(); len(got.Jobs) != 2 || got.Jobs[1].Triggers.OnVolume.ID != "ABCD-1234" {
		t.Fatalf("round trip lost data: %#v", got)
	}
	// No temp files left behind.
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestGetIsADeepCopy(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "s.json"))
	_, _ = s.Update(func(d *Settings) error { *d = validDoc(); return nil })
	got := s.Get()
	got.Mounts[0].Name = "mutated"
	got.Jobs[1].Triggers.OnVolume.ID = "mutated"
	again := s.Get()
	if again.Mounts[0].Name == "mutated" || again.Jobs[1].Triggers.OnVolume.ID == "mutated" {
		t.Fatal("Get returned shared memory")
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	if err := os.WriteFile(path, []byte(`{"version": 99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("newer schema accepted")
	}
}
