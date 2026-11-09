package archive

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MoeForge/moego/ojson"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 9, 18, 0, 0, 0, time.UTC)}
}

func testOptions(dir string) Options {
	return Options{
		Dir:             dir,
		SegmentMaxBytes: 64 << 20,
		SegmentMaxAge:   15 * time.Minute,
		RingSegments:    16,
		MaxRecordBytes:  64 << 20,
	}
}

// openTestWriter opens a writer on the mock clock and closes it at cleanup.
func openTestWriter(t *testing.T, opts Options, clock *fakeClock) *Writer {
	t.Helper()
	w, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w.nowFunc = clock.Now
	t.Cleanup(func() { _ = w.Close() })
	return w
}

// crash simulates a process that dies with its segment open: the buffered
// frames reach the file, but nothing is rolled, and the lock is released.
func crash(t *testing.T, w *Writer) {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seg != nil {
		if err := w.seg.buf.Flush(); err != nil {
			t.Fatal(err)
		}
		if err := w.seg.file.Close(); err != nil {
			t.Fatal(err)
		}
		w.seg = nil
	}
	_ = w.enc.Close()
	_ = w.lock.Close()
	w.closed = true
}

// testRecord is a record with a meta object and bodies of the given sizes.
// Bodies are pseudo-random so that frame sizes track body sizes.
func testRecord(id string, requestLen, responseLen int) Record {
	rng := rand.New(rand.NewSource(int64(len(id)*7919 + requestLen*31 + responseLen)))
	body := func(n int) []byte {
		b := make([]byte, n)
		_, _ = rng.Read(b)
		return b
	}
	meta := fmt.Sprintf(`{"v":1,"request_id":%q,"attempt":1,"request":{"format":"claude","bytes":%d,"omitted":false,"omitted_reason":""},"response":{"kind":"sse","bytes":%d,"omitted":false,"omitted_reason":""}}`, id, requestLen, responseLen)
	return Record{Meta: []byte(meta), Request: body(requestLen), Response: body(responseLen)}
}

func frameLen(t *testing.T, rec Record) int64 {
	t.Helper()
	enc, err := NewEncoder()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = enc.Close() }()
	frame, err := enc.Encode(rec)
	if err != nil {
		t.Fatal(err)
	}
	return int64(len(frame))
}

// spoolSegments lists the segment file names in dir, in name order.
func spoolSegments(t *testing.T, dir string) (completed, open []string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		switch name := entry.Name(); {
		case strings.HasSuffix(name, openSegmentSuffix):
			open = append(open, name)
		case strings.HasSuffix(name, segmentSuffix):
			completed = append(completed, name)
		}
	}
	sort.Strings(completed)
	sort.Strings(open)
	return completed, open
}

// readSpool decodes every completed segment with ReadSegment, in sequence
// order, and fails on any damage.
func readSpool(t *testing.T, dir string) map[string][]Record {
	t.Helper()
	dec, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	completed, _ := spoolSegments(t, dir)
	out := make(map[string][]Record, len(completed))
	for _, name := range completed {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		records, stats, err := ReadSegment(bytes.NewReader(data), dec)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if stats != (ReadStats{Frames: len(records)}) {
			t.Fatalf("%s: damaged segment: %+v", name, stats)
		}
		out[name] = records
	}
	return out
}

func requestIDs(records []Record) []string {
	ids := make([]string, 0, len(records))
	for _, rec := range records {
		ids = append(ids, ojson.GetBytes(rec.Meta, "request_id").String())
	}
	return ids
}

func assertRecordsEqual(t *testing.T, got, want []Record) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("decoded %d records, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i].Meta, want[i].Meta) || !bytes.Equal(got[i].Request, want[i].Request) || !bytes.Equal(got[i].Response, want[i].Response) {
			t.Fatalf("record %d differs from the record appended", i)
		}
	}
}

