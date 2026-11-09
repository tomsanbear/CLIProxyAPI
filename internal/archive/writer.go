package archive

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// segmentBufferSize is the in-memory buffer in front of the open segment.
// Frames are appended whole, so a crash loses at most the buffered frames and
// never leaves more than one truncated frame at the tail.
const segmentBufferSize = 1 << 20

// failureLogInterval rate-limits the error log for dropped frames.
const failureLogInterval = time.Minute

// Options configures a Writer (spec sections 1 to 3 and 7.2).
type Options struct {
	// Dir is the spool directory. It is created when missing.
	Dir string
	// SegmentMaxBytes rolls the open segment once it holds this many bytes.
	SegmentMaxBytes int64
	// SegmentMaxAge rolls the open segment this long after its first frame.
	SegmentMaxAge time.Duration
	// RingSegments bounds the completed segments kept in the spool.
	RingSegments int
	// MaxRecordBytes caps the combined body bytes per record; 0 disables the cap.
	MaxRecordBytes int64
}

// Stats are the writer's counters.
type Stats struct {
	// FramesWritten and BytesWritten count frames appended to a segment.
	FramesWritten uint64 `json:"frames_written"`
	BytesWritten  uint64 `json:"bytes_written"`
	// FramesDropped counts frames never written: write errors, records that
	// could not be encoded, and appends after Close.
	FramesDropped uint64 `json:"frames_dropped"`
	// SegmentsDropped and SegmentFramesDropped count completed segments the
	// ring unlinked before the archiver drained them, and their frames
	// (best effort for segments written by an earlier process).
	SegmentsDropped      uint64 `json:"segments_dropped"`
	SegmentFramesDropped uint64 `json:"segment_frames_dropped"`
	// RecordsOversize counts records written with bodies omitted by the size cap.
	RecordsOversize uint64 `json:"records_oversize"`
	// RingDepth is the number of completed segments in the spool when last observed.
	RingDepth int `json:"ring_depth"`
}

// Writer appends archive records to the spool directory as WAL frames. It
// holds spool.lock for its lifetime. Append never returns an error and never
// waits on anything but its own local file I/O: a frame that cannot be written
// is dropped and counted. A Writer is safe for concurrent use.
type Writer struct {
	mu      sync.Mutex
	opts    Options
	nowFunc func() time.Time

	lock *os.File
	enc  *Encoder

	nextSeq uint64
	seg     *openSegment
	// segFrames holds the frame counts of completed segments rolled by this
	// writer, so a ring eviction can count lost frames without reading them.
	segFrames map[uint64]uint64

	stats  Stats
	closed bool

	lastFailureLog time.Time
	suppressed     uint64
}

// openSegment is the seg-<seq>.wal.open file being written.
type openSegment struct {
	seq        uint64
	file       *os.File
	buf        *bufio.Writer
	size       int64
	frames     uint64
	firstFrame time.Time
	// buffered counts frames and bytes accepted but not yet handed to the file.
	bufferedFrames uint64
	bufferedBytes  int64
}

