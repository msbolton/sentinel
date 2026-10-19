package stream

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/source/decoder"
	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

// orderTransport records the driver's malformed counter at the moment a
// sender's row is updated.
type orderTransport struct {
	d       *Driver
	counter int64
	called  bool
}

func (o *orderTransport) NoteMalformed(string, string, string) {
	o.called, o.counter = true, o.d.malformed.Load()
}

// The sender's row is updated before the driver's counter, so that whoever
// waits for the counter to move (the malformed count in the health detail) finds
// the row already moved, as the listeners do for their own counters.
func TestMalformedRowIsNotedBeforeTheCounter(t *testing.T) {
	ot := &orderTransport{}
	d, err := NewFromParts(driver.Spec{FeedID: "f"}, driver.Deps{Logger: zap.NewNop()},
		transport.Config{Kind: "udp", Address: "127.0.0.1:0"}, "", decoder.Config{Name: "sentinel"})
	if err != nil {
		t.Fatal(err)
	}
	ot.d = d
	d.noter = ot
	d.decodeOne(context.Background(), transport.Frame{Payload: []byte{0xff, 0xff, 0xff}, Remote: "10.0.0.1:1"}, newSink())
	if !ot.called {
		t.Fatal("the sender's row was not updated")
	}
	if ot.counter != 0 {
		t.Errorf("the counter read %d when the row was updated, want 0: the row comes first", ot.counter)
	}
	if got := d.malformed.Load(); got != 1 {
		t.Errorf("malformed = %d, want 1", got)
	}
}
