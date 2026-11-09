package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// A valid SSE-C key: the base64 of 32 bytes.
const archiveTestSSECKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

func TestArchiveDefaults(t *testing.T) {
	for name, content := range map[string]string{
		"absent section": "port: 8317\n",
		"empty section":  "port: 8317\narchive: {}\n",
		"null section":   "port: 8317\narchive:\n",
		"explicit zeros": "archive:\n  segment-max-bytes: 0\n  ring-segments: 0\n  max-record-bytes: 0\n  segment-max-age: \"\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := writeUploadTestConfig(t, content)
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if cfg.Archive != DefaultArchiveConfig() {
				t.Fatalf("archive = %+v, want defaults %+v", cfg.Archive, DefaultArchiveConfig())
			}
			if cfg.Archive.Enabled || cfg.Archive.SpoolDir != "" {
				t.Fatal("archive must be disabled with a derived spool-dir by default")
			}
			if got := cfg.Archive.SegmentMaxAgeDuration(); got != 15*time.Minute {
				t.Fatalf("segment-max-age = %s", got)
			}
			if got := cfg.Archive.EffectiveSpoolDir("/auths"); got != filepath.Join("/auths", "archive-spool") {
				t.Fatalf("default spool-dir = %s", got)
			}
			parsed, err := ParseConfigBytes([]byte(content))
			if err != nil {
				t.Fatalf("ParseConfigBytes: %v", err)
			}
			if parsed.Archive != cfg.Archive {
				t.Fatalf("ParseConfigBytes archive = %+v, LoadConfig archive = %+v", parsed.Archive, cfg.Archive)
			}
			if saved := readUploadTestConfig(t, path); saved != content {
				t.Fatalf("loading rewrote the config:\n%s", saved)
			}
		})
	}
	if defaults := DefaultArchiveConfig(); defaults.SegmentMaxBytes != 64<<20 || defaults.SegmentMaxAge != "15m" || defaults.RingSegments != 16 || defaults.MaxRecordBytes != 64<<20 {
		t.Fatalf("defaults differ from the spec: %+v", defaults)
	}
}

