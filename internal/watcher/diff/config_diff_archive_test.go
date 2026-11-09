package diff

import (
	"reflect"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestBuildConfigChangeDetailsArchive(t *testing.T) {
	const (
		oldSecret = "old-secret-access-key-value"
		newSecret = "new-secret-access-key-value"
		oldSSEC   = "b2xkLXNzZS1jLWtleS12YWx1ZS0wMDAwMDAwMDAwMDA="
		newSSEC   = "bmV3LXNzZS1jLWtleS12YWx1ZS0wMDAwMDAwMDAwMDA="
		newKeyID  = "new-access-key-id-value"
	)
	oldCfg := &config.Config{Archive: config.DefaultArchiveConfig()}
	oldCfg.Archive.R2 = config.ArchiveR2Config{
		Endpoint:        "https://old.r2.cloudflarestorage.com",
		Bucket:          "old-bucket",
		SecretAccessKey: oldSecret,
		SSECKey:         oldSSEC,
	}
	newCfg := &config.Config{Archive: config.DefaultArchiveConfig()}
	newCfg.Archive.Enabled = true
	newCfg.Archive.SpoolDir = "/var/spool/archive"
	newCfg.Archive.SegmentMaxBytes = 1 << 20
	newCfg.Archive.SegmentMaxAge = "5m"
	newCfg.Archive.RingSegments = 4
	newCfg.Archive.MaxRecordBytes = 2 << 20
	newCfg.Archive.R2 = config.ArchiveR2Config{
		Endpoint:        "https://new.r2.cloudflarestorage.com/path",
		Bucket:          "new-bucket",
		Prefix:          "v1",
		AccessKeyID:     newKeyID,
		SecretAccessKey: newSecret,
	}

	changes := BuildConfigChangeDetails(oldCfg, newCfg)
	want := []string{
		"archive.enabled: false -> true (applies on restart)",
		"archive.spool-dir: <none> -> /var/spool/archive (applies on restart)",
		"archive.segment-max-bytes: 67108864 -> 1048576 (applies on restart)",
		"archive.segment-max-age: 15m -> 5m (applies on restart)",
		"archive.ring-segments: 16 -> 4 (applies on restart)",
		"archive.max-record-bytes: 67108864 -> 2097152 (applies on restart)",
		"archive.r2.endpoint: https://old.r2.cloudflarestorage.com -> https://new.r2.cloudflarestorage.com",
		"archive.r2.bucket: old-bucket -> new-bucket",
		"archive.r2.prefix: <none> -> v1",
		"archive.r2.access-key-id: created",
		"archive.r2.secret-access-key: updated",
		"archive.r2.sse-c-key: deleted",
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes =\n%s\nwant\n%s", strings.Join(changes, "\n"), strings.Join(want, "\n"))
	}
	joined := strings.Join(changes, "\n")
	for _, secret := range []string{oldSecret, newSecret, oldSSEC, newKeyID} {
		if strings.Contains(joined, secret) {
			t.Fatalf("change details leak an r2 credential: %s", joined)
		}
	}
	if unchanged := BuildConfigChangeDetails(newCfg, newCfg); len(unchanged) != 0 {
		t.Fatalf("unchanged archive section produced changes: %v", unchanged)
	}
}
