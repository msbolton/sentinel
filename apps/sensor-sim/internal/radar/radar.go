// Package radar is the simulated radar: it decides which scenario targets a
// sector radar at a fixed site would detect and turns each into an
// Observation carrying a polar measurement and a RadarDetail. It does no I/O.
//
// All randomness comes from one generator seeded from the run seed, so a fixed
// seed and the same sequence of Detect calls yield identical output.
package radar

import (
	"context"
	"math"
	"math/rand/v2"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/sentinel/go-common/geo"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/sensor-sim/internal/config"
	"github.com/sentinel/sensor-sim/internal/detect"
	"github.com/sentinel/sensor-sim/internal/scenario"
)

// Prefixes of the identities the radar gives a target. Both are stable for a
// target id, so the server sees one track per target.
const (
	EntityIDPrefix = "RADAR-"
	TrackIDPrefix  = "RT-"
)

const (
	// classifyCorrect is the probability that the classification is the truth;
	// otherwise it is UNKNOWN.
	classifyCorrect = 0.9
	// Classification confidence is uniform in [confMin, confMax].
	confMin, confMax = 0.6, 0.95
	// snrAtMaxRange is the SNR of a vehicle at the maximum range, in dB; SNR
	// falls with the fourth power of range (40 dB per decade) and follows RCS.
	snrAtMaxRange = 18.0
	snrFloor      = 3.0
)

// rcsFactor scales the detection probability by how well a kind reflects.
var rcsFactor = map[string]float64{
	scenario.KindVehicle: 1.0,
	scenario.KindPerson:  0.3,
	scenario.KindDrone:   0.15,
}

// rcsDbsm is a nominal radar cross-section by kind.
var rcsDbsm = map[string]float64{
	scenario.KindVehicle: 10,
	scenario.KindPerson:  -3,
	scenario.KindDrone:   -15,
}

// DetectionProbability is Pd x clamp((rangeMax/range)^4 x rcsFactor, 0, 1):
// the radar equation's fourth-power range law, scaled by what the target is.
func DetectionProbability(kind string, rangeM, rangeMaxM, pd float64) float64 {
	f, ok := rcsFactor[strings.ToLower(kind)]
	if !ok {
		f = 1
	}
	if rangeM <= 0 {
		return pd
	}
	r := rangeMaxM / rangeM
	return pd * math.Max(0, math.Min(1, r*r*r*r*f))
}

// Radar simulates one radar. It is not safe for concurrent use.
type Radar struct {
	cfg  config.Sensor
	site geo.LLA
	rng  *rand.Rand
}

// New builds a radar at site (altitude in metres above the ellipsoid) that
// seeds its generator from seed.
func New(cfg config.Sensor, site geo.LLA, seed int64) *Radar {
	return &Radar{cfg: cfg, site: site, rng: rand.New(rand.NewPCG(uint64(seed), 0x5241444152))}
}

// covers reports whether a target at azimuth az, elevation el and range
// rangeM is inside the radar's sector.
func (r *Radar) covers(az, el, rangeM float64) bool {
	c := r.cfg
	if rangeM < c.MinRangeKm*1000 || rangeM > c.RangeKm*1000 {
		return false
	}
	if c.Width < 360 && math.Abs(detect.AngleDiff(az, c.Boresight)) > c.Width/2 {
		return false
	}
	if c.ElevationMinDeg != nil && el < *c.ElevationMinDeg {
		return false
	}
	if c.ElevationMaxDeg != nil && el > *c.ElevationMaxDeg {
		return false
	}
	return true
}

// Detect returns one observation for each target in truth that is inside the
// sector and passes the detection roll, in truth order. The measurement is the
// true range, azimuth and elevation plus Gaussian noise; range rate comes from
// the target's velocity (positive is receding) without noise.
func (r *Radar) Detect(_ context.Context, _ time.Time, truth []scenario.Truth) []*sentinelv1.Observation {
	var out []*sentinelv1.Observation
	rangeMax := r.cfg.RangeKm * 1000
	for _, tr := range truth {
		az, el, rangeM := geo.LLAToAER(r.site, tr.At)
		if !r.covers(az, el, rangeM) {
			continue
		}
		if r.rng.Float64() >= DetectionProbability(tr.Kind, rangeM, rangeMax, r.cfg.Pd) {
			continue
		}
		out = append(out, r.observe(tr, az, el, rangeM, rangeMax))
	}
	return out
}

