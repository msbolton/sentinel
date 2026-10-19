package mgmt

import (
	"encoding/json"
	"net/http"

	"github.com/sentinel/go-common/classification"
	"github.com/sentinel/go-common/middleware"

	"github.com/sentinel/ingest-service/internal/registry"
)

// clearedFor reports whether the caller's clearance reaches the feed's
// classification. It is the one comparison behind everything that can show a
// caller what a sender sent: a feed's dead letters, and the decoder error texts
// in its status. A level that is missing or not recognised ranks as
// UNCLASSIFIED (classification.Rank), on either side.
func clearedFor(r *http.Request, f registry.Feed) bool {
	return classification.Rank(middleware.Classification(r.Context())) >= classification.Rank(f.Common().Classification)
}

// withoutErrorTexts returns a status detail without the texts a decoder's
// error can repeat from what a sender sent: the feed's lastDecodeError and each
// sender's lastError. Counts, times and reason codes stay, and so does every
// other key, wherever it is named lastError. A detail that cannot be read as
// expected is cut down rather than passed on: a detail that is not an object
// becomes {}, and senders that are not a list of objects are left out.
//
// The detail is the status's own, shared with whoever reported it, so the
// result is a new document.
func withoutErrorTexts(detail json.RawMessage) json.RawMessage {
	var d map[string]json.RawMessage
	if err := json.Unmarshal(detail, &d); err != nil {
		return json.RawMessage("{}")
	}
	if d == nil { // JSON null: nothing in it
		return detail
	}
	delete(d, "lastDecodeError")
	if raw, ok := d["senders"]; ok {
		if cut, ok := withoutSenderErrors(raw); ok {
			d["senders"] = cut
		} else {
			delete(d, "senders")
		}
	}
	out, err := json.Marshal(d)
	if err != nil {
		return json.RawMessage("{}")
	}
	return out
}

// withoutSenderErrors drops lastError from each sender; it reports false for
// senders that are not a list of objects. Each value is kept as it was written,
// so a count is not rounded on its way through.
func withoutSenderErrors(raw json.RawMessage) (json.RawMessage, bool) {
	var senders []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &senders); err != nil {
		return nil, false
	}
	for _, s := range senders {
		delete(s, "lastError")
	}
	out, err := json.Marshal(senders)
	return out, err == nil
}