// Open takes spool.lock in opts.Dir, rolls any segment a previous process left
// open, enforces the ring and returns a Writer ready to append. It returns an
// error wrapping ErrSpoolLocked when another writer holds the lock.
func Open(opts Options) (*Writer, error) {
	if opts.Dir == "" {
		return nil, errors.New("archive: spool directory is required")
	}
	if opts.SegmentMaxBytes <= 0 || opts.SegmentMaxAge <= 0 || opts.RingSegments <= 0 || opts.MaxRecordBytes < 0 {
		return nil, fmt.Errorf("archive: invalid writer options (segment-max-bytes %d, segment-max-age %s, ring-segments %d, max-record-bytes %d)",
			opts.SegmentMaxBytes, opts.SegmentMaxAge, opts.RingSegments, opts.MaxRecordBytes)
	}
	if errMkdir := os.MkdirAll(opts.Dir, 0o700); errMkdir != nil {
		return nil, fmt.Errorf("archive: create spool %s: %w", opts.Dir, errMkdir)
	}
	lock, errLock := lockSpool(opts.Dir)
	if errLock != nil {
		return nil, errLock
	}
	enc, errEncoder := NewEncoder()
	if errEncoder != nil {
		_ = lock.Close()
		return nil, errEncoder
	}
	w := &Writer{
		opts:      opts,
		nowFunc:   time.Now,
		lock:      lock,
		enc:       enc,
		segFrames: make(map[uint64]uint64),
	}
	segments, errList := listSegments(opts.Dir)
	if errList != nil {
		_ = enc.Close()
		_ = lock.Close()
		return nil, errList
	}
	var highest uint64
	for _, segment := range segments {
		highest = max(highest, segment.seq)
		if !segment.open {
			continue
		}
		// A previous process crashed with this segment open. Its tail may hold
		// a truncated frame, which readers tolerate (spec section 6).
		if errComplete := completeSegment(opts.Dir, segment.seq); errComplete != nil {
			log.WithError(errComplete).Warn("archive: could not roll a segment left open by a previous run")
			continue
		}
		log.WithField("segment", segmentName(segment.seq, false)).Info("archive: rolled a segment left open by a previous run")
	}
	w.nextSeq = highest + 1
	w.enforceRingLocked()
	return w, nil
}

// Dir returns the spool directory.
func (w *Writer) Dir() string {
	if w == nil {
		return ""
	}
	return w.opts.Dir
}

// Stats returns a snapshot of the counters.
func (w *Writer) Stats() Stats {
	if w == nil {
		return Stats{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stats
}

// Append encodes rec and appends it to the open segment, creating the segment
// for the first frame and rolling it on age before and on size after the
// append. Bodies over the size cap are omitted from the record (spec section
// 7.2). A frame that cannot be encoded or written is dropped and counted.
func (w *Writer) Append(rec Record) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		w.stats.FramesDropped++
		return
	}
	now := w.nowFunc()
	frame, errEncode := w.encodeLocked(rec)
	if errEncode != nil {
		w.stats.FramesDropped++
		w.logFailureLocked(now, "encode record", errEncode)
		return
	}
	if w.seg != nil && now.Sub(w.seg.firstFrame) >= w.opts.SegmentMaxAge {
		w.rollLocked(now)
	}
	if w.seg == nil {
		if errOpen := w.openSegmentLocked(now); errOpen != nil {
			w.stats.FramesDropped++
			w.logFailureLocked(now, "create segment", errOpen)
			return
		}
	}
	if errWrite := w.writeFrameLocked(frame); errWrite != nil {
		w.stats.FramesDropped++
		w.abandonSegmentLocked(now, "write frame", errWrite)
		return
	}
	w.stats.FramesWritten++
	w.stats.BytesWritten += uint64(len(frame))
	if w.seg.size >= w.opts.SegmentMaxBytes {
		w.rollLocked(now)
	}
}

// Tick rolls the open segment once it has reached segment-max-age, so an idle
// proxy still completes its last segment, and otherwise flushes buffered
// frames to the file. It also refreshes the ring depth, which the archiver
// lowers by draining segments. The service calls it periodically.
func (w *Writer) Tick() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	now := w.nowFunc()
	if w.seg != nil {
		if now.Sub(w.seg.firstFrame) >= w.opts.SegmentMaxAge {
			w.rollLocked(now)
			return
		}
		if w.seg.buf.Buffered() > 0 {
			if errFlush := w.seg.buf.Flush(); errFlush != nil {
				w.abandonSegmentLocked(now, "flush segment", errFlush)
			} else {
				w.seg.bufferedFrames, w.seg.bufferedBytes = 0, 0
			}
		}
	}
	if segments, errList := listSegments(w.opts.Dir); errList == nil {
		w.stats.RingDepth = countCompleted(segments)
	}
}