func (r *Radar) observe(tr scenario.Truth, az, el, rangeM, rangeMax float64) *sentinelv1.Observation {
	c := r.cfg
	// Draw order is fixed: range, azimuth, elevation, classification, confidence.
	mRange := math.Max(0, rangeM+r.rng.NormFloat64()*c.SigmaRangeM)
	mAz := detect.WrapAz(az + r.rng.NormFloat64()*c.SigmaAzDeg)
	mEl := el + r.rng.NormFloat64()*c.SigmaElDeg
	class := sentinelv1.RadarClass_RADAR_CLASS_UNKNOWN
	if r.rng.Float64() < classifyCorrect {
		class = radarClass(tr.Kind)
	}
	conf := confMin + r.rng.Float64()*(confMax-confMin)

	rate := rangeRate(r.site, tr)
	kind := strings.ToLower(tr.Kind)
	rcs, ok := rcsDbsm[kind]
	if !ok {
		rcs = rcsDbsm[scenario.KindVehicle]
	}
	snr := math.Max(snrFloor, snrAtMaxRange+(rcs-rcsDbsm[scenario.KindVehicle])+40*math.Log10(rangeMax/rangeM))
	quality := math.Max(0.1, math.Min(1, 1-0.5*rangeM/rangeMax))

	name := tr.DisplayName
	if name == "" {
		name = tr.ID
	}
	return &sentinelv1.Observation{
		SourceEntityId:   EntityIDPrefix + r.cfg.Name + "-" + tr.ID,
		EntityType:       detect.EntityType(tr.Kind),
		TrackEnvironment: detect.Environment(tr.Kind),
		DisplayName:      name,
		Polar: &sentinelv1.PolarMeasurement{
			RangeM:           mRange,
			AzimuthDeg:       mAz,
			ElevationDeg:     proto.Float64(mEl),
			RangeRateMps:     proto.Float64(rate),
			AzimuthReference: sentinelv1.AzimuthReference_AZIMUTH_REFERENCE_TRUE_NORTH,
			Sigma: &sentinelv1.PolarSigma{
				RangeM:       proto.Float64(c.SigmaRangeM),
				AzimuthDeg:   proto.Float64(c.SigmaAzDeg),
				ElevationDeg: proto.Float64(c.SigmaElDeg),
			},
		},
		Platform: &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Radar{Radar: &sentinelv1.RadarDetail{
			RangeRateMps:             proto.Float64(rate),
			RcsDbsm:                  proto.Float64(rcs),
			SnrDb:                    proto.Float64(snr),
			Mode:                     sentinelv1.RadarMode_RADAR_MODE_TRACK,
			TrackQuality:             proto.Float64(quality),
			Classification:           class,
			ClassificationConfidence: proto.Float64(conf),
			RadarTrackId:             TrackIDPrefix + tr.ID,
		}}},
	}
}

// rangeRate is the rate of change of the range from site to the target, m/s:
// positive when it is receding, negative when approaching.
func rangeRate(site geo.LLA, tr scenario.Truth) float64 {
	v := geo.ENUOf(site, geo.ToECEF(tr.At))
	d := math.Sqrt(v.E*v.E + v.N*v.N + v.U*v.U)
	if d == 0 {
		return 0
	}
	return (v.E*tr.VelENU[0] + v.N*tr.VelENU[1] + v.U*tr.VelENU[2]) / d
}

func radarClass(kind string) sentinelv1.RadarClass {
	switch strings.ToLower(kind) {
	case scenario.KindDrone:
		return sentinelv1.RadarClass_RADAR_CLASS_DRONE
	case scenario.KindPerson:
		return sentinelv1.RadarClass_RADAR_CLASS_PERSON
	default:
		return sentinelv1.RadarClass_RADAR_CLASS_VEHICLE
	}
}