func TestArchiveSectionLoads(t *testing.T) {
	content := `archive:
  enabled: true
  spool-dir: "/var/lib/cliproxyapi/archive-spool"
  segment-max-bytes: 1048576
  segment-max-age: "90s"
  ring-segments: 4
  max-record-bytes: 2097152
  r2:
    endpoint: "https://0123abcd.r2.cloudflarestorage.com"
    bucket: "agent-archive"
    prefix: "v1/proxy"
    access-key-id: "key-id"
    secret-access-key: "secret-value"
    sse-c-key: "` + archiveTestSSECKey + `"
`
	cfg, err := ParseConfigBytes([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	want := ArchiveConfig{
		Enabled:         true,
		SpoolDir:        "/var/lib/cliproxyapi/archive-spool",
		SegmentMaxBytes: 1 << 20,
		SegmentMaxAge:   "90s",
		RingSegments:    4,
		MaxRecordBytes:  2 << 20,
		R2: ArchiveR2Config{
			Endpoint:        "https://0123abcd.r2.cloudflarestorage.com",
			Bucket:          "agent-archive",
			Prefix:          "v1/proxy",
			AccessKeyID:     "key-id",
			SecretAccessKey: "secret-value",
			SSECKey:         archiveTestSSECKey,
		},
	}
	if cfg.Archive != want {
		t.Fatalf("archive = %+v", cfg.Archive)
	}
	if cfg.Archive.SegmentMaxAgeDuration() != 90*time.Second {
		t.Fatalf("segment-max-age = %s", cfg.Archive.SegmentMaxAgeDuration())
	}
	if cfg.Archive.EffectiveSpoolDir("/auths") != want.SpoolDir {
		t.Fatal("an explicit spool-dir must win over the auth-dir default")
	}
	// The section is valid in a v8 management write and keeps its keys strict.
	if err = ValidateV8Config([]byte("config-version: 8\n" + content)); err != nil {
		t.Fatalf("ValidateV8Config: %v", err)
	}
	if err = ValidateV8Config([]byte("config-version: 8\narchive:\n  spooldir: /tmp/x\n")); err == nil {
		t.Fatal("a misspelled archive key must be rejected in v8 writes")
	}
}

func TestArchiveSpoolDirTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	cfg, err := ParseConfigBytes([]byte("archive:\n  spool-dir: \"~/archive-spool\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "archive-spool"); cfg.Archive.SpoolDir != want {
		t.Fatalf("spool-dir = %s, want %s", cfg.Archive.SpoolDir, want)
	}
}

func TestParseArchiveDuration(t *testing.T) {
	cases := []struct {
		raw  string
		want time.Duration
		ok   bool
	}{
		{"15m", 15 * time.Minute, true},
		{"90s", 90 * time.Second, true},
		{"2h", 2 * time.Hour, true},
		{"007m", 7 * time.Minute, true},
		{"15min", 0, false},
		{"1.5h", 0, false},
		{"1h30m", 0, false},
		{"15", 0, false},
		{"m", 0, false},
		{"", 0, false},
		{"0s", 0, false},
		{"-5m", 0, false},
		{"+5m", 0, false},
		{" 5m", 0, false},
		{"5m ", 0, false},
		{"5M", 0, false},
		{"5ms", 0, false},
		{"5d", 0, false},
		{"9999999999999999999h", 0, false},
		{"3000000h", 0, false},
	}
	for _, tc := range cases {
		got, err := ParseArchiveDuration(tc.raw)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("ParseArchiveDuration(%q) = %s, %v; want %s, ok=%v", tc.raw, got, err, tc.want, tc.ok)
		}
	}
}

func TestArchiveValidation(t *testing.T) {
	const secret = "do-not-print-this-secret"
	cases := []struct {
		name    string
		section string
		wantErr string
	}{
		{"duration with unit word", "segment-max-age: \"15min\"", "archive.segment-max-age"},
		{"fractional duration", "segment-max-age: \"1.5h\"", "archive.segment-max-age"},
		{"relative spool-dir", "spool-dir: \"spool\"", "archive.spool-dir must be an absolute path"},
		{"negative segment size", "segment-max-bytes: -1", "archive.segment-max-bytes"},
		{"negative ring", "ring-segments: -2", "archive.ring-segments"},
		{"negative record cap", "max-record-bytes: -1", "archive.max-record-bytes"},
		{"record cap over the frame limit", "max-record-bytes: 134217729", "archive.max-record-bytes must not exceed"},
		{"record cap at the frame limit", "max-record-bytes: 134217728", ""},
		{"endpoint without scheme", "r2: {endpoint: \"0123.r2.cloudflarestorage.com\"}", "archive.r2.endpoint"},
		{"endpoint with credentials", "r2: {endpoint: \"https://user:" + secret + "@0123.r2.cloudflarestorage.com\"}", "archive.r2.endpoint"},
		{"uppercase bucket", "r2: {bucket: \"Agent-Archive\"}", "archive.r2.bucket"},
		{"short bucket", "r2: {bucket: \"ab\"}", "archive.r2.bucket"},
		{"prefix with leading slash", "r2: {prefix: \"/v1\"}", "archive.r2.prefix"},
		{"prefix with trailing slash", "r2: {prefix: \"v1/\"}", "archive.r2.prefix"},
		{"sse-c key not base64", "r2: {sse-c-key: \"" + secret + "\"}", "archive.r2.sse-c-key"},
		{"sse-c key of 16 bytes", "r2: {sse-c-key: \"MDEyMzQ1Njc4OWFiY2RlZg==\"}", "archive.r2.sse-c-key"},
		{"secrets alone are fine", "r2: {access-key-id: \"id\", secret-access-key: \"" + secret + "\"}", ""},
		{"valid sse-c key", "r2: {sse-c-key: \"" + archiveTestSSECKey + "\"}", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := "archive:\n  " + tc.section + "\n"
			_, errParse := ParseConfigBytes([]byte(content))
			_, errLoad := LoadConfig(writeUploadTestConfig(t, content))
			for _, err := range []error{errParse, errLoad} {
				if tc.wantErr == "" {
					if err != nil {
						t.Fatalf("unexpected error: %v", err)
					}
					continue
				}
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
				}
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaks a secret: %v", err)
				}
			}
		})
	}
}

