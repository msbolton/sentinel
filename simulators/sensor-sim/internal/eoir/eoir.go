// Package eoir is the simulated EO/IR camera: it decides which scenario
// targets a fixed camera would see (inside its range and field of view, with a
// clear line of sight over the terrain) and turns each into a direction-only
// Observation carrying an EoIrDetail. It does no I/O beyond the terrain
// service.
//
// All randomness comes from one generator seeded from the run seed, so a fixed
// seed and the same sequence of Detect calls yield identical output.
package eoir

import (
	"context"
	"math"
	"math/rand/v2"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/sentinel/go-common/geo"
	"github.com/sentinel/go-common/terrain"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/sensor-sim/internal/config"
	"github.com/sentinel/sensor-sim/internal/detect"
	"github.com/sentinel/sensor-sim/internal/scenario"
)

// Prefixes of the identities the camera gives a target. Both are stable for a
// target id.
const (
	EntityIDPrefix = "EOIR-"
	TrackIDPrefix  = "CT-"
)

const (
	// DefaultElevMinDeg and DefaultElevMaxDeg are the elevation window of a
	// camera whose configuration sets none (config validates the window the
	// camera will use against them).
	DefaultElevMinDeg = config.DefaultCameraElevMinDeg
	DefaultElevMaxDeg = config.DefaultCameraElevMaxDeg

	// terrainWait bounds the line-of-sight check for one detection.
	terrainWait = 2 * time.Second

	// Classification confidence is uniform in [confMin, confMax].
	confMin, confMax = 0.6, 0.95

	// minBoxSide keeps a distant target's bounding box visible, as a fraction of the frame.
	minBoxSide = 0.01
)

// size is a nominal target size in metres (width, height) for the bounding box.
var size = map[string][2]float64{
	scenario.KindVehicle: {5, 2},
	scenario.KindPerson:  {0.6, 1.8},
	scenario.KindDrone:   {0.6, 0.3},
}

// Camera simulates one EO/IR camera. It is not safe for concurrent use.
type Camera struct {
	cfg     config.Sensor
	site    geo.LLA
	terrain *terrain.Service
	rng     *rand.Rand
	elMin   float64
	elMax   float64
	band    sentinelv1.EoIrBand
}

// New builds a camera at site (altitude in metres above the ellipsoid) that
// seeds its generator from seed. t supplies the line-of-sight check; with nil
// every target in the field of view is visible.
func New(cfg config.Sensor, site geo.LLA, t *terrain.Service, seed int64) *Camera {
	c := &Camera{
		cfg: cfg, site: site, terrain: t,
		rng:   rand.New(rand.NewPCG(uint64(seed), 0x454f4952)),
		elMin: DefaultElevMinDeg, elMax: DefaultElevMaxDeg,
		band: band(cfg.Band),
	}
	if cfg.ElevationMinDeg != nil {
		c.elMin = *cfg.ElevationMinDeg
	}
	if cfg.ElevationMaxDeg != nil {
		c.elMax = *cfg.ElevationMaxDeg
	}
	return c
}

// covers reports whether a target at azimuth az, elevation el and range
// rangeM is inside the camera's range and field of view.
func (c *Camera) covers(az, el, rangeM float64) bool {
	cfg := c.cfg
	if rangeM < cfg.MinRangeKm*1000 || rangeM > cfg.RangeKm*1000 {
		return false
	}
	if cfg.Width < 360 && math.Abs(detect.AngleDiff(az, cfg.Boresight)) > cfg.Width/2 {
		return false
	}
	return el >= c.elMin && el <= c.elMax
}

// Detect returns one observation for each target in truth that is inside the
// field of view, passes the detection roll and has a clear line of sight, in
// truth order. A line of sight the terrain service cannot answer counts as
// blocked.
func (c *Camera) Detect(ctx context.Context, _ time.Time, truth []scenario.Truth) []*sentinelv1.Observation {
	var out []*sentinelv1.Observation
	for _, tr := range truth {
		az, el, rangeM := geo.LLAToAER(c.site, tr.At)
		if !c.covers(az, el, rangeM) {
			continue
		}
		if c.rng.Float64() >= c.cfg.Pd {
			continue
		}
		if !c.visible(ctx, tr.At) {
			continue
		}
		out = append(out, c.observe(tr, az, el, rangeM))
	}
	return out
}

