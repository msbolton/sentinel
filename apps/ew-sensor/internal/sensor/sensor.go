// Package sensor is the pure simulation core of the ESM sensor: given the
// platform pose at a tick, it moves the emitters, decides which are inside
// the coverage sector, rolls for detection and produces the geolocated fixes
// as Observation messages. It does no I/O.
//
// All randomness comes from one generator seeded from the run seed and owned
// by the Sensor, so a fixed seed and the same sequence of Step calls yield
// identical output.
package sensor

import (
	"math"
	"math/rand/v2"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/ew-sensor/internal/config"
	"github.com/sentinel/ew-sensor/internal/emitter"
	"github.com/sentinel/ew-sensor/internal/ownship"
	"github.com/sentinel/go-common/geo"
)

// EntityIDPrefix is prepended to an emitter id to form source_entity_id.
const EntityIDPrefix = "EMITTER-"

// referenceRangeKm is the range out to which the cross-range error equals the
// along-range error; beyond it the cross-range error grows linearly.
const referenceRangeKm = 10.0

// Sensor simulates the sensor and its emitters.
type Sensor struct {
	cfg      config.Config
	rng      *rand.Rand
	emitters []*emitter.Emitter
	last     time.Time
	started  bool
}

// New builds a Sensor from cfg: it creates the emitters (synthesising any the
// configuration asks for but does not list) and seeds its generator from
// cfg.Seed.
func New(cfg config.Config) *Sensor {
	return &Sensor{
		cfg:      cfg,
		rng:      rand.New(rand.NewPCG(uint64(cfg.Seed), 0x53454e534f52)),
		emitters: emitter.Build(cfg),
	}
}

// Emitters returns the simulated emitters.
func (s *Sensor) Emitters() []*emitter.Emitter { return s.emitters }

// Step advances the emitters to t (by the time since the previous Step; the
// first call does not move them), then returns the fixes for this tick: one
// per emitter that is inside coverage from pose and passes the detection
// roll, in emitter order. pose is the platform pose at t.
func (s *Sensor) Step(t time.Time, pose ownship.Pose) []*sentinelv1.Observation {
	if s.started {
		dt := t.Sub(s.last)
		for _, e := range s.emitters {
			e.Advance(dt)
		}
	}
	s.last, s.started = t, true

	site := pose.Position()
	bore := s.cfg.Emplacement.Boresight
	if strings.EqualFold(s.cfg.Emplacement.AzimuthReference, config.AzimuthPlatform) {
		bore += pose.HeadingDeg
	}

	var out []*sentinelv1.Observation
	for _, e := range s.emitters {
		az, el, rangeM, ok := Covered(site, bore, s.cfg.Emplacement, e.Position())
		if !ok {
			continue
		}
		if s.rng.Float64() >= s.cfg.Sensor.Pd {
			continue
		}
		out = append(out, s.observe(t, e, site, az, el, rangeM))
	}
	return out
}

// Covered reports whether target is inside the coverage of a sensor at site
// whose effective boresight (already rotated by the platform heading when the
// reference is PLATFORM) is boresightDeg. It also returns the azimuth,
// elevation and range of target from site. The range must lie in
// [MinRangeKm, RangeKm] and the azimuth within half the width of the
// boresight; a width of 360 or more is omni.
func Covered(site geo.LLA, boresightDeg float64, e config.Emplacement, target geo.LLA) (az, el, rangeM float64, ok bool) {
	az, el, rangeM = geo.LLAToAER(site, target)
	if rangeM < e.MinRangeKm*1000 || rangeM > e.RangeKm*1000 {
		return az, el, rangeM, false
	}
	if e.Width >= 360 {
		return az, el, rangeM, true
	}
	// The boresight can be any angle (a PLATFORM boresight plus the heading
	// reaches 720), so wrap the difference without assuming its sign.
	off := math.Mod(math.Abs(az-boresightDeg), 360)
	if off > 180 {
		off = 360 - off
	}
	return az, el, rangeM, off <= e.Width/2
}

