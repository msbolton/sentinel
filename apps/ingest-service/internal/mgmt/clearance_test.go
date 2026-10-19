package mgmt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sentinel/go-common/middleware"

	"github.com/sentinel/ingest-service/internal/registry"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

// rejectedDetail is the status detail of a stream feed that has refused
// messages, as the driver reports it: the feed's last decode error and, for each
// sender, the last error its messages drew. Both texts can repeat what a sender
// sent. Everything else in it is a count, a time or a reason code.
const rejectedDetail = `{
	"transport": "tcpserver", "decoder": "sentinel", "framing": "line",
	"connected": true, "connections": 2, "malformed": 3, "distinctSenders": 2,
	"lastDecodeError": "proto: (line 1:2): unknown field \"launchCode\"",
	"senders": [
		{"remote": "10.0.0.5:51234", "since": "2026-10-18T12:00:00Z", "lastAt": "2026-10-18T12:01:00Z", "messages": 9, "connected": true,
		 "malformed": 2, "lastReason": "parse_error", "lastError": "unknown field \"launchCode\"", "lastMalformedAt": "2026-10-18T12:01:00Z"},
		{"remote": "10.0.0.6:40000", "since": "2026-10-18T12:00:30Z", "messages": 4, "connected": false, "malformed": 1,
		 "lastReason": "missing_identity", "lastError": "source_entity_id or an identity is required", "lastMalformedAt": "2026-10-18T12:00:40Z"},
		{"remote": "10.0.0.7:40001", "since": "2026-10-18T12:00:50Z", "messages": 1, "connected": true, "malformed": 0}
	]
}`

// openskySecret is the built-in opensky config with a classification added.
const openskySecret = `{"intervalSec": 10, "viewportTtlSec": 90, "minPollSpacingSec": 3, "maxAreaSqDeg": 400, "maxIntervalSec": 60, "creditFloor": 200, "staleWarnSec": 120, "staleCriticalSec": 300, "classification": "SECRET"}`

// decodeObject reads a JSON object into the generic form two documents are
// compared in.
func decodeObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return m
}

// withoutTexts is the detail the way a caller who may not see the error texts
// reads it: the same document, less lastDecodeError and each sender's lastError.
func withoutTexts(t *testing.T, full map[string]any) map[string]any {
	t.Helper()
	out := map[string]any{}
	for k, v := range full {
		out[k] = v
	}
	delete(out, "lastDecodeError")
	var senders []any
	for _, s := range full["senders"].([]any) {
		kept := map[string]any{}
		for k, v := range s.(map[string]any) {
			if k != "lastError" {
				kept[k] = v
			}
		}
		senders = append(senders, kept)
	}
	out["senders"] = senders
	return out
}

// classifiedFeedServer is a server whose opensky feed is classified SECRET and
// whose celestrak feed is not classified, both with a status that holds the
// decoder error texts.
func classifiedFeedServer(t *testing.T) (run func(method, path, body string, o reqOpts) (int, []byte), rt *fakeRuntime) {
	t.Helper()
	srv, store, fake := newServer(t, "")
	for id, config := range map[string]string{"opensky": openskySecret, "celestrak": ""} {
		f := store.feeds[id]
		if config != "" {
			f.Config = json.RawMessage(config)
		}
		f.Status = &registry.Status{Owner: "me", State: registry.StateRunning, Health: registry.HealthHealthy, Detail: json.RawMessage(rejectedDetail)}
		store.feeds[id] = f
	}
	return func(method, path, body string, o reqOpts) (int, []byte) {
		resp, out := do(t, srv, method, path, body, o)
		return resp.StatusCode, out
	}, fake
}