func (c *Camera) visible(ctx context.Context, to geo.LLA) bool {
	if c.terrain == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, terrainWait)
	defer cancel()
	ok, err := c.terrain.Visible(ctx, c.site, to)
	return err == nil && ok
}

func (c *Camera) observe(tr scenario.Truth, az, el, rangeM float64) *sentinelv1.Observation {
	cfg := c.cfg
	// Draw order is fixed: azimuth, elevation, confidence.
	mAz := detect.WrapAz(az + c.rng.NormFloat64()*cfg.SigmaAzDeg)
	mEl := el + c.rng.NormFloat64()*cfg.SigmaElDeg
	conf := confMin + c.rng.Float64()*(confMax-confMin)

	name := tr.DisplayName
	if name == "" {
		name = tr.ID
	}
	kind := strings.ToLower(tr.Kind)
	return &sentinelv1.Observation{
		SourceEntityId:   EntityIDPrefix + cfg.Name + "-" + tr.ID,
		EntityType:       detect.EntityType(tr.Kind),
		TrackEnvironment: detect.Environment(tr.Kind),
		DisplayName:      name,
		Polar: &sentinelv1.PolarMeasurement{
			AzimuthDeg:       mAz,
			ElevationDeg:     proto.Float64(mEl),
			AzimuthReference: sentinelv1.AzimuthReference_AZIMUTH_REFERENCE_TRUE_NORTH,
			Sigma: &sentinelv1.PolarSigma{
				AzimuthDeg:   proto.Float64(cfg.SigmaAzDeg),
				ElevationDeg: proto.Float64(cfg.SigmaElDeg),
			},
			DirectionOnly: true,
		},
		Platform: &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Eoir{Eoir: &sentinelv1.EoIrDetail{
			Band:                     c.band,
			Classification:           kind,
			ClassificationConfidence: proto.Float64(conf),
			HorizontalFovDeg:         proto.Float64(cfg.Width),
			VerticalFovDeg:           proto.Float64(c.elMax - c.elMin),
			BoundingBox:              c.box(kind, mAz, mEl, rangeM),
			CameraTrackId:            TrackIDPrefix + tr.ID,
		}}},
	}
}

// box is where the target would appear in the frame: the frame spans the
// horizontal width around the boresight and the elevation window, and the
// target is its nominal size seen from rangeM.
func (c *Camera) box(kind string, az, el, rangeM float64) *sentinelv1.NormalizedBox {
	sz, ok := size[kind]
	if !ok {
		sz = size[scenario.KindVehicle]
	}
	width, span := c.cfg.Width, c.elMax-c.elMin
	cx := 0.5 + detect.AngleDiff(az, c.cfg.Boresight)/width
	cy := 0.5 - (el-(c.elMin+c.elMax)/2)/span
	w := math.Max(minBoxSide, math.Min(1, (sz[0]/rangeM*180/math.Pi)/width))
	h := math.Max(minBoxSide, math.Min(1, (sz[1]/rangeM*180/math.Pi)/span))
	return &sentinelv1.NormalizedBox{
		X:      clamp(cx-w/2, 0, 1-w),
		Y:      clamp(cy-h/2, 0, 1-h),
		Width:  w,
		Height: h,
	}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// band maps the configured name to the proto enum; VISIBLE when unknown.
func band(name string) sentinelv1.EoIrBand {
	if v, ok := sentinelv1.EoIrBand_value["EO_IR_BAND_"+strings.ToUpper(strings.TrimSpace(name))]; ok && v != 0 {
		return sentinelv1.EoIrBand(v)
	}
	return sentinelv1.EoIrBand_EO_IR_BAND_VISIBLE
}
