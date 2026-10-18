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

// TestLiveStream runs the driver against aisstream.io for 30 s over the
// English Channel: go test -tags live -run TestLiveStream with
// AISSTREAM_API_KEY set.
func TestLiveStream(t *testing.T) {
	key := os.Getenv("AISSTREAM_API_KEY")
	if key == "" {
		t.Skip("AISSTREAM_API_KEY not set")
	}
	cfg, _ := json.Marshal(map[string]any{"watchlistMmsi": []string{"235100247"}})
	drv, err := New(driver.Spec{FeedID: "live", Driver: ID, Config: cfg, Secrets: map[string]string{"apiKey": key}}, driver.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	d := drv.(*Driver)
	grids := &gridSink{}
	d.densityOut, d.densityEvery = grids, 5*time.Second
	sink := &memSink{}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); d.Run(ctx, sink) }()

	d.SetAreaOfInterest(aoi.Area{Active: true, Clients: 1, Boxes: []aoi.BBox{{North: 51.5, South: 49.5, East: 2, West: -2}}, ExpiresAt: time.Now().Add(time.Minute)})
	time.Sleep(20 * time.Second)
	detail := d.Health().Detail
	t.Logf("detail mode: %v", detail)
	if sink.count() == 0 {
		t.Error("no observations in 20 s over the Channel")
	}
	area, _ := detail["connections"].(map[string]any)[connArea].(map[string]any)
	if area["confirmed"] != true || area["compression"] != true {
		t.Errorf("area connection = %v", area)
	}

	// Europe: counts instead of vessels.
	stored := sink.count()
	d.SetAreaOfInterest(aoi.Area{Active: true, Clients: 1, Boxes: []aoi.BBox{{North: 70, South: 30, East: 40, West: -30}}, ExpiresAt: time.Now().Add(time.Minute)})
	time.Sleep(15 * time.Second)
	if g := grids.last(); g == nil || g.Total == 0 {
		t.Errorf("no density grid: %+v", g)
	} else {
		t.Logf("grid: %d vessels in %d cells", g.Total, len(g.Cells))
	}
	t.Logf("stored %d in detail mode, %d more in density mode (watchlist only)", stored, sink.count()-stored)
	t.Logf("density mode: %v", d.Health().Detail)
	cancel()
	<-done
}