func TestWriterRollsOnSize(t *testing.T) {
	probe := testRecord("probe", 4096, 1024)
	f := frameLen(t, probe)
	cases := []struct {
		name            string
		segmentMaxBytes int64
		appends         int
		wantSegments    []int
	}{
		{"every frame fills a segment", 1, 4, []int{1, 1, 1, 1}},
		{"exactly at the limit rolls", f, 3, []int{1, 1, 1}},
		{"three frames per segment", 2*f + 1, 7, []int{3, 3, 1}},
		{"never reaches the limit", 1 << 30, 5, []int{5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			opts := testOptions(dir)
			opts.SegmentMaxBytes = tc.segmentMaxBytes
			w := openTestWriter(t, opts, newFakeClock())
			var want []Record
			for i := 0; i < tc.appends; i++ {
				// Identical records keep every frame exactly f bytes long.
				want = append(want, probe)
				w.Append(probe)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			completed, open := spoolSegments(t, dir)
			if len(open) != 0 {
				t.Fatalf("open segments after Close: %v", open)
			}
			segments := readSpool(t, dir)
			var got []Record
			var sizes []int
			for i, name := range completed {
				if name != segmentName(uint64(i+1), false) {
					t.Fatalf("segment %d is %s", i, name)
				}
				sizes = append(sizes, len(segments[name]))
				got = append(got, segments[name]...)
			}
			if fmt.Sprint(sizes) != fmt.Sprint(tc.wantSegments) {
				t.Fatalf("frames per segment = %v, want %v", sizes, tc.wantSegments)
			}
			assertRecordsEqual(t, got, want)
			stats := w.Stats()
			if stats.FramesWritten != uint64(tc.appends) || stats.BytesWritten != uint64(tc.appends)*uint64(f) || stats.FramesDropped != 0 {
				t.Fatalf("stats = %+v", stats)
			}
		})
	}
}

func TestWriterRollsOnAge(t *testing.T) {
	type step struct {
		advance time.Duration
		append  bool
		tick    bool
	}
	cases := []struct {
		name          string
		steps         []step
		wantCompleted []int // frames per completed segment before Close
		wantOpen      bool
	}{
		{
			name:          "append before max age stays in segment",
			steps:         []step{{append: true}, {advance: 14 * time.Minute, append: true}},
			wantCompleted: nil,
			wantOpen:      true,
		},
		{
			name:          "append at max age rolls first",
			steps:         []step{{append: true}, {advance: 10 * time.Minute, append: true}, {advance: 5 * time.Minute, append: true}},
			wantCompleted: []int{2},
			wantOpen:      true,
		},
		{
			name:          "age counts from the first frame",
			steps:         []step{{advance: time.Hour}, {append: true}, {advance: 14 * time.Minute, append: true}},
			wantCompleted: nil,
			wantOpen:      true,
		},
		{
			name:          "tick before max age keeps segment open",
			steps:         []step{{append: true}, {advance: 14*time.Minute + 59*time.Second, tick: true}},
			wantCompleted: nil,
			wantOpen:      true,
		},
		{
			name:          "tick at max age rolls an idle segment",
			steps:         []step{{append: true}, {advance: 15 * time.Minute, tick: true}},
			wantCompleted: []int{1},
			wantOpen:      false,
		},
		{
			name:          "tick without a segment creates nothing",
			steps:         []step{{tick: true}, {advance: time.Hour, tick: true}},
			wantCompleted: nil,
			wantOpen:      false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			clock := newFakeClock()
			w := openTestWriter(t, testOptions(dir), clock)
			for i, s := range tc.steps {
				clock.Advance(s.advance)
				if s.append {
					w.Append(testRecord(fmt.Sprintf("r-%d", i), 128, 64))
				}
				if s.tick {
					w.Tick()
				}
			}
			completed, open := spoolSegments(t, dir)
			segments := readSpool(t, dir)
			var sizes []int
			for _, name := range completed {
				sizes = append(sizes, len(segments[name]))
			}
			if fmt.Sprint(sizes) != fmt.Sprint(tc.wantCompleted) {
				t.Fatalf("completed frames per segment = %v, want %v", sizes, tc.wantCompleted)
			}
			if (len(open) == 1) != tc.wantOpen || len(open) > 1 {
				t.Fatalf("open segments = %v, want open=%v", open, tc.wantOpen)
			}
		})
	}
}

