// Package emitter turns emitter configuration into simulated motion, and
// builds the full emitter set for a run, synthesising any emitters the
// configuration asks for but does not list.
//
// Everything here is pure: motion advances by an explicit time step and draws
// only from a generator seeded from the run seed, so a fixed seed and step
// sequence always gives the same track.
package emitter

import (
	"fmt"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/sentinel/ew-sensor/internal/config"
	"github.com/sentinel/go-common/geo"
)

const (
	knotsToMS = 1852.0 / 3600.0

	// RandomWalkMaxTurnDegPerSec bounds how far a random-walk emitter turns
	// per second of simulated time. A step of dt seconds turns by at most
	// RandomWalkMaxTurnDegPerSec*dt in either direction.
	RandomWalkMaxTurnDegPerSec = 15.0
)

// Emitter is one simulated emitter and its current state.
type Emitter struct {
	cfg     config.Emitter
	pos     geo.LLA // altitude is always 0
	heading float64 // degrees from north, [0,360)
	speedMS float64
	kind    string
	wps     []geo.LLA
	next    int // index of the waypoint being approached
	rng     *rand.Rand
}

// New builds an emitter from its configuration. The seed drives the random
// walk (including the initial heading); static and waypoint emitters ignore
// it.
func New(c config.Emitter, seed int64) *Emitter {
	h := fnv.New64a()
	h.Write([]byte(c.ID))
	e := &Emitter{
		cfg:     c,
		pos:     geo.LLA{Lat: c.Start.Lat, Lon: c.Start.Lon},
		speedMS: c.Motion.SpeedKts * knotsToMS,
		kind:    strings.ToLower(c.Motion.Type),
		rng:     rand.New(rand.NewPCG(uint64(seed), h.Sum64())),
	}
	switch e.kind {
	case config.MotionRandomWalk:
		e.heading = e.rng.Float64() * 360
	case config.MotionWaypoints:
		for _, w := range c.Motion.Waypoints {
			e.wps = append(e.wps, geo.LLA{Lat: w.Lat, Lon: w.Lon})
		}
		if len(e.wps) == 0 {
			e.kind = config.MotionStatic
			break
		}
		e.heading = bearing(e.pos, e.wps[0])
	default:
		e.kind = config.MotionStatic
	}
	return e
}

// ID is the configured emitter id, without the "EMITTER-" prefix.
func (e *Emitter) ID() string { return e.cfg.ID }

// Config returns the emitter's configuration.
func (e *Emitter) Config() config.Emitter { return e.cfg }

// Position is the emitter's current position (altitude 0).
func (e *Emitter) Position() geo.LLA { return e.pos }

// Heading is the direction of travel in degrees from north, [0,360). It is 0
// for a static emitter.
func (e *Emitter) Heading() float64 { return e.heading }

// Advance moves the emitter forward by dt of simulated time. A non-positive dt
// does nothing.
func (e *Emitter) Advance(dt time.Duration) {
	if dt <= 0 {
		return
	}
	switch e.kind {
	case config.MotionRandomWalk:
		turn := RandomWalkMaxTurnDegPerSec * dt.Seconds()
		e.heading = normalize(e.heading + (e.rng.Float64()*2-1)*turn)
		p := geo.AERToLLA(e.pos, e.heading, 0, e.speedMS*dt.Seconds())
		e.pos = geo.LLA{Lat: p.Lat, Lon: p.Lon}
	case config.MotionWaypoints:
		e.advanceWaypoints(e.speedMS * dt.Seconds())
	}
}

// advanceWaypoints travels dist metres along the looping path, carrying any
// distance left over at a waypoint on to the next leg.
func (e *Emitter) advanceWaypoints(dist float64) {
	// One pass round the loop is enough to reach the point of diminishing
	// returns; the bound also stops a path of coincident waypoints spinning.
	for i := 0; i <= len(e.wps) && dist > 0; i++ {
		target := e.wps[e.next]
		az, el, r := geo.LLAToAER(e.pos, target)
		if r > 0 {
			e.heading = az
		}
		if r > dist {
			p := geo.AERToLLA(e.pos, az, el, dist)
			e.pos = geo.LLA{Lat: p.Lat, Lon: p.Lon}
			return
		}
		e.pos = target
		dist -= r
		e.next = (e.next + 1) % len(e.wps)
	}
}

func bearing(from, to geo.LLA) float64 {
	az, _, _ := geo.LLAToAER(from, to)
	return az
}

