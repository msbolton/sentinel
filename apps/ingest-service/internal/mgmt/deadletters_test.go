package mgmt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/dlq"
	"github.com/sentinel/ingest-service/internal/drivers"
)

// fakeDeadLetters answers Recent with what it was given and records the calls.
type fakeDeadLetters struct {
	mu      sync.Mutex
	letters []dlq.Letter
	scanned int
	partial bool
	err     error
	calls   []deadLetterCall
}

type deadLetterCall struct {
	feedID string
	limit  int
}

func (f *fakeDeadLetters) Recent(_ context.Context, feedID string, limit int) ([]dlq.Letter, int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, deadLetterCall{feedID, limit})
	return f.letters, f.scanned, f.partial, f.err
}

func (f *fakeDeadLetters) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// sampleLetter is the letter of the contract's example.
func sampleLetter() dlq.Letter {
	return dlq.Letter{
		At: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC), Reason: "parse_error",
		Error:  "not a sentinel.v1.Observation in JSON: …",
		Driver: "tcp", Decoder: "sentinel", Sender: "10.0.0.5:51234",
		Size: 17, Payload: []byte("not json"), Partition: 0, Offset: 42,
	}
}

// deadLetterServer is a server whose dead letters come from reader. opensky is
// classified SECRET; celestrak is not classified.
func deadLetterServer(t *testing.T, token string, reader dlq.Reader) (*httptest.Server, *memStore) {
	t.Helper()
	store := newMemStore()
	f := store.feeds["opensky"]
	f.Config = json.RawMessage(openskySecret)
	store.feeds["opensky"] = f
	h := NewHandler(store, drivers.Default(), &fakeRuntime{}, nil)
	h.DeadLetters = reader
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, Auth(AuthOptions{ServiceToken: token}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, store
}

func TestDeadLettersRequireOperatorAndClearance(t *testing.T) {
	reader := &fakeDeadLetters{letters: []dlq.Letter{sampleLetter()}, scanned: 1}
	srv, _ := deadLetterServer(t, "", reader)

	for _, tc := range []struct {
		name string
		feed string
		opts reqOpts
		want int
	}{
		{"no identity at all", "opensky", reqOpts{}, 403},
		{"viewer, fully cleared", "opensky", reqOpts{roles: "sentinel-viewer", class: "TOP_SECRET"}, 403},
		{"analyst, fully cleared", "opensky", reqOpts{roles: "sentinel-analyst", class: "TOP_SECRET"}, 403},
		{"operator with no clearance", "opensky", reqOpts{roles: "sentinel-operator"}, 403},
		{"operator below the classification", "opensky", reqOpts{roles: "sentinel-operator", class: "CONFIDENTIAL"}, 403},
		{"admin below the classification", "opensky", reqOpts{roles: "sentinel-admin", class: "UNCLASSIFIED"}, 403},
		{"operator at the classification", "opensky", reqOpts{roles: "sentinel-operator", class: "SECRET"}, 200},
		{"operator above the classification", "opensky", reqOpts{roles: "sentinel-operator", class: "TOP_SECRET"}, 200},
		{"admin at the classification", "opensky", reqOpts{roles: "sentinel-admin", class: "SECRET"}, 200},
		{"operator among other roles", "opensky", reqOpts{roles: "sentinel-viewer, sentinel-operator", class: "SECRET"}, 200},
		{"operator with a lower-case clearance", "opensky", reqOpts{roles: "sentinel-operator", class: "secret"}, 200},
		// An unclassified feed asks nothing more of an operator, who may have no clearance on record.
		{"operator on an unclassified feed", "celestrak", reqOpts{roles: "sentinel-operator"}, 200},
		{"viewer on an unclassified feed", "celestrak", reqOpts{roles: "sentinel-viewer", class: "TOP_SECRET"}, 403},
	} {
		before := reader.callCount()
		resp, body := do(t, srv, "GET", "/feeds/"+tc.feed+"/deadletters", "", tc.opts)
		if resp.StatusCode != tc.want {
			t.Errorf("%s: status %d, want %d: %s", tc.name, resp.StatusCode, tc.want, body)
		}
		if tc.want == 403 {
			if reader.callCount() != before {
				t.Errorf("%s: the topic was read for a caller who may not see it", tc.name)
			}
			if !strings.Contains(string(body), `"error"`) || strings.Contains(string(body), "not json") {
				t.Errorf("%s: body = %s", tc.name, body)
			}
		}
	}
}

// Review focus 5: a classified feed, and a caller with the operator role whose
// clearance is below it. Nothing is returned and nothing is read.
func TestDeadLettersRefuseLowerClearance(t *testing.T) {
	reader := &fakeDeadLetters{letters: []dlq.Letter{sampleLetter()}, scanned: 1}
	srv, _ := deadLetterServer(t, "", reader)

	resp, body := do(t, srv, "GET", "/feeds/opensky/deadletters?payload=true", "", reqOpts{roles: "sentinel-operator", user: "u1", class: "CONFIDENTIAL"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", resp.StatusCode, body)
	}
	if n := reader.callCount(); n != 0 {
		t.Errorf("Recent was called %d times: the topic was read for a caller who may not see the feed", n)
	}
	for _, leaked := range []string{"not json", "10.0.0.5", "parse_error", `"letters"`} {
		if strings.Contains(string(body), leaked) {
			t.Errorf("the refusal carries %q: %s", leaked, body)
		}
	}
}

func TestDeadLettersHidePayloadUnlessAsked(t *testing.T) {
	reader := &fakeDeadLetters{letters: []dlq.Letter{sampleLetter()}, scanned: 1}
	srv, _ := deadLetterServer(t, "", reader)
	op := reqOpts{roles: "sentinel-operator", class: "SECRET"}

	for _, path := range []string{"/feeds/opensky/deadletters", "/feeds/opensky/deadletters?payload=false", "/feeds/opensky/deadletters?payload=1", "/feeds/opensky/deadletters?payload="} {
		resp, body := do(t, srv, "GET", path, "", op)
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d %s", path, resp.StatusCode, body)
		}
		var out struct {
			Letters []map[string]any `json:"letters"`
		}
		if err := json.Unmarshal(body, &out); err != nil || len(out.Letters) != 1 {
			t.Fatalf("%s: %s (%v)", path, body, err)
		}
		for _, field := range []string{"payload", "payloadEncoding"} {
			if _, present := out.Letters[0][field]; present {
				t.Errorf("%s: %s is in the response without payload=true", path, field)
			}
		}
		if strings.Contains(string(body), "not json") {
			t.Errorf("%s: the payload is in the response: %s", path, body)
		}
		// Everything else about the letter is there.
		for _, field := range []string{"at", "reason", "error", "driver", "decoder", "sender", "size", "payloadTruncated", "partition", "offset"} {
			if _, present := out.Letters[0][field]; !present {
				t.Errorf("%s: %s is missing", path, field)
			}
		}
	}

	_, body := do(t, srv, "GET", "/feeds/opensky/deadletters?payload=true", "", op)
	var out struct {
		Letters []struct {
			Payload         *string `json:"payload"`
			PayloadEncoding string  `json:"payloadEncoding"`
		} `json:"letters"`
	}
	if err := json.Unmarshal(body, &out); err != nil || len(out.Letters) != 1 || out.Letters[0].Payload == nil || *out.Letters[0].Payload != "not json" || out.Letters[0].PayloadEncoding != "utf8" {
		t.Errorf("payload=true: %s (%v)", body, err)
	}
}

func TestDeadLettersUnknownFeed404(t *testing.T) {
	reader := &fakeDeadLetters{}
	srv, _ := deadLetterServer(t, "", reader)
	resp, body := do(t, srv, "GET", "/feeds/no-such-feed/deadletters", "", reqOpts{roles: "sentinel-operator", class: "TOP_SECRET"})
	if resp.StatusCode != 404 || !strings.Contains(string(body), "feed not found") {
		t.Errorf("status %d, want 404 with the feed-not-found error: %s", resp.StatusCode, body)
	}
	if reader.callCount() != 0 {
		t.Error("the topic was read for a feed that does not exist")
	}
}

func TestDeadLettersDisabled501(t *testing.T) {
	srv, _ := deadLetterServer(t, "", nil)
	resp, body := do(t, srv, "GET", "/feeds/opensky/deadletters", "", reqOpts{roles: "sentinel-operator", class: "SECRET"})
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status %d, want 501: %s", resp.StatusCode, body)
	}
	// Who may ask is settled before whether there is anything to ask.
	if resp, _ := do(t, srv, "GET", "/feeds/opensky/deadletters", "", reqOpts{roles: "sentinel-viewer", class: "SECRET"}); resp.StatusCode != 403 {
		t.Errorf("a viewer got %d, want 403", resp.StatusCode)
	}
}