// statusDetailOf finds the status detail in what a route answered: a status, a
// feed, or a list of feeds (where id picks the feed).
func statusDetailOf(t *testing.T, path string, body []byte, id string) map[string]any {
	t.Helper()
	var st struct {
		Detail map[string]any `json:"detail"`
	}
	switch {
	case strings.HasSuffix(path, "/status"):
		if err := json.Unmarshal(body, &st); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
	case path == "/feeds":
		var feeds []struct {
			ID     string `json:"id"`
			Status struct {
				Detail map[string]any `json:"detail"`
			} `json:"status"`
		}
		if err := json.Unmarshal(body, &feeds); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		for _, f := range feeds {
			if f.ID == id {
				return f.Status.Detail
			}
		}
		t.Fatalf("%s: no feed %s in %s", path, id, body)
	default:
		var f struct {
			Status struct {
				Detail map[string]any `json:"detail"`
			} `json:"status"`
		}
		if err := json.Unmarshal(body, &f); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		st.Detail = f.Status.Detail
	}
	return st.Detail
}

// The error texts a decoder produces can repeat what the sender sent, so a
// feed's status shows them only to a caller whose clearance reaches the feed's
// classification: the rule the dead-letter endpoint applies. Counts, times and
// reason codes stay for everyone. Every route that returns a status applies it.
func TestStatusErrorTextsAreShownOnlyToCallersClearedForTheFeed(t *testing.T) {
	run, _ := classifiedFeedServer(t)
	full := decodeObject(t, []byte(rejectedDetail))
	hidden := withoutTexts(t, full)

	routes := []struct{ name, method, path, body string }{
		{"list", "GET", "/feeds", ""},
		{"get", "GET", "/feeds/opensky", ""},
		{"status", "GET", "/feeds/opensky/status", ""},
		{"replace", "PUT", "/feeds/opensky", `{"name":"OpenSky Network","config":` + openskySecret + `,"enabled":true}`},
		{"patch", "PATCH", "/feeds/opensky", `{"description":"classified"}`},
		{"enable", "POST", "/feeds/opensky/enable", ""},
		{"disable", "POST", "/feeds/opensky/disable", ""},
	}
	callers := []struct {
		name  string
		class string
		sees  bool
	}{
		{"no clearance header", "", false},
		{"UNCLASSIFIED", "UNCLASSIFIED", false},
		{"CONFIDENTIAL", "CONFIDENTIAL", false},
		{"SECRET", "SECRET", true},
		{"TOP_SECRET", "TOP_SECRET", true},
		{"SECRET in lower case", "secret", true},
	}
	for _, route := range routes {
		for _, c := range callers {
			t.Run(route.name+"/"+c.name, func(t *testing.T) {
				// An operator, so that the routes that change a feed are open to the caller.
				status, body := run(route.method, route.path, route.body, reqOpts{roles: "sentinel-operator", user: "u1", class: c.class})
				if status != 200 {
					t.Fatalf("%s %s: %d %s", route.method, route.path, status, body)
				}
				got := statusDetailOf(t, route.path, body, "opensky")
				want := hidden
				if c.sees {
					want = full
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("detail for a caller with clearance %q:\n got %v\nwant %v", c.class, got, want)
				}
			})
		}
	}

	// A feed with no classification is open to every caller, whatever their clearance.
	for _, class := range []string{"", "UNCLASSIFIED"} {
		_, body := run("GET", "/feeds/celestrak", "", reqOpts{class: class})
		if got := statusDetailOf(t, "/feeds/celestrak", body, ""); !reflect.DeepEqual(got, full) {
			t.Errorf("an unclassified feed read with clearance %q lost part of its detail: %v", class, got)
		}
	}
}