func normalize(deg float64) float64 {
	d := math.Mod(deg, 360)
	if d < 0 {
		d += 360
	}
	if d >= 360 { // -1e-17 rounds up to exactly 360
		d = 0
	}
	return d
}

// Build returns the emitters for a run: the configured list, cut or extended
// to cfg.EmitterCount. Extra emitters are synthesised from cfg.Seed with ids
// SYN-01, SYN-02, ..., placed inside the sensor's coverage; the first k of
// them do not depend on the total.
func Build(cfg config.Config) []*Emitter {
	list := cfg.Emitters
	if cfg.EmitterCount < len(list) {
		list = list[:max(cfg.EmitterCount, 0)]
	}
	out := make([]*Emitter, 0, max(cfg.EmitterCount, len(list)))
	for _, c := range list {
		out = append(out, New(c, cfg.Seed))
	}
	for i := 0; len(out) < cfg.EmitterCount; i++ {
		out = append(out, New(synthesize(cfg, i), cfg.Seed))
	}
	return out
}

// template is one of the emitter kinds used in the example configurations.
type template struct {
	emitterType, modulation, scanType, environment, affiliation string
	freqHz, bandwidthHz, priUs, pulseWidthUs, powerDbm          float64
	motion                                                      string
	speedKts                                                    float64
}

var templates = []template{
	{"RADAR_SEARCH", "PULSE", "CIRCULAR", "SEA_SURFACE", "UNKNOWN", 9.41e9, 20e6, 1000, 0.5, -62, config.MotionRandomWalk, 14},
	{"RADAR_TRACK", "PULSE", "FIXED", "SEA_SURFACE", "HOSTILE", 10.2e9, 50e6, 250, 0.2, -55, config.MotionRandomWalk, 6},
	{"SATCOM", "OFDM", "FIXED", "SEA_SURFACE", "UNKNOWN", 7.95e9, 25e6, 0, 0, -78, config.MotionStatic, 0},
	{"COMMS_DATALINK", "FHSS", "FIXED", "AIR", "HOSTILE", 1.13e9, 3e6, 0, 0, -70, config.MotionWaypoints, 240},
	{"JAMMER", "CHIRP", "FIXED", "SEA_SURFACE", "HOSTILE", 9.4e9, 400e6, 0, 0, -45, config.MotionStatic, 0},
}

// synthesize makes the i-th synthetic emitter. Its draws come from a generator
// keyed on (seed, i) alone, so it is independent of how many others exist.
func synthesize(cfg config.Config, i int) config.Emitter {
	rng := rand.New(rand.NewPCG(uint64(cfg.Seed), 0x53594e0000000000+uint64(i)))
	t := templates[(i+int(uint64(cfg.Seed)%uint64(len(templates))))%len(templates)]

	origin := geo.LLA{Lat: cfg.Emplacement.Lat, Lon: cfg.Emplacement.Lon, Alt: cfg.Emplacement.Alt}
	place := func() config.LatLon {
		e := cfg.Emplacement
		half := math.Min(e.Width, 360) / 2
		az := e.Boresight + (rng.Float64()*2-1)*half*0.9
		minR, maxR := e.MinRangeKm*1000, e.RangeKm*1000
		r := minR + (maxR-minR)*(0.2+0.65*rng.Float64())
		p := geo.AERToLLA(origin, az, 0, r)
		return config.LatLon{Lat: p.Lat, Lon: p.Lon}
	}

	c := config.Emitter{
		ID:               fmt.Sprintf("SYN-%02d", i+1),
		DisplayName:      fmt.Sprintf("Synthetic %s %02d", strings.ToLower(strings.ReplaceAll(t.emitterType, "_", " ")), i+1),
		EmitterType:      t.emitterType,
		FrequencyHz:      t.freqHz,
		BandwidthHz:      t.bandwidthHz,
		PriUs:            t.priUs,
		PulseWidthUs:     t.pulseWidthUs,
		Modulation:       t.modulation,
		ScanType:         t.scanType,
		Affiliation:      t.affiliation,
		TrackEnvironment: t.environment,
		Start:            place(),
		Motion:           config.Motion{Type: t.motion, SpeedKts: t.speedKts},
	}
	pw := t.powerDbm
	c.PowerDbm = &pw
	if t.motion == config.MotionWaypoints {
		c.Motion.Waypoints = []config.LatLon{place(), place(), place()}
	}
	return c
}
