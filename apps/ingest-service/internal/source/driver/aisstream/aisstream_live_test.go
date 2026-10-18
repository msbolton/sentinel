//go:build live

package aisstream

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/aoi"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

// TestLiveStream runs the driver against aisstream.io for 30 s with a view
// over the English Channel: go test -tags live -run TestLiveStream with
// AISSTREAM_API_KEY set. Vessels in the Channel are full-rate fixes, the
// rest of the world background fixes.
func TestLiveStream(t *testing.T) {
	key := os.Getenv("AISSTREAM_API_KEY")
	if key == "" {
		t.Skip("AISSTREAM_API_KEY not set")
	}
	cfg, _ := json.Marshal(map[string]any{})
	drv, err := New(driver.Spec{FeedID: "live", Driver: ID, Config: cfg, Secrets: map[string]string{"apiKey": key}}, driver.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	d := drv.(*Driver)
	sink := &memSink{}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); d.Run(ctx, sink) }()

	d.SetAreaOfInterest(aoi.Area{Active: true, Clients: 1, Boxes: []aoi.BBox{{North: 51.5, South: 49.5, East: 2, West: -2}}, ExpiresAt: time.Now().Add(time.Minute)})
	time.Sleep(30 * time.Second)
	detail := d.Health().Detail
	t.Logf("detail: %v", detail)
	if detail["detail"].(int64) == 0 || detail["background"].(int64) == 0 {
		t.Errorf("want both full-rate and background fixes: %v", detail)
	}
	if c, _ := detail["connection"].(map[string]any); c["confirmed"] != true || c["compression"] != true {
		t.Errorf("connection = %v", c)
	}
	cancel()
	<-done
}
