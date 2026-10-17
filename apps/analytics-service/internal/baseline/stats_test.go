package baseline

import (
	"math"
	"testing"
	"time"
)

func TestQuantile(t *testing.T) {
	if Quantile(nil, 0.5) != 0 {
		t.Error("empty")
	}
	if Quantile([]float64{7}, 0.9) != 7 {
		t.Error("single value")
	}
	s := []float64{1, 2, 3, 4, 5}
	if Quantile(s, 0.5) != 3 || Quantile(s, 0) != 1 || Quantile(s, 1) != 5 {
		t.Errorf("exact indices: %v %v %v", Quantile(s, 0.5), Quantile(s, 0), Quantile(s, 1))
	}
	if got := Quantile([]float64{10, 20}, 0.25); got != 12.5 {
		t.Errorf("interpolation = %v, want 12.5", got)
	}
}

func TestMeanStdAndSummarise(t *testing.T) {
	mean, std := MeanStd([]float64{2, 4, 4, 4, 5, 5, 7, 9})
	if mean != 5 || math.Abs(std-2) > 1e-9 {
		t.Errorf("mean %v std %v", mean, std)
	}
	d := Summarise([]float64{3, 1, 2})
	if d.N != 3 || d.Max != 3 || d.Mean != 2 {
		t.Errorf("summarise = %+v", d)
	}
}

func TestHourOfWeekWithSolarOffset(t *testing.T) {
	monday := time.Date(2026, 10, 19, 0, 30, 0, 0, time.UTC) // Monday 00:30 UTC
	if got := HourOfWeek(monday, 0); got != 24 {
		t.Errorf("Monday 00:30 UTC slot = %d, want 24", got)
	}
	// Five hours west it is still Sunday 19:30.
	if got := HourOfWeek(monday, -5); got != 19 {
		t.Errorf("Monday 00:30 UTC at UTC-5 slot = %d, want 19", got)
	}
	// Nine hours east it is Monday 09:30.
	if got := HourOfWeek(monday, 9); got != 33 {
		t.Errorf("Monday 00:30 UTC at UTC+9 slot = %d, want 33", got)
	}
}

func TestEpisodesSplitOnGapAndOtherCluster(t *testing.T) {
	t0 := time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	points := []Point{{At: at(0)}, {At: at(5)}, {At: at(10)}, {At: at(15)}, {At: at(20)}, {At: at(25)}, {At: at(90)}, {At: at(95)}}
	labels := []int{0, 0, 1, 0, 0, Noise, 0, 0}
	eps := Episodes(points, labels, 0, 30*time.Minute)
	// 0-5 (then cluster 1 intervenes), 15-20 (then a 65 min gap), 90-95.
	if len(eps) != 3 {
		t.Fatalf("episodes = %d, want 3: %+v", len(eps), eps)
	}
	if !eps[0].End.Equal(at(5)) || !eps[1].Start.Equal(at(15)) || !eps[2].Start.Equal(at(90)) {
		t.Errorf("episodes = %+v", eps)
	}
}
