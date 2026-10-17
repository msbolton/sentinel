package detect

import (
	"testing"
	"time"

	"github.com/sentinel/analytics-service/internal/baseline"
	"github.com/sentinel/analytics-service/internal/config"
	"github.com/sentinel/analytics-service/internal/model"
)

func f(v float64) *float64 { return &v }

func testCfg() config.DetectorConfig {
	return config.DetectorConfig{
		SkipTypes:        map[string]bool{"SATELLITE": true},
		PlaceRangeFactor: 1.5,
		PlaceMinM:        config.TypeMap{"VEHICLE": 2000, "DEFAULT": 2000},
		TimeMinShare:     0.002,
		TimeMinDays:      14,
		TimeQuietShare:   0.012,
		TimeQuietGap:     time.Hour,
		MoveZ:            3,
		MoveMaxKnots:     config.TypeMap{"VEHICLE": 200, "DEFAULT": 100},
		MoveMinGap:       5 * time.Second,
		DarkGapFactor:    3,
		DarkMinGap:       10 * time.Minute,
		CoverageCellDeg:  0.05,
		AbsorbWindow:     30 * time.Minute,
		Tick:             time.Minute,
		HighSeverity:     "HIGH",
	}
}

// matureBaseline: one place at (40,-74) with a 50 m radius, a 1 km range
// p95, speed 10 ± 2 kn over 300 fixes, a 60 s gap p95, active on weekdays
// 06:00-20:00 local (UTC-5) with Monday 08:00 the busiest hour, and never
// active at night.
func matureBaseline() *baseline.Baseline {
	b := &baseline.Baseline{EntityID: "e1", EntityType: "VEHICLE", PointCount: 9000, Days: 20, Mature: true, Hours: make([]int16, baseline.Slots)}
	b.Places = []baseline.Place{{ClusterIndex: 0, Lat: 40, Lon: -74, RadiusM: 50}}
	b.Stats.Range.P95M = 1000
	b.Stats.Speed = baseline.Dist{Mean: 10, Stddev: 2, P95: 13, Max: 15, N: 300}
	b.Stats.Gap.P95Sec = 60
	b.Stats.Params.SolarOffsetHours = -5
	for day := 1; day <= 5; day++ {
		for hour := 6; hour < 20; hour++ {
			b.Hours[day*24+hour] = 100 // 70 slots x 100 = 7000
		}
	}
	b.Hours[1*24+8] = 2100 // Monday 08:00: 2000 more, 23% of the 9000
	return b
}

func fixAt(lat, lon float64, at time.Time) model.Fix {
	return model.Fix{EntityID: "e1", EntityType: "VEHICLE", Classification: "UNCLASSIFIED", FeedID: "f1", Lat: lat, Lon: lon, At: at, SpeedKnots: f(10)}
}

var mondayNoonUTC = time.Date(2026, 10, 19, 17, 0, 0, 0, time.UTC) // Monday 12:00 local (UTC-5)

func TestUnusualPlace(t *testing.T) {
	cfg := testCfg()
	b := matureBaseline()
	if h := UnusualPlace(b, fixAt(40.001, -74, mondayNoonUTC), cfg); h != nil {
		t.Errorf("110 m away inside range fired: %+v", h)
	}
	if h := UnusualPlace(b, fixAt(40.015, -74, mondayNoonUTC), cfg); h != nil {
		t.Errorf("1.6 km away, inside the 2 km type minimum, fired: %+v", h)
	}
	h := UnusualPlace(b, fixAt(40.03, -74, mondayNoonUTC), cfg)
	if h == nil || h.Severity != SeverityMedium {
		t.Fatalf("3.3 km away: %+v", h)
	}
	if h := UnusualPlace(b, fixAt(40.1, -74, mondayNoonUTC), cfg); h == nil || h.Severity != SeverityHigh || h.Score < 5 {
		t.Errorf("11 km away: %+v", h)
	}
	immature := matureBaseline()
	immature.Mature = false
	if h := UnusualPlace(immature, fixAt(41, -74, mondayNoonUTC), cfg); h != nil {
		t.Error("immature baseline fired")
	}
	if h := UnusualPlace(nil, fixAt(41, -74, mondayNoonUTC), cfg); h != nil {
		t.Error("nil baseline fired")
	}
}

func TestUnusualTime(t *testing.T) {
	cfg := testCfg()
	b := matureBaseline()
	if h := UnusualTime(b, fixAt(40, -74, mondayNoonUTC), cfg); h != nil {
		t.Errorf("noon, a normal slot, fired: %+v", h)
	}
	threeAM := time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC) // Monday 03:00 local
	h := UnusualTime(b, fixAt(40, -74, threeAM), cfg)
	if h == nil || h.Severity != SeverityMedium || h.Kind != KindUnusualTime {
		t.Fatalf("03:00 local, never active: %+v", h)
	}
	if h.Actual["localHour"] != 3 {
		t.Errorf("actual = %v", h.Actual)
	}
	// Sunday afternoon: no history at all, also unusual.
	sunday := time.Date(2026, 10, 18, 19, 0, 0, 0, time.UTC) // Sunday 14:00 local
	if h := UnusualTime(b, fixAt(40, -74, sunday), cfg); h == nil {
		t.Error("Sunday afternoon, never active, did not fire")
	}
	young := matureBaseline()
	young.Days = 5
	if h := UnusualTime(young, fixAt(40, -74, threeAM), cfg); h != nil {
		t.Error("five days of history fired")
	}
}

