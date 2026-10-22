// Package ownship computes the pose of the sensor platform over time. The pose
// is a pure function of elapsed time since the run began, so it can be tested
// and replayed without a clock.
package ownship

import (
	"math"
	"time"

	"github.com/sentinel/go-common/geo"
	"github.com/sentinel/sensor-sim/internal/config"
)

const (
	knotsToMS = 1852.0 / 3600.0

	// DefaultCircleRadiusM is used when a circle path has no radius.
	DefaultCircleRadiusM = 5000.0
)

// Pose is where the platform is and how it moves.
type Pose struct {
	Lat, Lon, Alt float64 // degrees, degrees, metres
	HeadingDeg    float64 // degrees from north, [0,360)
	VelNorth      float64 // m/s
	VelEast       float64 // m/s
	VelUp         float64 // m/s
}

// Position is the pose's geodetic position.
func (p Pose) Position() geo.LLA { return geo.LLA{Lat: p.Lat, Lon: p.Lon, Alt: p.Alt} }

// Ownship follows the configured path.
type Ownship struct {
	mobile    bool
	lat, lon  float64
	alt       float64
	boresight float64
	speedMS   float64
	path      string

	// circle
	radiusM float64

	// waypoints: leg i runs wps[i] -> wps[(i+1)%n]
	wps    []geo.LLA
	legAz  []float64
	legEl  []float64
	legLen []float64
	total  float64
}

// New builds the ownship for cfg. When the sensor is not MOBILE the pose is
// the fixed sensor position.
func New(cfg config.Config) *Ownship {
	e, o := cfg.Sensor, cfg.Ownship
	s := &Ownship{
		mobile:    cfg.Mobile(),
		lat:       e.Lat,
		lon:       e.Lon,
		alt:       e.AltM(),
		boresight: e.Boresight,
		speedMS:   o.SpeedKts * knotsToMS,
		path:      o.Path,
	}
	if !s.mobile || s.speedMS <= 0 {
		s.mobile = false
		return s
	}
	switch o.Path {
	case config.PathWaypoints:
		for _, w := range o.Waypoints {
			s.wps = append(s.wps, geo.LLA{Lat: w.Lat, Lon: w.Lon})
		}
		n := len(s.wps)
		for i := range s.wps {
			az, el, r := geo.LLAToAER(s.wps[i], s.wps[(i+1)%n])
			s.legAz = append(s.legAz, az)
			s.legEl = append(s.legEl, el)
			s.legLen = append(s.legLen, r)
			s.total += r
		}
		if n < 2 || s.total <= 0 {
			s.mobile = false
		} else {
			s.lat, s.lon = s.wps[0].Lat, s.wps[0].Lon
		}
	default: // circle
		s.path = config.PathCircle
		s.radiusM = o.RadiusKm * 1000
		if !(s.radiusM > 0) {
			s.radiusM = DefaultCircleRadiusM
		}
	}
	return s
}

// Pose returns the platform pose elapsed after the start of the run.
func (s *Ownship) Pose(elapsed time.Duration) Pose {
	if !s.mobile {
		return Pose{Lat: s.lat, Lon: s.lon, Alt: s.alt, HeadingDeg: normalize(s.boresight)}
	}
	if elapsed < 0 {
		elapsed = 0
	}
	if s.path == config.PathWaypoints {
		return s.waypointPose(elapsed)
	}
	return s.circlePose(elapsed)
}

// circlePose goes clockwise round the emplacement, starting due north of it.
func (s *Ownship) circlePose(elapsed time.Duration) Pose {
	// Reduce the arc modulo the circumference first so long runs keep precision.
	arc := math.Mod(s.speedMS*elapsed.Seconds(), 2*math.Pi*s.radiusM)
	theta := arc / s.radiusM * 180 / math.Pi // position azimuth from the centre
	p := geo.AERToLLA(geo.LLA{Lat: s.lat, Lon: s.lon}, theta, 0, s.radiusM)
	return s.pose(p.Lat, p.Lon, theta+90)
}

func (s *Ownship) waypointPose(elapsed time.Duration) Pose {
	d := math.Mod(s.speedMS*elapsed.Seconds(), s.total)
	for i, l := range s.legLen {
		if d < l || i == len(s.legLen)-1 {
			if l == 0 {
				continue
			}
			p := geo.AERToLLA(s.wps[i], s.legAz[i], s.legEl[i], math.Min(d, l))
			return s.pose(p.Lat, p.Lon, s.legAz[i])
		}
		d -= l
	}
	return s.pose(s.wps[0].Lat, s.wps[0].Lon, s.legAz[0])
}

// pose builds a Pose moving at the platform speed on the given heading.
func (s *Ownship) pose(lat, lon, heading float64) Pose {
	h := normalize(heading)
	sin, cos := math.Sincos(h * math.Pi / 180)
	return Pose{
		Lat: lat, Lon: lon, Alt: s.alt, HeadingDeg: h,
		VelNorth: s.speedMS * cos, VelEast: s.speedMS * sin,
	}
}

func normalize(deg float64) float64 {
	d := math.Mod(deg, 360)
	if d < 0 {
		d += 360
	}
	if d >= 360 {
		d = 0
	}
	return d
}