// Close rolls the open segment, releases spool.lock and stops the writer.
// Later appends are dropped and counted. Close is idempotent.
func (w *Writer) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	var errs []error
	if w.seg != nil {
		if errRoll := w.rollLocked(w.nowFunc()); errRoll != nil {
			errs = append(errs, errRoll)
		}
	}
	if errClose := w.enc.Close(); errClose != nil {
		errs = append(errs, fmt.Errorf("archive: close encoder: %w", errClose))
	}
	if errUnlock := w.lock.Close(); errUnlock != nil {
		errs = append(errs, fmt.Errorf("archive: release %s: %w", LockFileName, errUnlock))
	}
	return errors.Join(errs...)
}

// encodeLocked applies the size cap and encodes rec. If the frame still
// exceeds the format limits (a near-limit cap on incompressible bodies), both
// bodies are omitted so the turn's metadata is kept.
func (w *Writer) encodeLocked(rec Record) ([]byte, error) {
	capped, oversize, errCap := capRecordBodies(rec, w.opts.MaxRecordBytes)
	if errCap != nil {
		return nil, errCap
	}
	frame, errEncode := w.enc.Encode(capped)
	if errEncode != nil && (len(capped.Request) > 0 || len(capped.Response) > 0) {
		capped, errCap = omitRecordBodies(capped, len(capped.Request) > 0, len(capped.Response) > 0)
		if errCap != nil {
			return nil, errCap
		}
		oversize = true
		frame, errEncode = w.enc.Encode(capped)
	}
	if errEncode != nil {
		return nil, errEncode
	}
	if oversize {
		w.stats.RecordsOversize++
	}
	return frame, nil
}

func (w *Writer) openSegmentLocked(now time.Time) error {
	seq := w.nextSeq
	path := filepath.Join(w.opts.Dir, segmentName(seq, true))
	f, errOpen := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errOpen != nil {
		if errors.Is(errOpen, fs.ErrExist) {
			// Never append to a segment this writer did not create.
			w.nextSeq++
		}
		return fmt.Errorf("archive: create %s: %w", path, errOpen)
	}
	w.nextSeq++
	w.seg = &openSegment{
		seq:        seq,
		file:       f,
		buf:        bufio.NewWriterSize(f, segmentBufferSize),
		firstFrame: now,
	}
	return nil
}

// writeFrameLocked appends one whole frame. The buffer is flushed before a
// frame that does not fit, and a frame larger than the buffer goes straight to
// the file, so a frame never straddles a flush.
func (w *Writer) writeFrameLocked(frame []byte) error {
	seg := w.seg
	if len(frame) > seg.buf.Available() && seg.buf.Buffered() > 0 {
		if errFlush := seg.buf.Flush(); errFlush != nil {
			return errFlush
		}
		seg.bufferedFrames, seg.bufferedBytes = 0, 0
	}
	if len(frame) >= seg.buf.Size() {
		if _, errWrite := seg.file.Write(frame); errWrite != nil {
			return errWrite
		}
	} else {
		if _, errWrite := seg.buf.Write(frame); errWrite != nil {
			return errWrite
		}
		seg.bufferedFrames++
		seg.bufferedBytes += int64(len(frame))
	}
	seg.size += int64(len(frame))
	seg.frames++
	return nil
}

// rollLocked completes the open segment (flush, fsync, rename, fsync the
// directory) and enforces the ring.
func (w *Writer) rollLocked(now time.Time) error {
	seg := w.seg
	if seg == nil {
		return nil
	}
	if errFlush := seg.buf.Flush(); errFlush != nil {
		w.abandonSegmentLocked(now, "flush segment", errFlush)
		return fmt.Errorf("archive: flush %s: %w", segmentName(seg.seq, true), errFlush)
	}
	seg.bufferedFrames, seg.bufferedBytes = 0, 0
	w.seg = nil
	errRoll := w.finishSegmentLocked(seg)
	if errRoll != nil {
		w.logFailureLocked(now, "roll segment", errRoll)
	}
	w.enforceRingLocked()
	return errRoll
}

