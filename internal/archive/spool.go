package archive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Spool directory entries (spec section 1).
const (
	// LockFileName is the advisory lock the writer holds for its lifetime.
	LockFileName = "spool.lock"

	segmentPrefix     = "seg-"
	segmentSuffix     = ".wal"
	openSegmentSuffix = ".wal.open"
)

// ErrSpoolLocked reports that another writer holds spool.lock.
var ErrSpoolLocked = errors.New("archive: spool is locked by another writer")

// segmentEntry is a segment file found in the spool directory.
type segmentEntry struct {
	seq  uint64
	open bool
}

// segmentName returns the file name of segment seq: seg-<seq>.wal, or
// seg-<seq>.wal.open while it is being written.
func segmentName(seq uint64, open bool) string {
	name := fmt.Sprintf("%s%012d%s", segmentPrefix, seq, segmentSuffix)
	if open {
		name += ".open"
	}
	return name
}

// parseSegmentName recognises segment file names. Other entries, such as
// spool.lock and the archiver's own archiver-* files, are not segments.
func parseSegmentName(name string) (segmentEntry, bool) {
	if !strings.HasPrefix(name, segmentPrefix) {
		return segmentEntry{}, false
	}
	var digits string
	var open bool
	switch {
	case strings.HasSuffix(name, openSegmentSuffix):
		digits, open = strings.TrimSuffix(name[len(segmentPrefix):], openSegmentSuffix), true
	case strings.HasSuffix(name, segmentSuffix):
		digits = strings.TrimSuffix(name[len(segmentPrefix):], segmentSuffix)
	default:
		return segmentEntry{}, false
	}
	if digits == "" {
		return segmentEntry{}, false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return segmentEntry{}, false
		}
	}
	seq, errParse := strconv.ParseUint(digits, 10, 64)
	if errParse != nil {
		return segmentEntry{}, false
	}
	return segmentEntry{seq: seq, open: open}, true
}

// listSegments returns the segments in dir in ascending sequence order, a
// completed segment before an open one with the same sequence.
func listSegments(dir string) ([]segmentEntry, error) {
	entries, errRead := os.ReadDir(dir)
	if errRead != nil {
		return nil, fmt.Errorf("archive: list spool %s: %w", dir, errRead)
	}
	var segments []segmentEntry
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if segment, ok := parseSegmentName(entry.Name()); ok {
			segments = append(segments, segment)
		}
	}
	sort.Slice(segments, func(i, j int) bool {
		if segments[i].seq != segments[j].seq {
			return segments[i].seq < segments[j].seq
		}
		return !segments[i].open && segments[j].open
	})
	return segments, nil
}

// syncDir makes renames and unlinks in dir durable.
func syncDir(dir string) error {
	d, errOpen := os.Open(dir)
	if errOpen != nil {
		return fmt.Errorf("archive: open spool %s: %w", dir, errOpen)
	}
	errSync := d.Sync()
	errClose := d.Close()
	if errSync != nil {
		return fmt.Errorf("archive: fsync spool %s: %w", dir, errSync)
	}
	if errClose != nil {
		return fmt.Errorf("archive: close spool %s: %w", dir, errClose)
	}
	return nil
}

// completeSegment turns seg-<seq>.wal.open into seg-<seq>.wal durably: fsync
// the file, rename it, fsync the directory (spec section 2, steps 3 and 5).
// The file must already be flushed and closed by the caller.
func completeSegment(dir string, seq uint64) error {
	openPath := filepath.Join(dir, segmentName(seq, true))
	donePath := filepath.Join(dir, segmentName(seq, false))
	f, errOpen := os.Open(openPath)
	if errOpen != nil {
		return fmt.Errorf("archive: open %s: %w", openPath, errOpen)
	}
	errSync := f.Sync()
	errClose := f.Close()
	if errSync != nil {
		return fmt.Errorf("archive: fsync %s: %w", openPath, errSync)
	}
	if errClose != nil {
		return fmt.Errorf("archive: close %s: %w", openPath, errClose)
	}
	if _, errStat := os.Lstat(donePath); errStat == nil {
		return fmt.Errorf("archive: roll %s: %s already exists", openPath, donePath)
	}
	if errRename := os.Rename(openPath, donePath); errRename != nil {
		return fmt.Errorf("archive: roll %s: %w", openPath, errRename)
	}
	return syncDir(dir)
}

// countFrames walks the frame headers of a completed segment and returns how
// many whole frames it holds. It is a best-effort count for a segment that is
// about to be dropped from the ring, so payloads are skipped, not verified.
func countFrames(path string) uint64 {
	f, errOpen := os.Open(path)
	if errOpen != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	info, errStat := f.Stat()
	if errStat != nil {
		return 0
	}
	size := info.Size()
	header := make([]byte, HeaderLen)
	var frames uint64
	for offset := int64(0); ; {
		if _, errRead := f.ReadAt(header, offset); errRead != nil {
			return frames
		}
		h, errParse := ParseHeader(header)
		if errParse != nil {
			return frames
		}
		offset += HeaderLen + int64(h.PayloadLen)
		if offset > size {
			return frames
		}
		frames++
	}
}