func TestDeadLettersKafkaFailure502(t *testing.T) {
	reader := &fakeDeadLetters{err: errors.New("dial tcp 10.1.2.3:9092: connect: connection refused")}
	srv, _ := deadLetterServer(t, "", reader)
	resp, body := do(t, srv, "GET", "/feeds/opensky/deadletters", "", reqOpts{roles: "sentinel-operator", class: "SECRET"})
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d, want 502: %s", resp.StatusCode, body)
	}
	for _, internal := range []string{"10.1.2.3", "9092", "connection refused", "dial"} {
		if strings.Contains(string(body), internal) {
			t.Errorf("the error body carries %q: %s", internal, body)
		}
	}
	var out errorResponse
	if err := json.Unmarshal(body, &out); err != nil || out.Error == "" {
		t.Errorf("the error body is not the service's error shape: %s", body)
	}
}

func TestDeadLettersLimit(t *testing.T) {
	reader := &fakeDeadLetters{}
	srv, _ := deadLetterServer(t, "", reader)
	op := reqOpts{roles: "sentinel-operator", class: "SECRET"}

	for _, tc := range []struct {
		query string
		want  int
	}{
		{"", 20},
		{"?limit=", 20},
		{"?limit=1", 1},
		{"?limit=50", 50},
		{"?limit=100", 100},
		{"?limit=101", 100},
		{"?limit=100000", 100},
	} {
		reader.calls = nil
		resp, body := do(t, srv, "GET", "/feeds/opensky/deadletters"+tc.query, "", op)
		if resp.StatusCode != 200 || len(reader.calls) != 1 || reader.calls[0].limit != tc.want || reader.calls[0].feedID != "opensky" {
			t.Errorf("%q: status %d, calls %v, want one read of opensky with limit %d (%s)", tc.query, resp.StatusCode, reader.calls, tc.want, body)
		}
	}

	for _, bad := range []string{"0", "-1", "abc", "1.5", "10x", "99999999999999999999", " 5"} {
		reader.calls = nil
		resp, body := do(t, srv, "GET", "/feeds/opensky/deadletters?limit="+strings.ReplaceAll(bad, " ", "%20"), "", op)
		if resp.StatusCode != 400 {
			t.Errorf("limit=%q: status %d, want 400: %s", bad, resp.StatusCode, body)
		}
		if len(reader.calls) != 0 {
			t.Errorf("limit=%q: the topic was read", bad)
		}
	}
}

