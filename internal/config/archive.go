package config

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/MoeForge/moego/base64"
)

// MaxArchiveMaxRecordBytes bounds archive.max-record-bytes by the WAL frame
// payload limit (docs/archive-wal-format.md section 5).
const MaxArchiveMaxRecordBytes int64 = 128 << 20

// archiveSpoolDirName is the spool directory under auth-dir used when
// archive.spool-dir is empty.
const archiveSpoolDirName = "archive-spool"

const archiveSSECKeyBytes = 32

var archiveBucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

// DefaultArchiveConfig returns the archive section with every default applied.
func DefaultArchiveConfig() ArchiveConfig {
	return ArchiveConfig{
		SegmentMaxBytes: DefaultArchiveSegmentMaxBytes,
		SegmentMaxAge:   DefaultArchiveSegmentMaxAge,
		RingSegments:    DefaultArchiveRingSegments,
		MaxRecordBytes:  DefaultArchiveMaxRecordBytes,
	}
}

// ParseArchiveDuration parses the archive duration grammar: a positive decimal
// integer followed by s, m or h, with nothing else ("15m", "90s", "2h").
func ParseArchiveDuration(raw string) (time.Duration, error) {
	if len(raw) < 2 {
		return 0, fmt.Errorf("duration %q must be an integer followed by s, m or h", raw)
	}
	var unit time.Duration
	switch raw[len(raw)-1] {
	case 's':
		unit = time.Second
	case 'm':
		unit = time.Minute
	case 'h':
		unit = time.Hour
	default:
		return 0, fmt.Errorf("duration %q must be an integer followed by s, m or h", raw)
	}
	digits := raw[:len(raw)-1]
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, fmt.Errorf("duration %q must be an integer followed by s, m or h", raw)
		}
	}
	n, errParse := strconv.ParseInt(digits, 10, 64)
	if errParse != nil || n > math.MaxInt64/int64(unit) {
		return 0, fmt.Errorf("duration %q is out of range", raw)
	}
	if n == 0 {
		return 0, fmt.Errorf("duration %q must be positive", raw)
	}
	return time.Duration(n) * unit, nil
}

// SegmentMaxAgeDuration returns the parsed segment-max-age, or the default when
// the value is empty or invalid (a loaded config has already been validated).
func (c ArchiveConfig) SegmentMaxAgeDuration() time.Duration {
	if d, errParse := ParseArchiveDuration(c.SegmentMaxAge); errParse == nil {
		return d
	}
	d, _ := ParseArchiveDuration(DefaultArchiveSegmentMaxAge)
	return d
}

// EffectiveSpoolDir returns the configured spool directory, or
// <authDir>/archive-spool when none is configured. authDir must already be
// resolved.
func (c ArchiveConfig) EffectiveSpoolDir(authDir string) string {
	if dir := strings.TrimSpace(c.SpoolDir); dir != "" {
		return dir
	}
	return filepath.Join(authDir, archiveSpoolDirName)
}

// NormalizeArchiveConfig applies archive defaults, resolves an explicit
// spool-dir (a leading ~ is expanded; the result must be absolute) and
// validates the section, including the shape of the r2 settings the archiver
// consumes. Errors never include r2 values.
func (cfg *Config) NormalizeArchiveConfig() error {
	if cfg == nil {
		return nil
	}
	a := &cfg.Archive

	if dir := strings.TrimSpace(a.SpoolDir); dir != "" {
		resolved, errResolve := expandHomePath(dir, "archive.spool-dir")
		if errResolve != nil {
			return errResolve
		}
		if !filepath.IsAbs(resolved) {
			return errors.New("archive.spool-dir must be an absolute path")
		}
		a.SpoolDir = resolved
	} else {
		a.SpoolDir = ""
	}

	switch {
	case a.SegmentMaxBytes == 0:
		a.SegmentMaxBytes = DefaultArchiveSegmentMaxBytes
	case a.SegmentMaxBytes < 0:
		return errors.New("archive.segment-max-bytes must be positive")
	}
	if a.SegmentMaxAge == "" {
		a.SegmentMaxAge = DefaultArchiveSegmentMaxAge
	} else if _, errParse := ParseArchiveDuration(a.SegmentMaxAge); errParse != nil {
		return fmt.Errorf("archive.segment-max-age: %w", errParse)
	}
	switch {
	case a.RingSegments == 0:
		a.RingSegments = DefaultArchiveRingSegments
	case a.RingSegments < 0:
		return errors.New("archive.ring-segments must be positive")
	}
	switch {
	case a.MaxRecordBytes == 0:
		a.MaxRecordBytes = DefaultArchiveMaxRecordBytes
	case a.MaxRecordBytes < 0:
		return errors.New("archive.max-record-bytes must be positive")
	case a.MaxRecordBytes > MaxArchiveMaxRecordBytes:
		return fmt.Errorf("archive.max-record-bytes must not exceed %d", MaxArchiveMaxRecordBytes)
	}

	return a.R2.validateShape()
}

// validateShape checks the r2 values without interpreting them: the proxy does
// not talk to R2. Every key is optional because the archiver can also take
// them from flags or the environment.
func (r ArchiveR2Config) validateShape() error {
	if r.Endpoint != "" {
		u, errParse := url.Parse(r.Endpoint)
		if errParse != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("archive.r2.endpoint must be an http(s) URL with a host and no credentials, query or fragment")
		}
	}
	if r.Bucket != "" && !archiveBucketPattern.MatchString(r.Bucket) {
		return errors.New("archive.r2.bucket must be 3-63 lowercase letters, digits or hyphens, starting and ending with a letter or digit")
	}
	if r.Prefix != "" && (strings.HasPrefix(r.Prefix, "/") || strings.HasSuffix(r.Prefix, "/")) {
		return errors.New("archive.r2.prefix must not start or end with a slash")
	}
	if r.SSECKey != "" {
		key, errDecode := base64.StdEncoding.DecodeString(r.SSECKey)
		if errDecode != nil || len(key) != archiveSSECKeyBytes {
			return fmt.Errorf("archive.r2.sse-c-key must be the base64 encoding of %d bytes", archiveSSECKeyBytes)
		}
	}
	return nil
}
