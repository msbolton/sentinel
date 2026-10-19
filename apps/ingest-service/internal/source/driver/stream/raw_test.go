package stream

import (
	"context"
	"encoding/json"
	"testing"

	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/transport"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

// An observation that came from a whole frame carries the frame as its raw
// payload; one a decoder cut out of a larger frame (an element of a JSON
// array) carries its own part. The whole line on each of up to 100
// observations would be 100 copies under the full raw policy and, under the
// bounded one, the first bytes of the line rather than the observation's own.
func TestRawPayloadIsTheFrameOrThePartTheDecoderKept(t *testing.T) {
	const (
		a = `{"sourceEntityId":"a","position":{"latitude":1,"longitude":2}}`
		b = `{"sourceEntityId":"b","position":{"latitude":3,"longitude":4}}`
	)
	binary, err := proto.Marshal(&sentinelv1.Observation{SourceEntityId: "a"})
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		encoding    string
		payload     string
		raw         []string // the raw payload of each observation, in order
		contentType string
	}{
		"one object line":         {"json", a + "\n", []string{a + "\n"}, "application/json"},
		"two object lines":        {"json", a + "\r\n" + b + "\n", []string{a + "\r\n" + b + "\n", a + "\r\n" + b + "\n"}, "application/json"},
		"binary protobuf":         {"protobuf", string(binary), []string{string(binary)}, "application/x-protobuf"},
		"a batch":                 {"json", "[" + a + ", " + b + "]\n", []string{a, b}, "application/json"},
		"a batch of one":          {"json", "[" + a + "]", []string{a}, "application/json"},
		"object line, then batch": {"json", a + "\n[" + b + "," + a + "]\n", []string{a + "\n[" + b + "," + a + "]\n", b, a}, "application/json"},
	} {
		d, err := NewFromParts(driver.Spec{FeedID: "f"}, driver.Deps{Logger: zap.NewNop()},
			transport.Config{Kind: "udp", Address: "127.0.0.1:0"}, "",
			decoder.Config{Name: "sentinel", Options: json.RawMessage(`{"encoding":"` + tc.encoding + `"}`)})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		sink := newSink()
		d.decodeOne(context.Background(), transport.Frame{Payload: []byte(tc.payload)}, sink)
		if len(sink.obs) != len(tc.raw) {
			t.Errorf("%s: %d observations, want %d (dead letters %v)", name, len(sink.obs), len(tc.raw), sink.deadLetters())
			continue
		}
		for i, o := range sink.obs {
			if string(o.Raw) != tc.raw[i] {
				t.Errorf("%s: observation %d raw = %q, want %q", name, i, o.Raw, tc.raw[i])
			}
			// The content type stays what the decoder says it is.
			if o.RawContentType != tc.contentType {
				t.Errorf("%s: observation %d content type = %q, want %q", name, i, o.RawContentType, tc.contentType)
			}
		}
	}
}

// The rule behind it is the driver's, not the sentinel decoder's: whatever a
// decoder left empty gets the frame, whatever it set is kept.
func TestDecodeOneKeepsRawOnlyWhereTheDecoderSetIt(t *testing.T) {
	dec := decoder.Stateless(func(transport.Frame) ([]*obs.Observation, error) {
		return []*obs.Observation{
			{SourceEntityID: "own", Raw: []byte("part")},
			{SourceEntityID: "unset"},
			{SourceEntityID: "empty", Raw: []byte{}},
		}, nil
	})
	d := newFakeDriver(driver.BackpressureBlock, &fakeTransport{}, dec)
	sink := newSink()
	d.decodeOne(context.Background(), transport.Frame{Payload: []byte("whole frame")}, sink)
	want := map[string]string{"own": "part", "unset": "whole frame", "empty": "whole frame"}
	if len(sink.obs) != len(want) {
		t.Fatalf("%d observations, want %d", len(sink.obs), len(want))
	}
	for _, o := range sink.obs {
		if got := string(o.Raw); got != want[o.SourceEntityID] {
			t.Errorf("%s: raw = %q, want %q", o.SourceEntityID, got, want[o.SourceEntityID])
		}
	}
}