func TestArchiveSecretsHiddenFromJSON(t *testing.T) {
	cfg, err := ParseConfigBytes([]byte("archive:\n  r2:\n    access-key-id: \"key-id\"\n    secret-access-key: \"secret-value\"\n    sse-c-key: \"" + archiveTestSSECKey + "\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret-value") || strings.Contains(string(data), archiveTestSSECKey) {
		t.Fatalf("config JSON exposes r2 secrets: %s", data)
	}
	if !strings.Contains(string(data), `"access-key-id":"key-id"`) {
		t.Fatalf("config JSON lost the archive section: %s", data)
	}
}

func TestArchiveSectionStaysTopLevel(t *testing.T) {
	legacy := []byte("port: 8317\narchive:\n  enabled: true\n  ring-segments: 4\n")
	var doc yaml.Node
	if err := yaml.Unmarshal(legacy, &doc); err != nil {
		t.Fatal(err)
	}
	if IsV8ConfigLayout(doc.Content[0]) {
		t.Fatal("an archive section alone must not mark a legacy file as v8")
	}
	migrated, changed, err := NormalizeConfigLayout(legacy, true)
	if err != nil || !changed {
		t.Fatalf("migrate: changed=%v err=%v", changed, err)
	}
	if err = yaml.Unmarshal(migrated, &doc); err != nil {
		t.Fatal(err)
	}
	if node := yamlPath(doc.Content[0], "archive.ring-segments"); node == nil || node.Value != "4" {
		t.Fatalf("v8 migration moved or commented out the archive section:\n%s", migrated)
	}
	if yamlPath(doc.Content[0], "observability.archive") != nil {
		t.Fatalf("archive must not move under observability:\n%s", migrated)
	}
	cfg, err := ParseConfigBytes(migrated)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Archive.Enabled || cfg.Archive.RingSegments != 4 {
		t.Fatalf("archive after migration = %+v", cfg.Archive)
	}
}

func TestArchiveDefaultsAreNotWrittenOnSave(t *testing.T) {
	for name, content := range map[string]string{
		"legacy": "port: 8317\n",
		"v8":     "config-version: 8\nserver:\n  port: 8317\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := writeUploadTestConfig(t, content)
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = SaveConfigPreserveComments(path, cfg); err != nil {
				t.Fatal(err)
			}
			if saved := readUploadTestConfig(t, path); strings.Contains(saved, "archive") {
				t.Fatalf("save wrote archive defaults into the file:\n%s", saved)
			}
		})
	}

	path := writeUploadTestConfig(t, "port: 8317\n")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Archive.Enabled = true
	cfg.Archive.RingSegments = 8
	if err = SaveConfigPreserveComments(path, cfg); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Archive != cfg.Archive {
		t.Fatalf("archive after save = %+v, want %+v", reloaded.Archive, cfg.Archive)
	}
	if saved := readUploadTestConfig(t, path); strings.Contains(saved, "segment-max-bytes") || strings.Contains(saved, "segment-max-age") {
		t.Fatalf("save wrote default archive values:\n%s", saved)
	}
}

func TestArchiveExampleConfig(t *testing.T) {
	example, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseConfigBytes(example)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Archive != DefaultArchiveConfig() {
		t.Fatalf("example archive section = %+v, want the disabled defaults", cfg.Archive)
	}
	// The documented r2 example must pass shape validation once uncommented.
	text := strings.ReplaceAll(string(example), "\r\n", "\n")
	_, after, found := strings.Cut(text, "  # r2:\n")
	if !found {
		t.Fatal("config.example.yaml is missing the archive r2 example")
	}
	var block strings.Builder
	block.WriteString("archive:\n  r2:\n")
	for _, line := range strings.Split(after, "\n") {
		if !strings.HasPrefix(line, "  #   ") {
			break
		}
		block.WriteString("    " + strings.TrimPrefix(line, "  #   ") + "\n")
	}
	r2 := strings.NewReplacer("<account-id>", "0123abcd", "<base64 of 32 random bytes>", archiveTestSSECKey).Replace(block.String())
	parsed, err := ParseConfigBytes([]byte(r2))
	if err != nil {
		t.Fatalf("uncommented r2 example: %v\n%s", err, r2)
	}
	if parsed.Archive.R2.Bucket != "agent-archive" || parsed.Archive.R2.Prefix != "v1" || parsed.Archive.R2.SSECKey != archiveTestSSECKey {
		t.Fatalf("r2 example = %+v", parsed.Archive.R2)
	}
}
