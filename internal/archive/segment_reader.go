package archive

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// ReadStats counts what a segment read encountered (spec section 6).
type ReadStats struct {
	Frames         int  `json:"frames"`
	UnknownVersion int  `json:"unknown_version"`
	CRCMismatch    int  `json:"crc_mismatch"`
	BadRecord      int  `json:"bad_record"`
	TruncatedTail  bool `json:"truncated_tail"`
	Corrupt        bool `json:"corrupt"`
}

// ReadSegment walks every frame in r, applying the damage rules of the spec:
// a truncated tail ends the segment quietly, bad magic ends it as corrupt,
// and unknown versions, CRC mismatches and undecodable records are skipped and
// counted. Records read before any stop are returned. The returned error is
// non-nil only for I/O failures other than EOF.
func ReadSegment(r io.Reader, dec *Decoder) ([]Record, ReadStats, error) {
	var records []Record
	var stats ReadStats
	br := bufio.NewReaderSize(r, 1<<20)
	header := make([]byte, HeaderLen)
	for {
		n, err := io.ReadFull(br, header)
		if err != nil {
			if errors.Is(err, io.EOF) && n == 0 {
				return records, stats, nil
			}
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				stats.TruncatedTail = true
				return records, stats, nil
			}
			return records, stats, fmt.Errorf("archive: read header: %w", err)
		}
		h, err := ParseHeader(header)
		if err != nil {
			stats.Corrupt = true
			return records, stats, nil
		}
		payload := make([]byte, h.PayloadLen)
		if _, err := io.ReadFull(br, payload); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				stats.TruncatedTail = true
				return records, stats, nil
			}
			return records, stats, fmt.Errorf("archive: read payload: %w", err)
		}
		rec, err := dec.Decode(h, payload)
		switch {
		case err == nil:
			records = append(records, rec)
			stats.Frames++
		case errors.Is(err, ErrUnknownVersion):
			stats.UnknownVersion++
		case errors.Is(err, ErrCRCMismatch):
			stats.CRCMismatch++
		default:
			stats.BadRecord++
		}
	}
}
