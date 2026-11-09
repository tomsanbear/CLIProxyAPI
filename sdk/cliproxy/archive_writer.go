package cliproxy

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/archive"
	log "github.com/sirupsen/logrus"
)

// archiveStatsLogInterval bounds how often changed writer counters are logged.
const archiveStatsLogInterval = time.Minute

// archiveRuntime owns the archive WAL writer for the service lifetime and
// drives its periodic work: rolling an idle segment once it reaches
// segment-max-age, flushing buffered frames and logging the counters.
type archiveRuntime struct {
	writer *archive.Writer
	cancel context.CancelFunc
	done   chan struct{}
}

// ArchiveWriter returns the running archive WAL writer, or nil when archiving
// is disabled or its spool could not be opened. Appends after shutdown are
// dropped and counted by the writer.
func (s *Service) ArchiveWriter() *archive.Writer {
	if s == nil {
		return nil
	}
	s.archiveMu.Lock()
	defer s.archiveMu.Unlock()
	if s.archive == nil {
		return nil
	}
	return s.archive.writer
}

// startArchive opens the archive writer when archive.enabled. The section is
// read once: a config change takes effect on restart. When another process
// holds the spool lock, or the spool cannot be opened, the error is logged and
// the service runs without archiving.
func (s *Service) startArchive() {
	if s == nil {
		return
	}
	cfg := s.currentConfig()
	if cfg == nil || !cfg.Archive.Enabled {
		return
	}
	s.archiveMu.Lock()
	defer s.archiveMu.Unlock()
	if s.archive != nil {
		return
	}
	dir, errAbs := filepath.Abs(cfg.Archive.EffectiveSpoolDir(cfg.AuthDir))
	if errAbs != nil {
		log.WithError(errAbs).Error("archive: cannot resolve the spool directory; archiving disabled")
		return
	}
	maxAge := cfg.Archive.SegmentMaxAgeDuration()
	writer, errOpen := archive.Open(archive.Options{
		Dir:             dir,
		SegmentMaxBytes: cfg.Archive.SegmentMaxBytes,
		SegmentMaxAge:   maxAge,
		RingSegments:    cfg.Archive.RingSegments,
		MaxRecordBytes:  cfg.Archive.MaxRecordBytes,
	})
	if errOpen != nil {
		if errors.Is(errOpen, archive.ErrSpoolLocked) {
			log.WithError(errOpen).WithField("spool", dir).Error("archive: the spool is locked by another process; archiving disabled")
			return
		}
		log.WithError(errOpen).WithField("spool", dir).Error("archive: cannot open the spool; archiving disabled")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	current := &archiveRuntime{writer: writer, cancel: cancel, done: make(chan struct{})}
	s.archive = current
	go current.run(ctx, archiveTickInterval(maxAge))
	log.WithFields(log.Fields{
		"spool":             dir,
		"segment_max_bytes": cfg.Archive.SegmentMaxBytes,
		"segment_max_age":   maxAge.String(),
		"ring_segments":     cfg.Archive.RingSegments,
		"max_record_bytes":  cfg.Archive.MaxRecordBytes,
		"ring_depth":        writer.Stats().RingDepth,
	}).Info("archive: writer started")
}

// stopArchive stops the periodic work, rolls the open segment and releases the
// spool lock. It runs after the HTTP server has stopped, so no request is
// still appending.
func (s *Service) stopArchive() error {
	if s == nil {
		return nil
	}
	s.archiveMu.Lock()
	current := s.archive
	s.archive = nil
	s.archiveMu.Unlock()
	if current == nil {
		return nil
	}
	current.cancel()
	<-current.done
	errClose := current.writer.Close()
	logArchiveStats(current.writer.Stats(), "archive: writer stopped")
	return errClose
}

// archiveTickInterval checks often enough that an idle segment rolls within a
// quarter of segment-max-age of its deadline, and at least once a minute.
func archiveTickInterval(maxAge time.Duration) time.Duration {
	return min(time.Minute, max(time.Second, maxAge/4))
}

func (r *archiveRuntime) run(ctx context.Context, interval time.Duration) {
	defer close(r.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var logged archive.Stats
	lastLog := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.writer.Tick()
			if time.Since(lastLog) < archiveStatsLogInterval {
				continue
			}
			lastLog = time.Now()
			if stats := r.writer.Stats(); stats != logged {
				logArchiveStats(stats, "archive: writer counters")
				logged = stats
			}
		}
	}
}

func logArchiveStats(stats archive.Stats, msg string) {
	log.WithFields(log.Fields{
		"frames_written":         stats.FramesWritten,
		"bytes_written":          stats.BytesWritten,
		"frames_dropped":         stats.FramesDropped,
		"segments_dropped":       stats.SegmentsDropped,
		"segment_frames_dropped": stats.SegmentFramesDropped,
		"records_oversize":       stats.RecordsOversize,
		"ring_depth":             stats.RingDepth,
	}).Info(msg)
}