// abandonSegmentLocked handles a failed write or flush: the frames still in
// the buffer are lost and counted, and the segment is completed with what
// reached the file so that the frames already written stay readable. The next
// append starts a new segment.
func (w *Writer) abandonSegmentLocked(now time.Time, op string, cause error) {
	seg := w.seg
	w.seg = nil
	w.logFailureLocked(now, op, cause)
	if seg == nil {
		return
	}
	lost := seg.bufferedFrames
	w.stats.FramesWritten -= min(w.stats.FramesWritten, lost)
	w.stats.BytesWritten -= min(w.stats.BytesWritten, uint64(seg.bufferedBytes))
	w.stats.FramesDropped += lost
	seg.frames -= min(seg.frames, lost)
	if errFinish := w.finishSegmentLocked(seg); errFinish != nil {
		w.logFailureLocked(now, "roll abandoned segment", errFinish)
	}
	w.enforceRingLocked()
}

// finishSegmentLocked closes the segment file and renames it to its completed
// name. When the rename fails the .wal.open file stays behind and the next
// start rolls it.
func (w *Writer) finishSegmentLocked(seg *openSegment) error {
	if errClose := seg.file.Close(); errClose != nil {
		return fmt.Errorf("archive: close %s: %w", segmentName(seg.seq, true), errClose)
	}
	if errComplete := completeSegment(w.opts.Dir, seg.seq); errComplete != nil {
		return errComplete
	}
	w.segFrames[seg.seq] = seg.frames
	return nil
}

// enforceRingLocked unlinks the lowest completed segments while more than
// ring-segments remain (spec section 3). A segment the archiver already
// removed (ENOENT) is not a drop.
func (w *Writer) enforceRingLocked() {
	segments, errList := listSegments(w.opts.Dir)
	if errList != nil {
		w.logFailureLocked(w.nowFunc(), "list spool", errList)
		return
	}
	completed := make([]uint64, 0, len(segments))
	present := make(map[uint64]bool, len(segments))
	for _, segment := range segments {
		if !segment.open {
			completed = append(completed, segment.seq)
			present[segment.seq] = true
		}
	}
	for seq := range w.segFrames {
		if !present[seq] {
			delete(w.segFrames, seq)
		}
	}
	removed := false
	for len(completed) > w.opts.RingSegments {
		seq := completed[0]
		path := filepath.Join(w.opts.Dir, segmentName(seq, false))
		frames, known := w.segFrames[seq]
		if !known {
			frames = countFrames(path)
		}
		if errRemove := os.Remove(path); errRemove != nil && !errors.Is(errRemove, fs.ErrNotExist) {
			w.logFailureLocked(w.nowFunc(), "drop oldest segment", errRemove)
			break
		} else if errRemove == nil {
			w.stats.SegmentsDropped++
			w.stats.SegmentFramesDropped += frames
			removed = true
		}
		delete(w.segFrames, seq)
		completed = completed[1:]
	}
	if removed {
		if errSync := syncDir(w.opts.Dir); errSync != nil {
			w.logFailureLocked(w.nowFunc(), "sync spool", errSync)
		}
	}
	w.stats.RingDepth = len(completed)
}

// logFailureLocked logs a failure at most once per failureLogInterval,
// reporting how many failures were suppressed in between.
func (w *Writer) logFailureLocked(now time.Time, op string, cause error) {
	if !w.lastFailureLog.IsZero() && now.Sub(w.lastFailureLog) < failureLogInterval {
		w.suppressed++
		return
	}
	log.WithError(cause).WithFields(log.Fields{
		"op":               op,
		"spool":            w.opts.Dir,
		"suppressed":       w.suppressed,
		"frames_written":   w.stats.FramesWritten,
		"frames_dropped":   w.stats.FramesDropped,
		"segments_dropped": w.stats.SegmentsDropped,
		"ring_depth":       w.stats.RingDepth,
	}).Error("archive: spool operation failed; frames that cannot be written are dropped and counted")
	w.lastFailureLog = now
	w.suppressed = 0
}

func countCompleted(segments []segmentEntry) int {
	n := 0
	for _, segment := range segments {
		if !segment.open {
			n++
		}
	}
	return n
}