// The status a replica holds in memory is what GET returns for a feed that runs
// there, so it is held to the same rule as the stored row.
func TestLiveStatusErrorTextsAreHeldToTheSameRule(t *testing.T) {
	run, rt := classifiedFeedServer(t)
	rt.snaps = map[string]registry.Status{"opensky": {Owner: "me", State: registry.StateRunning, Health: registry.HealthHealthy, Detail: json.RawMessage(rejectedDetail)}}
	full := decodeObject(t, []byte(rejectedDetail))

	for _, path := range []string{"/feeds", "/feeds/opensky", "/feeds/opensky/status"} {
		_, body := run("GET", path, "", reqOpts{class: "CONFIDENTIAL"})
		if got := statusDetailOf(t, path, body, "opensky"); !reflect.DeepEqual(got, withoutTexts(t, full)) {
			t.Errorf("%s for a CONFIDENTIAL caller: %v", path, got)
		}
		_, body = run("GET", path, "", reqOpts{class: "SECRET"})
		if got := statusDetailOf(t, path, body, "opensky"); !reflect.DeepEqual(got, full) {
			t.Errorf("%s for a SECRET caller: %v", path, got)
		}
	}
}

// Nothing is hidden but the two texts: the rest of the response is the same for
// a caller who may not see them.
func TestHidingErrorTextsChangesNothingElse(t *testing.T) {
	run, _ := classifiedFeedServer(t)
	_, cleared := run("GET", "/feeds/opensky", "", reqOpts{class: "SECRET"})
	_, below := run("GET", "/feeds/opensky", "", reqOpts{class: "CONFIDENTIAL"})
	want, got := decodeObject(t, cleared), decodeObject(t, below)
	want["status"].(map[string]any)["detail"] = withoutTexts(t, decodeObject(t, []byte(rejectedDetail)))
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the feed read below its classification differs by more than the texts:\n got %v\nwant %v", got, want)
	}
}

