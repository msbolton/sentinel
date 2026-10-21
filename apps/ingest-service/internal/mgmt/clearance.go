package mgmt

import (
	"bytes"
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

// withoutDecoderTexts returns a status without the texts a decoder's error can
// repeat from what a sender sent: the ones withoutErrorTexts removes or empties
// in the detail, and the status's last error when it is the feed's last decode
// error.
//
// The last error is that when it is the same text as detail.lastDecodeError.
// The feed runner records a payload's decode error as the last error, and the
// supervisor reports that same text under lastDecodeError whichever driver the
// feed runs (feed.Supervisor.statusOf), so the two are one text exactly when
// the last error came from a decode. Any other last error, a failed dial say,
// says nothing of what a sender sent and is left. The status is a copy; what
// the caller holds is not changed.
func withoutDecoderTexts(st registry.Status) registry.Status {
	if st.LastError != "" && st.LastError == lastDecodeError(st.Detail) {
		st.LastError = ""
	}
	st.Detail = withoutErrorTexts(st.Detail)
	return st
}

// lastDecodeError is the detail's lastDecodeError, or "" when it has none or
// the detail cannot be read.
func lastDecodeError(detail json.RawMessage) string {
	var d map[string]json.RawMessage
	if json.Unmarshal(detail, &d) != nil {
		return ""
	}
	var text string
	if json.Unmarshal(d["lastDecodeError"], &text) != nil {
		return ""
	}
	return text
}

// blankedDetailKeys are the keys at the top of a status detail whose strings
// the gate empties, keeping the keys and the rest of their shape so that a
// client reading them finds the fields it expects. Both are a driver's account
// of how its own requests went, and after a response that would not parse the
// decoder's text is in it:
//
//	lastError  httpjson: the last error of its poll
//	errors     celestrak: the error of each selection's last fetch, by selection
//
// Where a driver uses a key for a number (intelsky and aisstream count their
// failures under errors) the number stays: only strings are emptied.
// TestEveryErrorKeyIsAccountedFor holds this list, and the keys that are
// removed instead, to the keys the code writes.
var blankedDetailKeys = []string{"errors", "lastError"}

// withoutErrorTexts returns a status detail without the texts a decoder's
// error can repeat from what a sender sent. It removes the feed's
// lastDecodeError and each sender's lastError, and empties the strings under
// the keys in blankedDetailKeys. Counts, times and reason codes stay, and so
// does every other key, a lastError nested elsewhere included (aisstream's
// connections report their own dial errors). A detail that cannot be read as
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
	for _, key := range blankedDetailKeys {
		if raw, ok := d[key]; ok {
			d[key] = blankStrings(raw)
		}
	}
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

// blankStrings returns a JSON value with every string in it empty: the shape,
// the keys and the numbers stay, as they were written, so a count is neither
// hidden nor rounded. A value that cannot be read becomes an empty string.
func blankStrings(raw json.RawMessage) json.RawMessage {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return json.RawMessage(`""`)
	}
	out, err := json.Marshal(blank(v))
	if err != nil {
		return json.RawMessage(`""`)
	}
	return out
}

// blank empties the strings of a decoded JSON value, in place.
func blank(v any) any {
	switch x := v.(type) {
	case string:
		return ""
	case map[string]any:
		for k, e := range x {
			x[k] = blank(e)
		}
	case []any:
		for i, e := range x {
			x[i] = blank(e)
		}
	}
	return v
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

// callerRank is the rank of the caller's clearance.
func callerRank(r *http.Request) int {
	return classification.Rank(middleware.Classification(r.Context()))
}

// visibleEmplacements returns a status detail whose emplacements (the
// heartbeat state of a connection's emplacements, detail.emplacements) are
// those the caller may see. An entry above rank is left out, as the API
// leaves out the emplacement itself. When the caller is not cleared for the
// connection, the names are blanked like the detail's other texts. A detail
// without the key comes back as it is.
func visibleEmplacements(detail json.RawMessage, rank int, cleared bool) json.RawMessage {
	var d map[string]json.RawMessage
	if json.Unmarshal(detail, &d) != nil || d == nil {
		return detail
	}
	raw, ok := d["emplacements"]
	if !ok {
		return detail
	}
	var list []map[string]json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		delete(d, "emplacements")
	} else {
		kept := make([]map[string]json.RawMessage, 0, len(list))
		for _, e := range list {
			var level string
			_ = json.Unmarshal(e["classification"], &level)
			if classification.Rank(level) > rank {
				continue
			}
			if !cleared {
				e["name"] = json.RawMessage(`""`)
			}
			kept = append(kept, e)
		}
		out, err := json.Marshal(kept)
		if err != nil {
			return json.RawMessage("{}")
		}
		d["emplacements"] = out
	}
	out, err := json.Marshal(d)
	if err != nil {
		return json.RawMessage("{}")
	}
	return out
}
