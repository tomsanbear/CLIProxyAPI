// Package archive implements the archive write-ahead log format shared with
// cliproxyapi-archiver. The format is specified in docs/archive-wal-format.md;
// this file is the frame and record codec (sections 5 to 7 of the spec).
package archive

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"

	"github.com/klauspost/compress/zstd"
)

const (
	// HeaderLen is the fixed frame header size in every format version.
	HeaderLen = 20
	// Version is the frame version this package writes.
	Version = 1
	// FlagZstd marks a zstd-compressed payload.
	FlagZstd = 0x01

	// MaxPayloadLen bounds the stored payload of one frame.
	MaxPayloadLen = 128 << 20
	// MaxRecordLen bounds the decompressed record of one frame.
	MaxRecordLen = 256 << 20

	recordSectionsOverhead = 12
)

// Magic opens every frame.
var Magic = [4]byte{'C', 'P', 'A', 'W'}

var crcTable = crc32.MakeTable(crc32.Castagnoli)

// Record is one archived turn: the meta JSON and the two bodies.
type Record struct {
	Meta     []byte
	Request  []byte
	Response []byte
}

// Header is a decoded frame header.
type Header struct {
	Version    uint8
	Flags      uint8
	PayloadLen uint32
	RecordLen  uint32
	CRC32C     uint32
}

// Encoder writes frames. It owns a zstd encoder and is not safe for concurrent use.
type Encoder struct {
	zstd *zstd.Encoder
}

// NewEncoder returns an Encoder compressing with zstd at the default level.
func NewEncoder() (*Encoder, error) {
	enc, err := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.SpeedDefault),
		zstd.WithEncoderConcurrency(1),
		zstd.WithEncoderCRC(true),
	)
	if err != nil {
		return nil, fmt.Errorf("archive: zstd encoder: %w", err)
	}
	return &Encoder{zstd: enc}, nil
}

// Close releases the encoder's resources.
func (e *Encoder) Close() error {
	if e == nil || e.zstd == nil {
		return nil
	}
	return e.zstd.Close()
}

// Encode returns the complete frame bytes (header plus payload) for rec.
func (e *Encoder) Encode(rec Record) ([]byte, error) {
	raw := packRecord(rec)
	if len(raw) > MaxRecordLen {
		return nil, fmt.Errorf("archive: record of %d bytes exceeds %d", len(raw), MaxRecordLen)
	}
	payload := e.zstd.EncodeAll(raw, nil)
	if len(payload) > MaxPayloadLen {
		return nil, fmt.Errorf("archive: payload of %d bytes exceeds %d", len(payload), MaxPayloadLen)
	}
	frame := make([]byte, HeaderLen+len(payload))
	putHeader(frame, Header{
		Version:    Version,
		Flags:      FlagZstd,
		PayloadLen: uint32(len(payload)),
		RecordLen:  uint32(len(raw)),
		CRC32C:     crc32.Checksum(payload, crcTable),
	})
	copy(frame[HeaderLen:], payload)
	return frame, nil
}

func putHeader(dst []byte, h Header) {
	copy(dst[0:4], Magic[:])
	dst[4] = h.Version
	dst[5] = h.Flags
	binary.LittleEndian.PutUint16(dst[6:8], 0)
	binary.LittleEndian.PutUint32(dst[8:12], h.PayloadLen)
	binary.LittleEndian.PutUint32(dst[12:16], h.RecordLen)
	binary.LittleEndian.PutUint32(dst[16:20], h.CRC32C)
}

// ErrBadMagic reports a header that does not start with Magic.
var ErrBadMagic = errors.New("archive: bad frame magic")