func TestWriterTickFlushesBufferedFrames(t *testing.T) {
	dir := t.TempDir()
	w := openTestWriter(t, testOptions(dir), newFakeClock())
	rec := testRecord("r-1", 100, 100)
	w.Append(rec)
	path := filepath.Join(dir, segmentName(1, true))
	if info, err := os.Stat(path); err != nil || info.Size() != 0 {
		t.Fatalf("frame should still be buffered: info=%v err=%v", info, err)
	}
	w.Tick()
	if info, err := os.Stat(path); err != nil || info.Size() != frameLen(t, rec) {
		t.Fatalf("tick did not flush the frame: info=%v err=%v", info, err)
	}
}

func TestWriterRingEviction(t *testing.T) {
	t.Run("drops the lowest segments first", func(t *testing.T) {
		dir := t.TempDir()
		opts := testOptions(dir)
		opts.SegmentMaxBytes = 1 // one frame per segment
		opts.RingSegments = 3
		w := openTestWriter(t, opts, newFakeClock())
		for i := 1; i <= 6; i++ {
			w.Append(testRecord(fmt.Sprintf("r-%d", i), 64, 64))
			completed, _ := spoolSegments(t, dir)
			first := uint64(1)
			if i > opts.RingSegments {
				first = uint64(i - opts.RingSegments + 1)
			}
			if completed[0] != segmentName(first, false) || len(completed) != min(i, opts.RingSegments) {
				t.Fatalf("after append %d: segments %v", i, completed)
			}
		}
		segments := readSpool(t, dir)
		var ids []string
		for _, seq := range []uint64{4, 5, 6} {
			ids = append(ids, requestIDs(segments[segmentName(seq, false)])...)
		}
		if strings.Join(ids, ",") != "r-4,r-5,r-6" {
			t.Fatalf("surviving records = %v", ids)
		}
		stats := w.Stats()
		if stats.SegmentsDropped != 3 || stats.SegmentFramesDropped != 3 || stats.RingDepth != 3 || stats.FramesWritten != 6 {
			t.Fatalf("stats = %+v", stats)
		}
	})

	t.Run("a segment the archiver removed is not a drop", func(t *testing.T) {
		dir := t.TempDir()
		opts := testOptions(dir)
		opts.SegmentMaxBytes = 1
		opts.RingSegments = 2
		w := openTestWriter(t, opts, newFakeClock())
		w.Append(testRecord("r-1", 64, 64))
		w.Append(testRecord("r-2", 64, 64))
		if err := os.Remove(filepath.Join(dir, segmentName(1, false))); err != nil {
			t.Fatal(err)
		}
		w.Append(testRecord("r-3", 64, 64))
		completed, _ := spoolSegments(t, dir)
		if strings.Join(completed, ",") != segmentName(2, false)+","+segmentName(3, false) {
			t.Fatalf("segments = %v", completed)
		}
		if stats := w.Stats(); stats.SegmentsDropped != 0 || stats.RingDepth != 2 {
			t.Fatalf("stats = %+v", stats)
		}
	})

	t.Run("segments from an earlier run are counted by their frames", func(t *testing.T) {
		dir := t.TempDir()
		first := openTestWriter(t, testOptions(dir), newFakeClock())
		for seg := 0; seg < 3; seg++ {
			for i := 0; i <= seg; i++ {
				first.Append(testRecord(fmt.Sprintf("s%d-%d", seg, i), 64, 64))
			}
			first.mu.Lock()
			_ = first.rollLocked(first.nowFunc())
			first.mu.Unlock()
		}
		if err := first.Close(); err != nil {
			t.Fatal(err)
		}
		// Segments hold 1, 2 and 3 frames; a ring of 1 keeps only the last.
		opts := testOptions(dir)
		opts.RingSegments = 1
		second := openTestWriter(t, opts, newFakeClock())
		stats := second.Stats()
		if stats.SegmentsDropped != 2 || stats.SegmentFramesDropped != 3 || stats.RingDepth != 1 {
			t.Fatalf("stats = %+v", stats)
		}
		completed, _ := spoolSegments(t, dir)
		if strings.Join(completed, ",") != segmentName(3, false) {
			t.Fatalf("segments = %v", completed)
		}
	})
}

func TestWriterSequenceContinuityAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	clock := newFakeClock()

	first := openTestWriter(t, testOptions(dir), clock)
	r1 := testRecord("r-1", 200, 100)
	first.Append(r1)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second := openTestWriter(t, testOptions(dir), clock)
	r2 := testRecord("r-2", 200, 100)
	r3 := testRecord("r-3", 200, 100)
	second.Append(r2)
	second.Append(r3)
	crash(t, second)
	// The crash also cut the last frame in half.
	openPath := filepath.Join(dir, segmentName(2, true))
	data, err := os.ReadFile(openPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(openPath, data[:len(data)-10], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, open := spoolSegments(t, dir); len(open) != 1 {
		t.Fatalf("crash should leave one open segment, have %v", open)
	}

	third := openTestWriter(t, testOptions(dir), clock)
	completed, open := spoolSegments(t, dir)
	if len(open) != 0 || strings.Join(completed, ",") != segmentName(1, false)+","+segmentName(2, false) {
		t.Fatalf("after restart: completed=%v open=%v", completed, open)
	}
	r4 := testRecord("r-4", 200, 100)
	third.Append(r4)
	if err = third.Close(); err != nil {
		t.Fatal(err)
	}
	completed, _ = spoolSegments(t, dir)
	if strings.Join(completed, ",") != segmentName(1, false)+","+segmentName(2, false)+","+segmentName(3, false) {
		t.Fatalf("segments = %v", completed)
	}

	dec, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	for _, tc := range []struct {
		seq  uint64
		want []Record
		tail bool
	}{
		{1, []Record{r1}, false},
		{2, []Record{r2}, true},
		{3, []Record{r4}, false},
	} {
		f, err := os.Open(filepath.Join(dir, segmentName(tc.seq, false)))
		if err != nil {
			t.Fatal(err)
		}
		records, stats, err := ReadSegment(f, dec)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if stats.TruncatedTail != tc.tail || stats.Corrupt {
			t.Fatalf("segment %d stats = %+v", tc.seq, stats)
		}
		assertRecordsEqual(t, records, tc.want)
	}
}

func TestWriterNextSequence(t *testing.T) {
	cases := []struct {
		name     string
		existing []string
		want     uint64
	}{
		{"empty spool", nil, 1},
		{"after completed segments", []string{segmentName(3, false), segmentName(7, false)}, 8},
		{"an open segment counts", []string{segmentName(3, false), segmentName(9, true)}, 10},
		{"other files are ignored", []string{"archiver-status.json", "seg-x.wal", "seg-000000000004.wal.tmp", LockFileName}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range tc.existing {
				if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			w := openTestWriter(t, testOptions(dir), newFakeClock())
			w.Append(testRecord("r", 10, 10))
			if _, err := os.Stat(filepath.Join(dir, segmentName(tc.want, true))); err != nil {
				t.Fatalf("expected the new segment at sequence %d: %v", tc.want, err)
			}
			if _, open := spoolSegments(t, dir); len(open) != 1 {
				t.Fatalf("open segments = %v, want only the new one", open)
			}
		})
	}
}

func TestWriterSecondWriterRefusedByLock(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(testOptions(dir))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Open(testOptions(dir)); !errors.Is(err, ErrSpoolLocked) {
		t.Fatalf("second Open error = %v, want ErrSpoolLocked", err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(testOptions(dir))
	if err != nil {
		t.Fatalf("Open after Close: %v", err)
	}
	if err = again.Close(); err != nil {
		t.Fatal(err)
	}
	if err = again.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestWriterOversizeRecord(t *testing.T) {
	cases := []struct {
		name                 string
		requestLen, respLen  int
		maxRecordBytes       int64
		omitRequest, omitRsp bool
	}{
		{"under the cap", 30, 20, 50, false, false},
		{"larger request omitted first", 100, 10, 50, true, false},
		{"larger response omitted first", 10, 100, 50, false, true},
		{"both omitted when the smaller still exceeds", 80, 60, 50, true, true},
		{"cap disabled", 100, 100, 0, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			opts := testOptions(dir)
			opts.MaxRecordBytes = tc.maxRecordBytes
			w := openTestWriter(t, opts, newFakeClock())
			rec := testRecord("big", tc.requestLen, tc.respLen)
			w.Append(rec)
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			segments := readSpool(t, dir)
			records := segments[segmentName(1, false)]
			if len(records) != 1 {
				t.Fatalf("records = %d, want 1", len(records))
			}
			got := records[0]
			for _, section := range []struct {
				name    string
				omitted bool
				body    []byte
				orig    []byte
			}{
				{"request", tc.omitRequest, got.Request, rec.Request},
				{"response", tc.omitRsp, got.Response, rec.Response},
			} {
				meta := ojson.GetBytes(got.Meta, section.name)
				if meta.Get("omitted").Bool() != section.omitted {
					t.Fatalf("%s.omitted = %v, want %v (meta %s)", section.name, meta.Get("omitted").Bool(), section.omitted, got.Meta)
				}
				wantReason := ""
				if section.omitted {
					wantReason = OmittedReasonOversize
				}
				if meta.Get("omitted_reason").String() != wantReason {
					t.Fatalf("%s.omitted_reason = %q", section.name, meta.Get("omitted_reason").String())
				}
				if meta.Get("bytes").Int() != int64(len(section.orig)) {
					t.Fatalf("%s.bytes changed to %d", section.name, meta.Get("bytes").Int())
				}
				if section.omitted && len(section.body) != 0 {
					t.Fatalf("%s body kept %d bytes", section.name, len(section.body))
				}
				if !section.omitted && !bytes.Equal(section.body, section.orig) {
					t.Fatalf("%s body changed", section.name)
				}
			}
			if got.Meta[0] != '{' || ojson.GetBytes(got.Meta, "request_id").String() != "big" {
				t.Fatalf("meta lost: %s", got.Meta)
			}
			wantOversize := uint64(0)
			if tc.omitRequest || tc.omitRsp {
				wantOversize = 1
			}
			if stats := w.Stats(); stats.RecordsOversize != wantOversize || stats.FramesWritten != 1 || stats.FramesDropped != 0 {
				t.Fatalf("stats = %+v", stats)
			}
		})
	}
}

func TestWriterOversizeRecordWithInvalidMetaIsDropped(t *testing.T) {
	dir := t.TempDir()
	opts := testOptions(dir)
	opts.MaxRecordBytes = 10
	w := openTestWriter(t, opts, newFakeClock())
	w.Append(Record{Meta: []byte(`not json`), Request: bytes.Repeat([]byte("x"), 100)})
	if stats := w.Stats(); stats.FramesDropped != 1 || stats.FramesWritten != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestWriterReadOnlySpoolDropsFrames(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	cases := []struct {
		name       string
		openBefore bool // a segment is already open when the directory turns read-only
	}{
		{"no open segment", false},
		{"open segment cannot roll", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			opts := testOptions(dir)
			opts.SegmentMaxBytes = 1
			clock := newFakeClock()
			w := openTestWriter(t, opts, clock)
			if tc.openBefore {
				w.opts.SegmentMaxBytes = 1 << 30
				w.Append(testRecord("before", 64, 64))
				w.opts.SegmentMaxBytes = 1
			}
			if err := os.Chmod(dir, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

			hook := logtest.NewGlobal()
			defer hook.Reset()
			for i := 0; i < 3; i++ {
				w.Append(testRecord(fmt.Sprintf("r-%d", i), 64, 64))
			}
			stats := w.Stats()
			if tc.openBefore {
				// The frames reach the open file, but its rename fails, so it stays
				// .wal.open for the next start to roll; nothing is lost.
				if stats.FramesWritten != 2 || stats.FramesDropped != 2 {
					t.Fatalf("stats = %+v", stats)
				}
			} else if stats.FramesDropped != 3 || stats.FramesWritten != 0 {
				t.Fatalf("stats = %+v", stats)
			}
			if errorLogs := countLevel(hook, log.ErrorLevel); errorLogs != 1 {
				t.Fatalf("logged %d errors within a minute, want 1", errorLogs)
			}
			clock.Advance(time.Minute)
			w.Append(testRecord("later", 64, 64))
			if errorLogs := countLevel(hook, log.ErrorLevel); errorLogs != 2 {
				t.Fatalf("logged %d errors after a minute, want 2", errorLogs)
			}
			if err := w.Close(); err != nil {
				t.Logf("Close on a read-only spool: %v", err)
			}
		})
	}
}

func countLevel(hook *logtest.Hook, level log.Level) int {
	n := 0
	for _, entry := range hook.AllEntries() {
		if entry.Level == level {
			n++
		}
	}
	return n
}

func TestWriterAppendAfterCloseIsDropped(t *testing.T) {
	w := openTestWriter(t, testOptions(t.TempDir()), newFakeClock())
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	w.Append(testRecord("late", 10, 10))
	w.Tick()
	if stats := w.Stats(); stats.FramesDropped != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	var nilWriter *Writer
	nilWriter.Append(testRecord("nil", 10, 10))
	nilWriter.Tick()
	if err := nilWriter.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWriterLargeFrameBypassesBuffer(t *testing.T) {
	dir := t.TempDir()
	w := openTestWriter(t, testOptions(dir), newFakeClock())
	small := testRecord("small", 64, 64)
	large := testRecord("large", segmentBufferSize+1024, 64)
	w.Append(small)
	w.Append(large)
	// The small frame was flushed whole before the large one went to the file.
	info, err := os.Stat(filepath.Join(dir, segmentName(1, true)))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != frameLen(t, small)+frameLen(t, large) {
		t.Fatalf("file holds %d bytes, want both frames", info.Size())
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	assertRecordsEqual(t, readSpool(t, dir)[segmentName(1, false)], []Record{small, large})
}

func TestWriterConcurrentAppends(t *testing.T) {
	dir := t.TempDir()
	opts := testOptions(dir)
	opts.SegmentMaxBytes = 4 << 10
	opts.RingSegments = 1000
	w := openTestWriter(t, opts, newFakeClock())
	const workers, perWorker = 8, 25
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				w.Append(testRecord(fmt.Sprintf("g%d-%d", g, i), 512, 256))
				if i%10 == 0 {
					w.Tick()
					_ = w.Stats()
				}
			}
		}(g)
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	total := 0
	seen := map[string]bool{}
	for _, records := range readSpool(t, dir) {
		total += len(records)
		for _, id := range requestIDs(records) {
			seen[id] = true
		}
	}
	if total != workers*perWorker || len(seen) != workers*perWorker {
		t.Fatalf("decoded %d records (%d distinct), want %d", total, len(seen), workers*perWorker)
	}
	if stats := w.Stats(); stats.FramesWritten != workers*perWorker || stats.FramesDropped != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestParseSegmentName(t *testing.T) {
	cases := []struct {
		name string
		want segmentEntry
		ok   bool
	}{
		{"seg-000000000001.wal", segmentEntry{seq: 1}, true},
		{"seg-000000000042.wal.open", segmentEntry{seq: 42, open: true}, true},
		{"seg-1234567890123.wal", segmentEntry{seq: 1234567890123}, true},
		{"seg-.wal", segmentEntry{}, false},
		{"seg-00000000000a.wal", segmentEntry{}, false},
		{"seg-000000000001.wal.tmp", segmentEntry{}, false},
		{"spool.lock", segmentEntry{}, false},
		{"archiver-state.json", segmentEntry{}, false},
	}
	for _, tc := range cases {
		got, ok := parseSegmentName(tc.name)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseSegmentName(%q) = %+v, %v; want %+v, %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
	if segmentName(1, false) != "seg-000000000001.wal" || segmentName(12, true) != "seg-000000000012.wal.open" {
		t.Fatal("segment names do not match the spec")
	}
}

func TestCountFrames(t *testing.T) {
	enc, _ := newCodec(t)
	frame, err := enc.Encode(sampleRecord())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		data []byte
		want uint64
	}{
		{"empty", nil, 0},
		{"two frames", append(append([]byte(nil), frame...), frame...), 2},
		{"truncated tail not counted", append(append([]byte(nil), frame...), frame[:len(frame)-1]...), 1},
		{"corrupt header stops", append(append([]byte(nil), frame...), []byte("garbage-garbage-garbage")...), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "seg.wal")
			if err := os.WriteFile(path, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			if got := countFrames(path); got != tc.want {
				t.Fatalf("countFrames = %d, want %d", got, tc.want)
			}
		})
	}
}
