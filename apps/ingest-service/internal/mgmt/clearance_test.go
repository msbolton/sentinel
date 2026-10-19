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

// decodeError is the text of the last decode error in rejectedDetail.
const decodeError = `proto: (line 1:2): unknown field "launchCode"`

// connectionError is the kind of last error that has nothing to do with a decoder.
const connectionError = "dial tcp 10.0.0.9:4001: connect: connection refused"

// classifiedFeedServer is a server whose opensky feed is classified SECRET and
// whose celestrak feed is not classified, both with a status that holds the
// decoder error texts and lastError as its last error.
func classifiedFeedServer(t *testing.T, lastError string) (run func(method, path, body string, o reqOpts) (int, []byte), rt *fakeRuntime) {
	t.Helper()
	srv, store, fake := newServer(t, "")
	for id, config := range map[string]string{"opensky": openskySecret, "celestrak": ""} {
		f := store.feeds[id]
		if config != "" {
			f.Config = json.RawMessage(config)
		}
		f.Status = &registry.Status{Owner: "me", State: registry.StateRunning, Health: registry.HealthHealthy, LastError: lastError, Detail: json.RawMessage(rejectedDetail)}
		store.feeds[id] = f
	}
	return func(method, path, body string, o reqOpts) (int, []byte) {
		resp, out := do(t, srv, method, path, body, o)
		return resp.StatusCode, out
	}, fake
}

// statusOf finds the status in what a route answered: a status, a feed, or a
// list of feeds (where id picks the feed). It is the status as a client reads
// it, so a field that was left out is missing from the map.
func statusOf(t *testing.T, path string, body []byte, id string) map[string]any {
	t.Helper()
	switch {
	case strings.HasSuffix(path, "/status"):
		return decodeObject(t, body)
	case path == "/feeds":
		var feeds []map[string]any
		if err := json.Unmarshal(body, &feeds); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		for _, f := range feeds {
			if f["id"] == id {
				return f["status"].(map[string]any)
			}
		}
		t.Fatalf("%s: no feed %s in %s", path, id, body)
	}
	return decodeObject(t, body)["status"].(map[string]any)
}

// statusRoutes are the routes that return a feed's status, in the feed or alone.
var statusRoutes = []struct{ name, method, path, body string }{
	{"list", "GET", "/feeds", ""},
	{"get", "GET", "/feeds/opensky", ""},
	{"status", "GET", "/feeds/opensky/status", ""},
	{"replace", "PUT", "/feeds/opensky", `{"name":"OpenSky Network","config":` + openskySecret + `,"enabled":true}`},
	{"patch", "PATCH", "/feeds/opensky", `{"description":"classified"}`},
	{"enable", "POST", "/feeds/opensky/enable", ""},
	{"disable", "POST", "/feeds/opensky/disable", ""},
}