// The response is a contract the client is built against: field names, order
// and which fields are present are compared as text.
func TestDeadLettersResponseShape(t *testing.T) {
	reader := &fakeDeadLetters{letters: []dlq.Letter{sampleLetter()}, scanned: 120}
	srv, _ := deadLetterServer(t, "", reader)
	op := reqOpts{roles: "sentinel-operator", class: "SECRET"}

	const withPayload = `{"letters":[{"at":"2026-10-18T12:00:00Z","reason":"parse_error","error":"not a sentinel.v1.Observation in JSON: …","driver":"tcp","decoder":"sentinel","sender":"10.0.0.5:51234","size":17,"payload":"not json","payloadEncoding":"utf8","payloadTruncated":false,"partition":0,"offset":42}],"scanned":120,"partial":false}`
	const withoutPayload = `{"letters":[{"at":"2026-10-18T12:00:00Z","reason":"parse_error","error":"not a sentinel.v1.Observation in JSON: …","driver":"tcp","decoder":"sentinel","sender":"10.0.0.5:51234","size":17,"payloadTruncated":false,"partition":0,"offset":42}],"scanned":120,"partial":false}`

	resp, body := do(t, srv, "GET", "/feeds/opensky/deadletters?payload=true", "", op)
	if got := strings.TrimSpace(string(body)); resp.StatusCode != 200 || got != withPayload || resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("with payload=true: %d %s:\n got %s\nwant %s", resp.StatusCode, resp.Header.Get("Content-Type"), got, withPayload)
	}
	_, body = do(t, srv, "GET", "/feeds/opensky/deadletters", "", op)
	if got := strings.TrimSpace(string(body)); got != withoutPayload {
		t.Errorf("without payload:\n got %s\nwant %s", got, withoutPayload)
	}

	// No letters is an empty list, not null, and partial is passed on.
	reader.letters, reader.scanned, reader.partial = nil, 0, true
	_, body = do(t, srv, "GET", "/feeds/opensky/deadletters", "", op)
	if got, want := strings.TrimSpace(string(body)), `{"letters":[],"scanned":0,"partial":true}`; got != want {
		t.Errorf("no letters:\n got %s\nwant %s", got, want)
	}

	// A letter from before the publisher recorded senders has an empty sender,
	// and its fields are all still there.
	old := sampleLetter()
	old.Sender, old.Decoder, old.Driver = "", "", ""
	reader.letters, reader.scanned, reader.partial = []dlq.Letter{old}, 1, false
	_, body = do(t, srv, "GET", "/feeds/opensky/deadletters", "", op)
	if !strings.Contains(string(body), `"driver":"","decoder":"","sender":""`) {
		t.Errorf("empty texts are left out of the response: %s", body)
	}
}