// observe turns a detected emitter into a fix. The fix is the true position
// plus independent Gaussian errors along the line of sight (sigma = SigmaM)
// and across it (sigma = SigmaM * max(1, range_km/10)); the reported 95%
// ellipse is built from those same sigmas, with its major axis across the line
// of sight.
func (s *Sensor) observe(t time.Time, e *emitter.Emitter, site geo.LLA, az, el, rangeM float64) *sentinelv1.Observation {
	sigAlong := s.cfg.Sensor.SigmaM
	sigCross := sigAlong * math.Max(1, rangeM/1000/referenceRangeKm)

	// Draw order is fixed: along, then cross.
	dAlong := s.rng.NormFloat64() * sigAlong
	dCross := s.rng.NormFloat64() * sigCross

	truth := geo.ENUOf(site, geo.ToECEF(e.Position()))
	sin, cos := math.Sincos(az * math.Pi / 180)
	// Unit vectors in (E,N): along = line of sight, cross = 90 degrees clockwise.
	fix := geo.FromECEF(geo.ECEFOf(site, geo.ENU{
		E: truth.E + dAlong*sin + dCross*cos,
		N: truth.N + dAlong*cos - dCross*sin,
		U: truth.U,
	}))

	semiMinor := geo.Ellipse95 * sigAlong
	semiMajor := math.Max(geo.Ellipse95*sigCross, semiMinor)

	c := e.Config()
	o := &sentinelv1.Observation{
		SourceEntityId:   EntityIDPrefix + c.ID,
		EntityType:       sentinelv1.EntityType_ENTITY_TYPE_SIGNAL,
		TrackEnvironment: trackEnvironment(c.TrackEnvironment),
		DisplayName:      c.DisplayName,
		Position:         &sentinelv1.Coordinate{Latitude: fix.Lat, Longitude: fix.Lon},
		Uncertainty: &sentinelv1.MeasurementUncertainty{
			SemiMajor:   semiMajor,
			SemiMinor:   semiMinor,
			Orientation: math.Mod(az+90, 180),
		},
		Platform: &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Rf{Rf: rfDetail(c)}},
	}
	if o.DisplayName == "" {
		o.DisplayName = c.ID
	}
	if c.Affiliation != "" {
		o.Affiliation = affiliation(c.Affiliation)
	}
	if s.cfg.Sensor.Classification != "" {
		o.Classification = classification(s.cfg.Sensor.Classification)
	}
	if s.cfg.Sensor.SendTimestamps {
		o.SourceTimestamp = timestamppb.New(t)
	}
	return o
}

// EmplacementState builds the ownship report for pose. The time is set only
// when sensor.sendTimestamps is on, so that by default the server stamps its
// own arrival time.
func (s *Sensor) EmplacementState(emplacementID string, t time.Time, pose ownship.Pose) *sentinelv1.EmplacementState {
	st := &sentinelv1.EmplacementState{
		EmplacementId: emplacementID,
		Position: &sentinelv1.Coordinate{
			Latitude:       pose.Lat,
			Longitude:      pose.Lon,
			AltitudeMeters: proto.Float64(pose.Alt),
		},
		Heading:  proto.Float64(pose.HeadingDeg),
		Velocity: &sentinelv1.GeodeticVelocity{North: pose.VelNorth, East: pose.VelEast, Up: pose.VelUp},
	}
	if s.cfg.Sensor.SendTimestamps {
		st.Time = timestamppb.New(t)
	}
	return st
}

func rfDetail(c config.Emitter) *sentinelv1.RfDetail {
	rf := &sentinelv1.RfDetail{
		FrequencyHz: c.FrequencyHz,
		EmitterType: c.EmitterType,
		Modulation:  c.Modulation,
		ScanType:    c.ScanType,
		PowerDbm:    c.PowerDbm,
	}
	if c.BandwidthHz != 0 {
		rf.BandwidthHz = proto.Float64(c.BandwidthHz)
	}
	if c.PriUs != 0 {
		rf.PriUs = proto.Float64(c.PriUs)
	}
	if c.PulseWidthUs != 0 {
		rf.PulseWidthUs = proto.Float64(c.PulseWidthUs)
	}
	if rf.PowerDbm != nil {
		rf.PowerDbm = proto.Float64(*rf.PowerDbm) // do not alias the config
	}
	return rf
}

// trackEnvironment maps the configured name to the proto enum. The empty
// string and LAND (the spec's name for it) mean GROUND.
func trackEnvironment(name string) sentinelv1.TrackEnvironment {
	name = strings.ToUpper(strings.TrimSpace(name))
	if name == "" || name == "LAND" {
		return sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_GROUND
	}
	if v, ok := sentinelv1.TrackEnvironment_value["TRACK_ENVIRONMENT_"+name]; ok {
		return sentinelv1.TrackEnvironment(v)
	}
	return sentinelv1.TrackEnvironment_TRACK_ENVIRONMENT_GROUND
}

func affiliation(name string) sentinelv1.Affiliation {
	return sentinelv1.Affiliation(sentinelv1.Affiliation_value["AFFILIATION_"+strings.ToUpper(strings.TrimSpace(name))])
}

func classification(name string) sentinelv1.Classification {
	return sentinelv1.Classification(sentinelv1.Classification_value["CLASSIFICATION_"+strings.ToUpper(strings.TrimSpace(name))])
}