// statusCallers are callers of the SECRET opensky feed; sees says whether the
// clearance reaches it.
var statusCallers = []struct {
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

// eachStatus asks every route that returns the opensky feed's status for it as
// every caller, as an operator so that the routes that change a feed are open
// to them, and hands what the route answered to check.
func eachStatus(t *testing.T, run func(method, path, body string, o reqOpts) (int, []byte), check func(t *testing.T, sees bool, class string, st map[string]any)) {
	t.Helper()
	for _, route := range statusRoutes {
		for _, c := range statusCallers {
			t.Run(route.name+"/"+c.name, func(t *testing.T) {
				status, body := run(route.method, route.path, route.body, reqOpts{roles: "sentinel-operator", user: "u1", class: c.class})
				if status != 200 {
					t.Fatalf("%s %s: %d %s", route.method, route.path, status, body)
				}
				check(t, c.sees, c.class, statusOf(t, route.path, body, "opensky"))
			})
		}
	}
}

// checkDetail compares the detail of a status with the detail a caller may see.
func checkDetail(t *testing.T, sees bool, class string, st map[string]any) {
	t.Helper()
	full := decodeObject(t, []byte(rejectedDetail))
	want := withoutTexts(t, full)
	if sees {
		want = full
	}
	if got, _ := st["detail"].(map[string]any); !reflect.DeepEqual(got, want) {
		t.Errorf("detail for a caller with clearance %q:\n got %v\nwant %v", class, got, want)
	}
}

// The error texts a decoder produces can repeat what the sender sent, so a
// feed's status shows them only to a caller whose clearance reaches the feed's
// classification: the rule the dead-letter endpoint applies. Counts, times and
// reason codes stay for everyone. Every route that returns a status applies it.
func TestStatusErrorTextsAreShownOnlyToCallersClearedForTheFeed(t *testing.T) {
	if got := decodeObject(t, []byte(rejectedDetail))["lastDecodeError"]; got != decodeError {
		t.Fatalf("the fixture's decode error is %q, not %q", got, decodeError)
	}
	run, _ := classifiedFeedServer(t, "")
	eachStatus(t, run, checkDetail)

	// A feed with no classification is open to every caller, whatever their clearance.
	full := decodeObject(t, []byte(rejectedDetail))
	for _, class := range []string{"", "UNCLASSIFIED"} {
		_, body := run("GET", "/feeds/celestrak", "", reqOpts{class: class})
		if got, _ := statusOf(t, "/feeds/celestrak", body, "")["detail"].(map[string]any); !reflect.DeepEqual(got, full) {
			t.Errorf("an unclassified feed read with clearance %q lost part of its detail: %v", class, got)
		}
	}
}

// When a decode error is also the status's last error, which is where the
// operator's list shows it, it is the same text and is hidden with the rest:
// blank to a caller who may not see the feed's decoder texts, as it is
// everywhere the status is returned.
func TestLastErrorThatIsADecodeErrorIsHiddenWithTheOtherDecoderTexts(t *testing.T) {
	run, _ := classifiedFeedServer(t, decodeError)
	eachStatus(t, run, func(t *testing.T, sees bool, class string, st map[string]any) {
		checkDetail(t, sees, class, st)
		last, present := st["lastError"]
		switch {
		case sees && last != decodeError:
			t.Errorf("a caller with clearance %q sees last error %v, want the decode error", class, last)
		case !sees && present:
			t.Errorf("a caller with clearance %q sees last error %v: the decoder's text, which can repeat the payload", class, last)
		}
		// The rest of the status is the caller's whatever their clearance.
		if st["state"] != registry.StateRunning && st["state"] != registry.StateStopped {
			t.Errorf("state = %v", st["state"])
		}
	})

	// A feed that is not classified shows its last error to every caller.
	for _, class := range []string{"", "UNCLASSIFIED"} {
		_, body := run("GET", "/feeds/celestrak/status", "", reqOpts{class: class})
		if got := statusOf(t, "/feeds/celestrak/status", body, "")["lastError"]; got != decodeError {
			t.Errorf("an unclassified feed read with clearance %q: last error %v, want %q", class, got, decodeError)
		}
	}
}

// A last error that is not a decoder's, such as a dial failure, says nothing of
// what a sender sent and stays visible to every caller, which is how an
// operator finds out why a connection is down. The decoder texts beside it are
// still hidden.
func TestLastErrorThatIsAConnectionErrorStaysVisible(t *testing.T) {
	run, _ := classifiedFeedServer(t, connectionError)
	eachStatus(t, run, func(t *testing.T, sees bool, class string, st map[string]any) {
		checkDetail(t, sees, class, st)
		if got := st["lastError"]; got != connectionError {
			t.Errorf("a caller with clearance %q sees last error %v, want the connection error", class, got)
		}
	})
}

// Two drivers keep an account of their own failures in the status detail, and
// after a parse failure the decode text is in it: celestrak under errors, one
// entry for each selection, and httpjson under lastError. A caller who may not
// see the feed's decoder texts gets the same fields with the texts emptied, so
// a client finds the shape it expects.
func TestDriverSpecificErrorTextsAreGatedLikeTheRest(t *testing.T) {
	const celestrakFull = `{"satellites":3,"pinned":0,"emitted":12,"polls":2,"selections":2,"lastStatus":403,` +
		`"errors":{"group:active":"omm json: invalid character '<' looking for beginning of value","group:stations":"remote returned 403: one download per update"}}`
	const celestrakBlank = `{"satellites":3,"pinned":0,"emitted":12,"polls":2,"selections":2,"lastStatus":403,` +
		`"errors":{"group:active":"","group:stations":""}}`
	const httpjsonFull = `{"host":"api.example.org","method":"GET","requireArea":false,"polls":4,` +
		`"lastError":"json: cannot unmarshal object into Go value of type []interface {}"}`
	const httpjsonBlank = `{"host":"api.example.org","method":"GET","requireArea":false,"polls":4,"lastError":""}`

	srv, store, _ := newServer(t, "")
	running := func(detail string) *registry.Status {
		return &registry.Status{Owner: "me", State: registry.StateRunning, Health: registry.HealthWarn, Detail: json.RawMessage(detail)}
	}
	cel := store.feeds["celestrak"]
	cel.Config = json.RawMessage(`{"groups":["active"],"classification":"SECRET"}`)
	cel.Status = running(celestrakFull)
	store.feeds["celestrak"] = cel
	store.feeds["ops-json"] = registry.Feed{
		ID: "ops-json", Name: "Ops JSON", Driver: "httpjson", Enabled: true, SecretRefs: map[string]string{},
		Config: json.RawMessage(`{"url":"https://api.example.org/v1/tracks","classification":"SECRET"}`),
		Status: running(httpjsonFull),
	}

	for _, feed := range []struct{ id, full, blank string }{
		{"celestrak", celestrakFull, celestrakBlank},
		{"ops-json", httpjsonFull, httpjsonBlank},
	} {
		for _, path := range []string{"/feeds", "/feeds/" + feed.id, "/feeds/" + feed.id + "/status"} {
			for _, c := range statusCallers {
				t.Run(feed.id+path+"/"+c.name, func(t *testing.T) {
					resp, body := do(t, srv, "GET", path, "", reqOpts{roles: "sentinel-operator", class: c.class})
					if resp.StatusCode != 200 {
						t.Fatalf("GET %s: %d %s", path, resp.StatusCode, body)
					}
					want := feed.blank
					if c.sees {
						want = feed.full
					}
					got, _ := statusOf(t, path, body, feed.id)["detail"].(map[string]any)
					if !reflect.DeepEqual(got, decodeObject(t, []byte(want))) {
						t.Errorf("detail for a caller with clearance %q:\n got %v\nwant %s", c.class, got, want)
					}
				})
			}
		}
	}
}

// The status a replica holds in memory is what GET returns for a feed that runs
// there, so it is held to the same rule as the stored row.
func TestLiveStatusErrorTextsAreHeldToTheSameRule(t *testing.T) {
	run, rt := classifiedFeedServer(t, "")
	rt.snaps = map[string]registry.Status{"opensky": {Owner: "me", State: registry.StateRunning, Health: registry.HealthHealthy, LastError: decodeError, Detail: json.RawMessage(rejectedDetail)}}
	full := decodeObject(t, []byte(rejectedDetail))

	for _, path := range []string{"/feeds", "/feeds/opensky", "/feeds/opensky/status"} {
		_, body := run("GET", path, "", reqOpts{class: "CONFIDENTIAL"})
		st := statusOf(t, path, body, "opensky")
		if got, _ := st["detail"].(map[string]any); !reflect.DeepEqual(got, withoutTexts(t, full)) {
			t.Errorf("%s for a CONFIDENTIAL caller: %v", path, got)
		}
		if last, present := st["lastError"]; present {
			t.Errorf("%s for a CONFIDENTIAL caller: last error %v", path, last)
		}
		_, body = run("GET", path, "", reqOpts{class: "SECRET"})
		st = statusOf(t, path, body, "opensky")
		if got, _ := st["detail"].(map[string]any); !reflect.DeepEqual(got, full) {
			t.Errorf("%s for a SECRET caller: %v", path, got)
		}
		if st["lastError"] != decodeError {
			t.Errorf("%s for a SECRET caller: last error %v", path, st["lastError"])
		}
	}
}

// Nothing is hidden but the decoder texts: the rest of the response is the same
// for a caller who may not see them.
func TestHidingErrorTextsChangesNothingElse(t *testing.T) {
	run, _ := classifiedFeedServer(t, decodeError)
	_, cleared := run("GET", "/feeds/opensky", "", reqOpts{class: "SECRET"})
	_, below := run("GET", "/feeds/opensky", "", reqOpts{class: "CONFIDENTIAL"})
	want, got := decodeObject(t, cleared), decodeObject(t, below)
	status := want["status"].(map[string]any)
	status["detail"] = withoutTexts(t, decodeObject(t, []byte(rejectedDetail)))
	delete(status, "lastError")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the feed read below its classification differs by more than the texts:\n got %v\nwant %v", got, want)
	}
}