func TestDeadLettersPayloadEncoding(t *testing.T) {
	binary := []byte{0x00, 0xff, 0xfe, 'a', 0x80}
	long := []byte(strings.Repeat("é", 512))
	for _, tc := range []struct {
		name         string
		payload      []byte
		truncated    bool
		wantPayload  string
		wantEncoding string
	}{
		{"text", []byte("not json"), false, "not json", "utf8"},
		{"text with quotes and control characters", []byte("a\"b\n\x01é"), false, "a\"b\n\x01é", "utf8"},
		{"empty", nil, false, "", "utf8"},
		{"binary", binary, false, base64.StdEncoding.EncodeToString(binary), "base64"},
		{"text that was cut", long, true, string(long), "utf8"},
		{"binary that was cut", binary, true, base64.StdEncoding.EncodeToString(binary), "base64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := sampleLetter()
			l.Payload, l.PayloadTruncated, l.Size = tc.payload, tc.truncated, len(tc.payload)+100
			reader := &fakeDeadLetters{letters: []dlq.Letter{l}, scanned: 1}
			srv, _ := deadLetterServer(t, "", reader)
			_, body := do(t, srv, "GET", "/feeds/opensky/deadletters?payload=true", "", reqOpts{roles: "sentinel-operator", class: "SECRET"})
			var out struct {
				Letters []struct {
					Size             int     `json:"size"`
					Payload          *string `json:"payload"`
					PayloadEncoding  string  `json:"payloadEncoding"`
					PayloadTruncated bool    `json:"payloadTruncated"`
				} `json:"letters"`
			}
			if err := json.Unmarshal(body, &out); err != nil || len(out.Letters) != 1 {
				t.Fatalf("%s (%v)", body, err)
			}
			got := out.Letters[0]
			if got.Payload == nil || *got.Payload != tc.wantPayload || got.PayloadEncoding != tc.wantEncoding || got.PayloadTruncated != tc.truncated || got.Size != len(tc.payload)+100 {
				t.Errorf("payload %v, encoding %q, truncated %v, size %d; want %q, %q, %v, %d", got.Payload, got.PayloadEncoding, got.PayloadTruncated, got.Size, tc.wantPayload, tc.wantEncoding, tc.truncated, len(tc.payload)+100)
			}
			if tc.wantEncoding == "base64" {
				if _, err := base64.StdEncoding.DecodeString(*got.Payload); err != nil {
					t.Errorf("the payload is not base64: %v", err)
				}
			}
		})
	}
}

func TestDeadLettersAreGuardedLikeTheRestOfTheAPI(t *testing.T) {
	reader := &fakeDeadLetters{letters: []dlq.Letter{sampleLetter()}}
	srv, _ := deadLetterServer(t, "secret", reader)
	op := reqOpts{roles: "sentinel-operator", class: "SECRET"}
	if resp, _ := do(t, srv, "GET", "/feeds/opensky/deadletters", "", op); resp.StatusCode != 401 {
		t.Errorf("without the service token: %d, want 401", resp.StatusCode)
	}
	op.token = "secret"
	if resp, body := do(t, srv, "GET", "/feeds/opensky/deadletters", "", op); resp.StatusCode != 200 {
		t.Errorf("with the service token: %d %s", resp.StatusCode, body)
	}
	// Only GET is a route: nothing here can be posted, replaced or deleted.
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		if resp, _ := do(t, srv, method, "/feeds/opensky/deadletters", `{}`, op); resp.StatusCode != 404 && resp.StatusCode != 405 {
			t.Errorf("%s: %d, want 404 or 405", method, resp.StatusCode)
		}
	}
}