// ParseHeader decodes a 20-byte header. It validates only the magic and the
// size limits; version and checksum are the caller's decision.
func ParseHeader(b []byte) (Header, error) {
	if len(b) < HeaderLen {
		return Header{}, fmt.Errorf("archive: header needs %d bytes, have %d", HeaderLen, len(b))
	}
	if [4]byte(b[0:4]) != Magic {
		return Header{}, ErrBadMagic
	}
	h := Header{
		Version:    b[4],
		Flags:      b[5],
		PayloadLen: binary.LittleEndian.Uint32(b[8:12]),
		RecordLen:  binary.LittleEndian.Uint32(b[12:16]),
		CRC32C:     binary.LittleEndian.Uint32(b[16:20]),
	}
	if h.PayloadLen > MaxPayloadLen {
		return Header{}, fmt.Errorf("archive: payload length %d exceeds %d", h.PayloadLen, MaxPayloadLen)
	}
	return h, nil
}

// Decoder turns payloads back into records. It owns a zstd decoder and is not
// safe for concurrent use.
type Decoder struct {
	zstd *zstd.Decoder
}

// NewDecoder returns a Decoder.
func NewDecoder() (*Decoder, error) {
	dec, err := zstd.NewReader(nil,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(MaxRecordLen),
	)
	if err != nil {
		return nil, fmt.Errorf("archive: zstd decoder: %w", err)
	}
	return &Decoder{zstd: dec}, nil
}

// Close releases the decoder's resources.
func (d *Decoder) Close() {
	if d != nil && d.zstd != nil {
		d.zstd.Close()
	}
}

// ErrCRCMismatch reports a payload whose CRC-32C does not match its header.
var ErrCRCMismatch = errors.New("archive: payload crc32c mismatch")

// ErrUnknownVersion reports a frame version this package cannot decode.
var ErrUnknownVersion = errors.New("archive: unknown frame version")

// Decode checks the payload against h and unpacks the record.
func (d *Decoder) Decode(h Header, payload []byte) (Record, error) {
	if h.Version != Version {
		return Record{}, ErrUnknownVersion
	}
	if uint32(len(payload)) != h.PayloadLen {
		return Record{}, fmt.Errorf("archive: payload is %d bytes, header says %d", len(payload), h.PayloadLen)
	}
	if crc32.Checksum(payload, crcTable) != h.CRC32C {
		return Record{}, ErrCRCMismatch
	}
	if h.RecordLen > MaxRecordLen {
		return Record{}, fmt.Errorf("archive: record length %d exceeds %d", h.RecordLen, MaxRecordLen)
	}
	raw := payload
	switch h.Flags & FlagZstd {
	case FlagZstd:
		var err error
		raw, err = d.zstd.DecodeAll(payload, make([]byte, 0, h.RecordLen))
		if err != nil {
			return Record{}, fmt.Errorf("archive: zstd decode: %w", err)
		}
	default:
	}
	if uint32(len(raw)) != h.RecordLen {
		return Record{}, fmt.Errorf("archive: record is %d bytes, header says %d", len(raw), h.RecordLen)
	}
	return unpackRecord(raw)
}

func packRecord(rec Record) []byte {
	raw := make([]byte, 0, recordSectionsOverhead+len(rec.Meta)+len(rec.Request)+len(rec.Response))
	for _, section := range [][]byte{rec.Meta, rec.Request, rec.Response} {
		raw = binary.LittleEndian.AppendUint32(raw, uint32(len(section)))
		raw = append(raw, section...)
	}
	return raw
}

func unpackRecord(raw []byte) (Record, error) {
	var rec Record
	sections := []*[]byte{&rec.Meta, &rec.Request, &rec.Response}
	offset := 0
	for i, section := range sections {
		if len(raw)-offset < 4 {
			return Record{}, fmt.Errorf("archive: record section %d: missing length", i)
		}
		n := int(binary.LittleEndian.Uint32(raw[offset : offset+4]))
		offset += 4
		if len(raw)-offset < n {
			return Record{}, fmt.Errorf("archive: record section %d: %d bytes declared, %d remain", i, n, len(raw)-offset)
		}
		*section = append([]byte(nil), raw[offset:offset+n]...)
		offset += n
	}
	if offset != len(raw) {
		return Record{}, fmt.Errorf("archive: record has %d trailing bytes", len(raw)-offset)
	}
	return rec, nil
}
