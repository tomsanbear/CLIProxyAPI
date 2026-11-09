package archive

import (
	"errors"
	"fmt"

	"github.com/MoeForge/moego/ojson"
)

// OmittedReasonOversize marks a body left out by the size cap (spec section 7.2).
const OmittedReasonOversize = "oversize"

// capRecordBodies applies the size cap of spec section 7.2: when the request
// and response bodies together exceed maxBodyBytes, the larger body is omitted
// first, then the other one too if the remaining body alone still exceeds the
// cap. Each omitted body becomes an empty section and its meta descriptor gets
// omitted: true and omitted_reason: "oversize"; bytes and sha256 keep
// describing the original. It reports whether anything was omitted.
func capRecordBodies(rec Record, maxBodyBytes int64) (Record, bool, error) {
	requestLen, responseLen := int64(len(rec.Request)), int64(len(rec.Response))
	if maxBodyBytes <= 0 || requestLen+responseLen <= maxBodyBytes {
		return rec, false, nil
	}
	omitRequest := requestLen >= responseLen
	remaining := requestLen
	if omitRequest {
		remaining = responseLen
	}
	omitBoth := remaining > maxBodyBytes
	capped, errOmit := omitRecordBodies(rec, omitRequest || omitBoth, !omitRequest || omitBoth)
	if errOmit != nil {
		return rec, false, errOmit
	}
	return capped, true, nil
}

// omitRecordBodies empties the selected bodies and marks them omitted in the
// meta JSON. The meta must be a JSON object; the input record is not modified.
func omitRecordBodies(rec Record, request, response bool) (Record, error) {
	if !ojson.ValidBytes(rec.Meta) || !ojson.ParseBytes(rec.Meta).IsObject() {
		return rec, errors.New("archive: record meta is not a JSON object")
	}
	doc := ojson.NewDoc(rec.Meta)
	for _, section := range []struct {
		name string
		omit bool
	}{{"request", request}, {"response", response}} {
		if !section.omit {
			continue
		}
		if errSet := doc.Set(section.name+".omitted", true); errSet != nil {
			return rec, fmt.Errorf("archive: mark %s omitted: %w", section.name, errSet)
		}
		if errSet := doc.Set(section.name+".omitted_reason", OmittedReasonOversize); errSet != nil {
			return rec, fmt.Errorf("archive: mark %s omitted: %w", section.name, errSet)
		}
	}
	out := Record{Meta: doc.Bytes(), Request: rec.Request, Response: rec.Response}
	if request {
		out.Request = nil
	}
	if response {
		out.Response = nil
	}
	return out, nil
}
