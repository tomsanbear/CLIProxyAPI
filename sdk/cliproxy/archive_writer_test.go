package cliproxy

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/archive"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

func archiveTestService(authDir, spoolDir string, enabled bool) *Service {
	cfg := &config.Config{AuthDir: authDir}
	cfg.Archive = internalconfig.DefaultArchiveConfig()
	cfg.Archive.Enabled = enabled
	cfg.Archive.SpoolDir = spoolDir
	return &Service{cfg: cfg}
}

func TestServiceArchiveLifecycle(t *testing.T) {
	cases := []struct {
		name       string
		enabled    bool
		explicit   bool
		wantWriter bool
	}{
		{"disabled", false, true, false},
		{"explicit spool-dir", true, true, true},
		{"default spool under auth-dir", true, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			authDir := t.TempDir()
			spoolDir := filepath.Join(authDir, "archive-spool")
			configured := ""
			if tc.explicit {
				spoolDir = filepath.Join(t.TempDir(), "spool")
				configured = spoolDir
			}
			s := archiveTestService(authDir, configured, tc.enabled)
			s.startArchive()
			w := s.ArchiveWriter()
			if (w != nil) != tc.wantWriter {
				t.Fatalf("ArchiveWriter() = %v, want writer=%v", w, tc.wantWriter)
			}
			if w != nil {
				if w.Dir() != spoolDir {
					t.Fatalf("spool = %s, want %s", w.Dir(), spoolDir)
				}
				w.Append(archive.Record{Meta: []byte(`{"v":1,"request_id":"r-1","attempt":1}`), Request: []byte("{}"), Response: []byte("{}")})
			}
			if err := s.stopArchive(); err != nil {
				t.Fatalf("stopArchive: %v", err)
			}
			if s.ArchiveWriter() != nil {
				t.Fatal("writer still exposed after stop")
			}
			_, errStat := os.Stat(filepath.Join(spoolDir, "seg-000000000001.wal"))
			if tc.wantWriter && errStat != nil {
				t.Fatalf("stop did not roll the open segment: %v", errStat)
			}
			if !tc.wantWriter && !os.IsNotExist(errStat) {
				t.Fatalf("disabled archive touched the spool: %v", errStat)
			}
		})
	}
}

func TestServiceArchiveLockedSpoolRunsWithoutArchiving(t *testing.T) {
	spoolDir := t.TempDir()
	holder, err := archive.Open(archive.Options{Dir: spoolDir, SegmentMaxBytes: 1 << 20, SegmentMaxAge: time.Minute, RingSegments: 2, MaxRecordBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if errClose := holder.Close(); errClose != nil {
			t.Errorf("close lock holder: %v", errClose)
		}
	}()
	s := archiveTestService(t.TempDir(), spoolDir, true)
	s.startArchive()
	if s.ArchiveWriter() != nil {
		t.Fatal("a second writer must not start on a locked spool")
	}
	if err = s.stopArchive(); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveTickInterval(t *testing.T) {
	for _, tc := range []struct {
		maxAge, want time.Duration
	}{
		{15 * time.Minute, time.Minute},
		{2 * time.Minute, 30 * time.Second},
		{2 * time.Second, time.Second},
	} {
		if got := archiveTickInterval(tc.maxAge); got != tc.want {
			t.Errorf("archiveTickInterval(%s) = %s, want %s", tc.maxAge, got, tc.want)
		}
	}
}
