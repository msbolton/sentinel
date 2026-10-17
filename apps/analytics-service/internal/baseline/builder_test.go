package baseline

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/sentinel/analytics-service/internal/geo"
)

// Home and work near New York: about 6.9 km apart, UTC-5 solar time.
var (
	homeLat, homeLon = 40.0000, -74.0000
	workLat, workLon = 40.0500, -74.0500
)

func f(v float64) *float64 { return &v }

// commuterTrack is a synthetic track: at home overnight, a 30 minute drive
// at 40 km/h, at work until 17:00, home again. Dwell points every 5
// minutes with 20 m jitter, commute points every minute.
func commuterTrack(days int, start time.Time) []Point {
	r := rand.New(rand.NewSource(42))
	jitter := func(lat, lon float64) (float64, float64) {
		return lat + r.NormFloat64()*20/111000, lon + r.NormFloat64()*20/85000
	}
	var pts []Point
	for d := 0; d < days; d++ {
		day := start.Add(time.Duration(d) * 24 * time.Hour)
		dwell := func(lat, lon float64, from, to time.Duration) {
			for m := from; m < to; m += 5 * time.Minute {
				la, lo := jitter(lat, lon)
				pts = append(pts, Point{Lat: la, Lon: lo, SpeedKnots: f(0), AltitudeM: f(10), At: day.Add(m)})
			}
		}
		drive := func(fromLat, fromLon, toLat, toLon float64, from time.Duration) {
			for m := 0; m < 30; m++ {
				frac := float64(m) / 30
				pts = append(pts, Point{
					Lat: fromLat + (toLat-fromLat)*frac, Lon: fromLon + (toLon-fromLon)*frac,
					SpeedKnots: f(21.6), AltitudeM: f(10), At: day.Add(from + time.Duration(m)*time.Minute),
				})
			}
		}
		// Local 00:00 is 05:00 UTC.
		base := 5 * time.Hour
		dwell(homeLat, homeLon, base, base+8*time.Hour)
		drive(homeLat, homeLon, workLat, workLon, base+8*time.Hour)
		dwell(workLat, workLon, base+8*time.Hour+30*time.Minute, base+17*time.Hour)
		drive(workLat, workLon, homeLat, homeLon, base+17*time.Hour)
		dwell(homeLat, homeLon, base+17*time.Hour+30*time.Minute, base+24*time.Hour)
	}
	return pts
}

func testConfig() Config {
	return Config{WindowDays: 30, ThinSec: 30, SlowKnots: 2, EpsM: 150, MinPts: 10, MaxSlowPoints: 20000, MinPoints: 200, MinDays: 3, MaxPlaces: 20}
}

func TestBuildCommuterBaseline(t *testing.T) {
	start := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC) // a Monday
	pts := commuterTrack(10, start)
	b := Build("e1", "VEHICLE", pts, start, start.Add(10*24*time.Hour), testConfig())

	if b.PointCount != len(pts) || b.Days != 10 || !b.Mature {
		t.Fatalf("pointCount=%d days=%d mature=%v", b.PointCount, b.Days, b.Mature)
	}
	if b.Stats.Params.SolarOffsetHours != -5 {
		t.Errorf("solar offset = %d, want -5", b.Stats.Params.SolarOffsetHours)
	}
	if len(b.Places) != 2 {
		t.Fatalf("places = %d, want 2: %+v", len(b.Places), b.Places)
	}
	home := b.Places[0] // most points: the overnight dwell
	if geo.HaversineM(home.Lat, home.Lon, homeLat, homeLon) > 30 {
		t.Errorf("first place is not home: %+v", home)
	}
	if home.RadiusM < MinPlaceRadiusM || home.RadiusM > 100 {
		t.Errorf("home radius = %.0f m, want between 25 and 100", home.RadiusM)
	}
	// Home is visited twice a day (overnight stay spans midnight into the next
	// day, so 10 days give 11 overnight stays, split by the work day).
	if home.Visits < 10 || home.Visits > 12 {
		t.Errorf("home visits = %d, want about 11", home.Visits)
	}
	if home.MedianDwellSec < 6*3600 {
		t.Errorf("home median dwell = %.0f s, want hours", home.MedianDwellSec)
	}
	work := b.Places[1]
	if work.Visits != 10 {
		t.Errorf("work visits = %d, want 10", work.Visits)
	}
	// Range: inside a place for most fixes; the commute reaches ~3.4 km out.
	if b.Stats.Range.P50M != 0 {
		t.Errorf("range p50 = %.0f, want 0", b.Stats.Range.P50M)
	}
	if b.Stats.Range.P95M < 1000 || b.Stats.Range.P95M > 4000 {
		t.Errorf("range p95 = %.0f m, want between 1 and 4 km", b.Stats.Range.P95M)
	}
	// Hours: everything is accounted for, and the local 08:00 weekday slot is busy.
	var sum int
	for _, h := range b.Hours {
		sum += int(h)
	}
	if sum != b.PointCount {
		t.Errorf("histogram sums to %d, want %d", sum, b.PointCount)
	}
	mondayEight := 1*24 + 8
	if b.SlotShare(mondayEight) == 0 {
		t.Error("Monday 08:00 local has no activity")
	}
	// Speed statistics include the drives; the p95 is a drive.
	if b.Stats.Speed.N != b.PointCount || math.Abs(b.Stats.Speed.Max-21.6) > 0.01 {
		t.Errorf("speed stats = %+v", b.Stats.Speed)
	}
	if b.Stats.Gap.P50Sec != 300 {
		t.Errorf("gap p50 = %.0f s, want 300", b.Stats.Gap.P50Sec)
	}
	// Nearest place for a far position reports the distance outside the radius.
	if _, d, ok := b.NearestPlace(41, -74); !ok || d < 100000 {
		t.Errorf("nearest place from 111 km away: d=%.0f ok=%v", d, ok)
	}
}

func TestBuildImmatureAndEmpty(t *testing.T) {
	start := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	b := Build("e2", "VEHICLE", commuterTrack(2, start), start, start.Add(48*time.Hour), testConfig())
	if b.Mature {
		t.Error("two days must not be mature")
	}
	if b.Days != 2 {
		t.Errorf("days = %d", b.Days)
	}
	empty := Build("e3", "VESSEL", nil, start, start, testConfig())
	if empty.PointCount != 0 || empty.Mature || len(empty.Hours) != Slots || len(empty.Places) != 0 {
		t.Errorf("empty baseline = %+v", empty)
	}
}

func TestBuildWithoutSpeedUsesImpliedSpeed(t *testing.T) {
	start := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	pts := commuterTrack(4, start)
	for i := range pts {
		pts[i].SpeedKnots = nil
	}
	b := Build("e4", "VEHICLE", pts, start, start.Add(4*24*time.Hour), testConfig())
	if len(b.Places) != 2 {
		t.Errorf("places without reported speed = %d, want 2", len(b.Places))
	}
	if b.Stats.Speed.N != 0 {
		t.Error("speed stats from nothing")
	}
}
