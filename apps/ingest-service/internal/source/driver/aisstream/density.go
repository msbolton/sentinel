package aisstream

import (
	"math"
	"sort"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/ingest-service/internal/aoi"
)

// counter remembers where each vessel was last seen, to count distinct
// vessels per grid cell over a sliding window. It is used from one
// goroutine.
type counter struct {
	cellDeg float64
	window  time.Duration
	seen    map[string]sighting
}

type sighting struct {
	lat, lon float64
	at       time.Time
}

func newCounter(cellDeg float64, window time.Duration) *counter {
	return &counter{cellDeg: cellDeg, window: window, seen: map[string]sighting{}}
}

func (c *counter) observe(id string, lat, lon float64, at time.Time) {
	c.seen[id] = sighting{lat: lat, lon: lon, at: at}
}

// grid counts the vessels seen within the window inside any of the boxes,
// per cell, and forgets older sightings.
func (c *counter) grid(now time.Time, boxes []aoi.BBox) (cells []*sentinelv1.DensityCell, total int) {
	type key struct{ lat, lon int }
	counts := map[key]uint32{}
	for id, s := range c.seen {
		if now.Sub(s.at) > c.window {
			delete(c.seen, id)
			continue
		}
		if !inAnyBox(boxes, s.lat, s.lon) {
			continue
		}
		counts[key{int(math.Floor(s.lat / c.cellDeg)), int(math.Floor(s.lon / c.cellDeg))}]++
		total++
	}
	cells = make([]*sentinelv1.DensityCell, 0, len(counts))
	for k, n := range counts {
		cells = append(cells, &sentinelv1.DensityCell{
			Latitude:  (float64(k.lat) + 0.5) * c.cellDeg,
			Longitude: (float64(k.lon) + 0.5) * c.cellDeg,
			Count:     n,
		})
	}
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].Latitude != cells[j].Latitude {
			return cells[i].Latitude < cells[j].Latitude
		}
		return cells[i].Longitude < cells[j].Longitude
	})
	return cells, total
}

// publishDensity sends the current grid while the viewed area is too large
// to store vessels one by one.
func (d *Driver) publishDensity() {
	if d.densityOut == nil {
		return
	}
	now := d.now()
	d.mu.Lock()
	mode := d.modeLocked(now)
	boxes := d.area.Boxes
	d.mu.Unlock()
	if mode != modeDensity {
		return
	}
	cells, total := d.counter.grid(now, boxes)
	grid := &sentinelv1.DensityGrid{
		Source:           sentinelv1.EntitySource_ENTITY_SOURCE_AIS,
		EntityType:       sentinelv1.EntityType_ENTITY_TYPE_VESSEL,
		CellDegrees:      d.counter.cellDeg,
		WindowSeconds:    uint32(d.counter.window / time.Second),
		CapSquareDegrees: d.maxArea,
		GeneratedAt:      timestamppb.New(now),
		Cells:            cells,
		Total:            uint32(total),
	}
	err := d.densityOut.PublishDensity(grid)
	d.mu.Lock()
	defer d.mu.Unlock()
	if err != nil {
		d.densityErr = err.Error()
		return
	}
	d.densityErr = ""
	d.densityCells, d.densityVessels, d.densityAt = len(cells), total, now
}
