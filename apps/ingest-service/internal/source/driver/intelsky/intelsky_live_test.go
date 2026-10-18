//go:build live

package intelsky

import (
	"context"
	"testing"
)

// TestLivePoll polls the real IntelSky once: go test -tags live -run TestLivePoll.
func TestLivePoll(t *testing.T) {
	d := newDriver(t, defaultURL, nil)
	sink := &memSink{}
	d.poll(context.Background(), sink)
	h := d.Health()
	t.Logf("health: %+v", h)
	if h.LastError != "" {
		t.Fatalf("poll failed: %s", h.LastError)
	}
	if len(sink.obs) < 10 {
		t.Errorf("only %d aircraft", len(sink.obs))
	}
	withCountry := 0
	for _, o := range sink.obs {
		if o.CountryOfOrigin != "" {
			withCountry++
		}
	}
	t.Logf("%d aircraft, %d with a country; first %s %s %s", len(sink.obs), withCountry, sink.obs[0].DisplayName, sink.obs[0].CountryOfOrigin, sink.obs[0].ADSB.OperatorName)
	if withCountry*2 < len(sink.obs) {
		t.Errorf("most aircraft should carry a country")
	}
}