func TestUnusualMovement(t *testing.T) {
	cfg := testCfg()
	b := matureBaseline()
	t0 := mondayNoonUTC
	prev := fixAt(40, -74, t0)
	// Teleport: 100 km in 10 s, no baseline needed.
	cur := fixAt(40.9, -74, t0.Add(10*time.Second))
	h := UnusualMovement(nil, cur, &prev, cfg)
	if h == nil || h.Severity != SeverityHigh || h.Actual["impliedKnots"].(float64) < 10000 {
		t.Fatalf("teleport: %+v", h)
	}
	if !h.EvidenceStart.Equal(t0) {
		t.Error("evidence window should start at the previous fix")
	}
	// Same move over an hour is 54 kn: fine for a vehicle.
	if h := UnusualMovement(nil, fixAt(40.9, -74, t0.Add(time.Hour)), &prev, cfg); h != nil {
		t.Errorf("plausible move fired: %+v", h)
	}
	// Below the minimum gap nothing is judged (clock jitter).
	if h := UnusualMovement(nil, fixAt(40.9, -74, t0.Add(time.Second)), &prev, cfg); h != nil {
		t.Error("sub-gap move fired")
	}
	// z-score: 17 kn is 3.5 sigma from 10 ± 2.
	slow := fixAt(40, -74, t0.Add(time.Minute))
	slow.SpeedKnots = f(17)
	h = UnusualMovement(b, slow, nil, cfg)
	if h == nil || h.Severity != SeverityMedium {
		t.Fatalf("z=3.5: %+v", h)
	}
	fast := slow
	fast.SpeedKnots = f(30)
	if h := UnusualMovement(b, fast, nil, cfg); h == nil || h.Severity != SeverityHigh {
		t.Errorf("z=10: %+v", h)
	}
	normal := slow
	normal.SpeedKnots = f(12)
	if h := UnusualMovement(b, normal, nil, cfg); h != nil {
		t.Errorf("z=1 fired: %+v", h)
	}
	if h := UnusualMovement(nil, fast, nil, cfg); h != nil {
		t.Error("z-score without a baseline")
	}
}

func TestWentDarkAndReappeared(t *testing.T) {
	cfg := testCfg()
	b := matureBaseline() // gap p95 60 s -> threshold max(180 s, 10 min) = 10 min
	last := LastFix{Fix: fixAt(40, -74, mondayNoonUTC)}
	if th := DarkThreshold(b, cfg); th != 10*time.Minute {
		t.Errorf("threshold = %v, want 10m", th)
	}
	if h := WentDark(b, last, mondayNoonUTC.Add(5*time.Minute), true, cfg); h != nil {
		t.Error("5 minutes of silence fired")
	}
	if h := WentDark(b, last, mondayNoonUTC.Add(20*time.Minute), false, cfg); h != nil {
		t.Error("silence without coverage fired")
	}
	h := WentDark(b, last, mondayNoonUTC.Add(20*time.Minute), true, cfg)
	if h == nil || h.Severity != SeverityMedium || h.Kind != KindWentDark {
		t.Fatalf("20 minutes: %+v", h)
	}
	if h := WentDark(b, last, mondayNoonUTC.Add(time.Hour), true, cfg); h == nil || h.Severity != SeverityHigh {
		t.Errorf("an hour: %+v", h)
	}
	if h := WentDark(nil, last, mondayNoonUTC.Add(time.Hour), true, cfg); h != nil {
		t.Error("no baseline, no rhythm, fired")
	}

	dark := last
	dark.Dark = true
	// Back after 30 minutes, 2 km away: at 13 kn p95 it could cover 12 km. LOW.
	h = Reappeared(b, fixAt(40.018, -74, mondayNoonUTC.Add(30*time.Minute)), dark)
	if h == nil || h.Severity != SeverityLow {
		t.Fatalf("plausible reappearance: %+v", h)
	}
	// Back after 30 minutes 100 km away: HIGH.
	if h := Reappeared(b, fixAt(40.9, -74, mondayNoonUTC.Add(30*time.Minute)), dark); h == nil || h.Severity != SeverityHigh {
		t.Errorf("implausible reappearance: %+v", h)
	}
	if h := Reappeared(b, fixAt(40.9, -74, mondayNoonUTC.Add(30*time.Minute)), last); h != nil {
		t.Error("reappeared without having been dark")
	}
}

func TestQuietSlot(t *testing.T) {
	cfg := testCfg()
	b := matureBaseline()
	mondayEight := time.Date(2026, 10, 19, 13, 30, 0, 0, time.UTC) // Monday 08:30 local, the busy slot
	last := LastFix{Fix: fixAt(40, -74, mondayEight.Add(-2*time.Hour))}
	h := QuietSlot(b, last, mondayEight, cfg)
	if h == nil || h.Kind != KindUnusualTime || h.Severity != SeverityMedium {
		t.Fatalf("quiet busy slot: %+v", h)
	}
	if h := QuietSlot(b, LastFix{Fix: fixAt(40, -74, mondayEight.Add(-10*time.Minute))}, mondayEight, cfg); h != nil {
		t.Error("recent fix fired")
	}
	if h := QuietSlot(b, last, mondayNoonUTC, cfg); h != nil {
		t.Error("an ordinary slot fired")
	}
	dark := last
	dark.Dark = true
	if h := QuietSlot(b, dark, mondayEight, cfg); h != nil {
		t.Error("a dark entity gets a quiet-slot finding on top")
	}
}
