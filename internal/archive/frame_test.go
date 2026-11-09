package archive

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"testing"
)

func crcOf(b []byte) uint32 { return crc32.Checksum(b, crcTable) }

func newCodec(t *testing.T) (*Encoder, *Decoder) {
	t.Helper()
	enc, err := NewEncoder()
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = enc.Close(); dec.Close() })
	return enc, dec
}

func sampleRecord() Record {
	return Record{
		Meta:     []byte(`{"v":1,"request_id":"r1","attempt":1}`),
		Request:  bytes.Repeat([]byte(`{"messages":[{"role":"user","content":"hello"}]}`), 50),
		Response: []byte("event: message_stop\ndata: {}\n\n"),
	}
}

func TestFrameRoundTrip(t *testing.T) {
	enc, dec := newCodec(t)
	rec := sampleRecord()
	frame, err := enc.Encode(rec)
	if err != nil {
		t.Fatal(err)
	}
	h, err := ParseHeader(frame)
	if err != nil {
		t.Fatal(err)
	}
	if h.Version != Version || h.Flags != FlagZstd {
		t.Fatalf("header = %+v", h)
	}
	if int(h.PayloadLen) != len(frame)-HeaderLen {
		t.Fatalf("payload_len %d, frame has %d payload bytes", h.PayloadLen, len(frame)-HeaderLen)
	}
	if int(h.RecordLen) != recordSectionsOverhead+len(rec.Meta)+len(rec.Request)+len(rec.Response) {
		t.Fatalf("record_len %d", h.RecordLen)
	}
	got, err := dec.Decode(h, frame[HeaderLen:])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Meta, rec.Meta) || !bytes.Equal(got.Request, rec.Request) || !bytes.Equal(got.Response, rec.Response) {
		t.Fatal("decoded record differs")
	}
}

func TestEmptySectionsRoundTrip(t *testing.T) {
	enc, dec := newCodec(t)
	frame, err := enc.Encode(Record{Meta: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	records, stats, err := ReadSegment(bytes.NewReader(frame), dec)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Frames != 1 || len(records) != 1 || len(records[0].Request) != 0 || len(records[0].Response) != 0 {
		t.Fatalf("records=%d stats=%+v", len(records), stats)
	}
}

func TestReadSegmentDamage(t *testing.T) {
	enc, dec := newCodec(t)
	frame, err := enc.Encode(sampleRecord())
	if err != nil {
		t.Fatal(err)
	}
	patched := func(mutate func([]byte)) []byte {
		b := append([]byte(nil), frame...)
		mutate(b)
		return b
	}

	cases := []struct {
		name    string
		segment []byte
		want    ReadStats
	}{
		{"empty", nil, ReadStats{}},
		{"two frames", append(append([]byte(nil), frame...), frame...), ReadStats{Frames: 2}},
		{"truncated header", frame[:HeaderLen-3], ReadStats{TruncatedTail: true}},
		{"truncated payload", frame[:len(frame)-5], ReadStats{TruncatedTail: true}},
		{"frame then truncated", append(append([]byte(nil), frame...), frame[:HeaderLen+1]...), ReadStats{Frames: 1, TruncatedTail: true}},
		{"bad magic stops", append(patched(func(b []byte) { b[0] = 'X' }), frame...), ReadStats{Corrupt: true}},
		{"unknown version skipped", append(patched(func(b []byte) { b[4] = 99 }), frame...), ReadStats{Frames: 1, UnknownVersion: 1}},
		{"crc mismatch skipped", append(patched(func(b []byte) { b[HeaderLen] ^= 0xFF }), frame...), ReadStats{Frames: 1, CRCMismatch: 1}},
		{"record_len mismatch skipped", append(patched(func(b []byte) {
			binary.LittleEndian.PutUint32(b[12:16], binary.LittleEndian.Uint32(b[12:16])+1)
		}), frame...), ReadStats{Frames: 1, BadRecord: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			records, stats, err := ReadSegment(bytes.NewReader(tc.segment), dec)
			if err != nil {
				t.Fatal(err)
			}
			if stats != tc.want {
				t.Fatalf("stats = %+v, want %+v", stats, tc.want)
			}
			if len(records) != tc.want.Frames {
				t.Fatalf("records = %d, want %d", len(records), tc.want.Frames)
			}
		})
	}
}

func TestParseHeaderRejectsOversizedPayload(t *testing.T) {
	b := make([]byte, HeaderLen)
	copy(b, Magic[:])
	b[4] = Version
	binary.LittleEndian.PutUint32(b[8:12], MaxPayloadLen+1)
	if _, err := ParseHeader(b); err == nil {
		t.Fatal("expected error for oversized payload_len")
	}
}

func TestUncompressedPayloadDecodes(t *testing.T) {
	_, dec := newCodec(t)
	rec := sampleRecord()
	raw := packRecord(rec)
	frame := make([]byte, HeaderLen+len(raw))
	putHeader(frame, Header{Version: Version, Flags: 0, PayloadLen: uint32(len(raw)), RecordLen: uint32(len(raw)), CRC32C: crcOf(raw)})
	copy(frame[HeaderLen:], raw)
	records, stats, err := ReadSegment(bytes.NewReader(frame), dec)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Frames != 1 || !bytes.Equal(records[0].Request, rec.Request) {
		t.Fatalf("stats=%+v", stats)
	}
}