func TestWithoutErrorTexts(t *testing.T) {
	for _, tc := range []struct {
		name, detail, want string
	}{
		{"nothing to hide", `{"malformed":0}`, `{"malformed":0}`},
		{"an empty detail", `{}`, `{}`},
		{"the feed's text", `{"malformed":2,"lastDecodeError":"bad"}`, `{"malformed":2}`},
		{
			"each sender's text, and no other key of a sender",
			`{"senders":[{"remote":"a:1","malformed":1,"lastReason":"parse_error","lastError":"bad"},{"remote":"b:2","messages":3}]}`,
			`{"senders":[{"remote":"a:1","malformed":1,"lastReason":"parse_error"},{"remote":"b:2","messages":3}]}`,
		},
		{
			"a lastError elsewhere is not a decoder text",
			`{"lastError":"dial tcp: refused","connections":[{"name":"a","lastError":"Api Key Is Not Valid"}],"senders":[{"remote":"a:1","lastError":"bad"}]}`,
			`{"connections":[{"name":"a","lastError":"Api Key Is Not Valid"}],"lastError":"dial tcp: refused","senders":[{"remote":"a:1"}]}`,
		},
		{"an empty sender list", `{"senders":[]}`, `{"senders":[]}`},
		// What cannot be read cannot be shown to be free of the texts.
		{"senders that are not a list", `{"malformed":1,"senders":"10.0.0.5: bad"}`, `{"malformed":1}`},
		{"a sender that is not an object", `{"malformed":1,"senders":["10.0.0.5: bad"]}`, `{"malformed":1}`},
		{"a detail that is not an object", `["lastDecodeError"]`, `{}`},
		{"a null detail", `null`, `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := withoutErrorTexts(json.RawMessage(tc.detail))
			if !reflect.DeepEqual(decodeAny(t, got), decodeAny(t, json.RawMessage(tc.want))) {
				t.Errorf("withoutErrorTexts(%s) = %s, want %s", tc.detail, got, tc.want)
			}
		})
	}

}

// A sender's text is hidden by the key the transport writes it under. Here the
// detail is built from the transport's own type, so renaming the key cannot
// leave the text showing.
func TestWithoutErrorTextsHidesWhatTheTransportWrites(t *testing.T) {
	at := time.Date(2026, 10, 18, 12, 1, 0, 0, time.UTC)
	detail, err := json.Marshal(map[string]any{"senders": []transport.SenderStat{{
		Remote: "10.0.0.5:51234", Since: at, LastAt: at, Messages: 9, Connected: true,
		Malformed: 2, LastReason: "parse_error", LastError: "unknown field \"launchCode\"", LastMalformedAt: &at,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(detail), "launchCode") {
		t.Fatalf("the transport no longer writes the sender's error text: %s", detail)
	}
	got := string(withoutErrorTexts(detail))
	if strings.Contains(got, "launchCode") {
		t.Errorf("a sender's error text is still in the detail: %s", got)
	}
	for _, kept := range []string{"remote", "since", "lastAt", "messages", "connected", "malformed", "lastReason", "lastMalformedAt"} {
		if !strings.Contains(got, `"`+kept+`"`) {
			t.Errorf("%s was lost with the text: %s", kept, got)
		}
	}
}

// Counts are not rounded on their way through: decoding them into float64 would.
func TestWithoutErrorTextsKeepsNumbersExact(t *testing.T) {
	got := string(withoutErrorTexts(json.RawMessage(`{"messages":12345678901234567890,"senders":[{"messages":9007199254740993,"lastError":"x"}]}`)))
	for _, digits := range []string{"12345678901234567890", "9007199254740993"} {
		if !strings.Contains(got, digits) {
			t.Errorf("%s lost its digits: %s", digits, got)
		}
	}
}

// The detail a status holds is shared with whoever reported it, so hiding the
// texts leaves it as it was.
func TestWithoutErrorTextsLeavesItsArgumentAlone(t *testing.T) {
	detail := json.RawMessage(rejectedDetail)
	before := string(detail)
	_ = withoutErrorTexts(detail)
	if string(detail) != before {
		t.Error("withoutErrorTexts changed the detail it was given")
	}
}

func decodeAny(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return v
}

func TestClearedFor(t *testing.T) {
	// clearedBy asks clearedFor the way a request does: the clearance arrives
	// in the gateway's header and AuthFromHeaders puts it in the context.
	clearedBy := func(caller string, f registry.Feed) bool {
		var got bool
		h := middleware.AuthFromHeaders(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = clearedFor(r, f) }))
		req := httptest.NewRequest("GET", "/", nil)
		if caller != "" {
			req.Header.Set(middleware.HeaderClassification, caller)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		return got
	}
	feed := func(class string) registry.Feed {
		cfg := `{}`
		if class != "" {
			cfg = `{"classification":"` + class + `"}`
		}
		return registry.Feed{Config: json.RawMessage(cfg)}
	}
	for _, tc := range []struct {
		feedClass, caller string
		want              bool
	}{
		{"", "", true},
		{"", "UNCLASSIFIED", true},
		{"UNCLASSIFIED", "", true},
		{"CONFIDENTIAL", "", false},
		{"CONFIDENTIAL", "UNCLASSIFIED", false},
		{"CONFIDENTIAL", "CONFIDENTIAL", true},
		{"SECRET", "CONFIDENTIAL", false},
		{"SECRET", "SECRET", true},
		{"SECRET", "TOP_SECRET", true},
		{"TOP_SECRET", "SECRET", false},
		{"TOP_SECRET", "TOP_SECRET", true},
		// An unrecognised level ranks as UNCLASSIFIED, so it can never be what lets a caller in.
		{"SECRET", "BOGUS", false},
		{"SECRET", " secret ", true},
	} {
		if got := clearedBy(tc.caller, feed(tc.feedClass)); got != tc.want {
			t.Errorf("caller %q, feed %q: %v, want %v", tc.caller, tc.feedClass, got, tc.want)
		}
	}
	// A config that cannot be read has no classification to be above.
	if !clearedBy("", registry.Feed{Config: json.RawMessage(`not json`)}) {
		t.Error("a feed whose config cannot be read should rank as UNCLASSIFIED")
	}
}
