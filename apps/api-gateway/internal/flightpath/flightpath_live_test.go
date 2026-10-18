//go:build live

package flightpath

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// TestLivePaths asks the real IntelSky for today's and three days' path of
// an aircraft it is tracking now: go test -tags live -run TestLivePaths.
func TestLivePaths(t *testing.T) {
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Get(DefaultBaseURL + "/api/")
	if err != nil {
		t.Skipf("IntelSky unreachable: %v", err)
	}
	defer resp.Body.Close()
	var live struct {
		AC []struct {
			Hex string `json:"hex"`
		} `json:"ac"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&live); err != nil || len(live.AC) == 0 {
		t.Fatalf("live feed: %v (%d aircraft)", err, len(live.AC))
	}
	hex := live.AC[0].Hex
	h := NewHandler(Options{}, nil)
	for _, days := range []int{1, 3} {
		p, err := h.Get(hex, days)
		if err != nil {
			t.Fatalf("%s, %d days: %v", hex, days, err)
		}
		t.Logf("%s, %d days: %d points, meta %+v", hex, days, len(p.Points), p.Meta)
		if days == 1 && len(p.Points) == 0 {
			t.Errorf("a live aircraft should have a path today")
		}
	}
}