func TestWithoutDecoderTexts(t *testing.T) {
	status := func(lastError, detail string) registry.Status {
		return registry.Status{
			Owner: "me", State: registry.StateRunning, Health: registry.HealthWarn, LastError: lastError,
			MessagesTotal: 12, ErrorsTotal: 3, Detail: json.RawMessage(detail),
		}
	}
	for _, tc := range []struct {
		name          string
		lastError     string
		detail        string
		wantLastError string
	}{
		{"the last error is the decode error", "bad json", `{"lastDecodeError":"bad json"}`, ""},
		{"the last error is something else", "connection refused", `{"lastDecodeError":"bad json"}`, "connection refused"},
		{"no decode error to be", "bad json", `{"malformed":2}`, "bad json"},
		{"nothing to hide", "", `{"lastDecodeError":"bad json"}`, ""},
		{"both empty", "", `{"lastDecodeError":""}`, ""},
		{"the decode error is not text", "7", `{"lastDecodeError":7}`, "7"},
		// The texts are the same only if they are the same text.
		{"a different case", "Bad JSON", `{"lastDecodeError":"bad json"}`, "Bad JSON"},
		{"the selection that prefixed it", "group:active: omm json: bad", `{"lastDecodeError":"omm json: bad"}`, "group:active: omm json: bad"},
		{"the decode error of a sender is not the feed's", "bad json", `{"senders":[{"remote":"a:1","lastError":"bad json"}]}`, "bad json"},
		// A detail that cannot be read has no decode error to compare with.
		{"a detail that is not an object", "bad json", `["bad json"]`, "bad json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := status(tc.lastError, tc.detail)
			got := withoutDecoderTexts(in)
			if got.LastError != tc.wantLastError {
				t.Errorf("last error = %q, want %q", got.LastError, tc.wantLastError)
			}
			// Everything else of the status is as it was, and the detail is cut down
			// as before.
			want := in
			want.LastError = tc.wantLastError
			want.Detail = withoutErrorTexts(in.Detail)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("status = %+v, want %+v", got, want)
			}
		})
	}

	// The status the caller passes in is not changed.
	in := status("bad json", `{"lastDecodeError":"bad json"}`)
	_ = withoutDecoderTexts(in)
	if in.LastError != "bad json" || string(in.Detail) != `{"lastDecodeError":"bad json"}` {
		t.Errorf("withoutDecoderTexts changed the status it was given: %+v", in)
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
			// aisstream's connections report their own dial and subscription errors,
			// which say nothing of a payload.
			"a lastError nested elsewhere is a connection's own",
			`{"connections":{"area":{"connected":false,"lastError":"Api Key Is Not Valid"}},"senders":[{"remote":"a:1","lastError":"bad"}]}`,
			`{"connections":{"area":{"connected":false,"lastError":"Api Key Is Not Valid"}},"senders":[{"remote":"a:1"}]}`,
		},
		// The driver-specific keys that can hold a parse failure's text keep their
		// shape and lose their strings.
		{"httpjson's last error is blanked, its key kept", `{"polls":4,"lastError":"json: cannot unmarshal object"}`, `{"polls":4,"lastError":""}`},
		{"a last error that is empty already", `{"lastError":""}`, `{"lastError":""}`},
		{"a last error that is null", `{"lastError":null}`, `{"lastError":null}`},
		{
			"celestrak's errors keep their selections and lose their texts",
			`{"lastStatus":403,"errors":{"group:active":"omm json: invalid character '<'","group:stations":"remote returned 403: one download per update"}}`,
			`{"lastStatus":403,"errors":{"group:active":"","group:stations":""}}`,
		},
		{"errors that are a count stay", `{"errors":7}`, `{"errors":7}`},
		{"errors that are none", `{"errors":{}}`, `{"errors":{}}`},
		{"errors nested deeper keep what is not text", `{"errors":{"a":{"msg":"text","n":2,"ok":true,"none":null},"b":["x",3]}}`, `{"errors":{"a":{"msg":"","n":2,"ok":true,"none":null},"b":["",3]}}`},
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
	got := string(withoutErrorTexts(json.RawMessage(`{"messages":12345678901234567890,"senders":[{"messages":9007199254740993,"lastError":"x"}],"errors":{"a":"x","n":18446744073709551617}}`)))
	for _, digits := range []string{"12345678901234567890", "9007199254740993", "18446744073709551617"} {
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
