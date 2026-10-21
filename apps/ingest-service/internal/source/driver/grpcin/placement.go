package grpcin

import (
	"context"
	"time"

	"github.com/sentinel/go-common/geo"
	"github.com/sentinel/ingest-service/internal/emplacement"
	"github.com/sentinel/ingest-service/internal/obs"
)

// Why a direction-only detection became a bearing instead of a position.
// The report is accepted either way.
const (
	reasonAirTarget        = "air_target"
	reasonNoGround         = "no_ground_intersection"
	reasonTerrainUnavail   = "terrain_unavailable"
	reasonNoOriginAltitude = "no_origin_altitude"
)

// defaultDirectionSigmaDeg is the pointing error assumed on an axis when
// neither the measurement nor the emplacement gives one.
const defaultDirectionSigmaDeg = 0.5

// bearing is a direction-only detection that could not be placed: a line
// from the emplacement, LengthM long, recorded on the site instead of an
// observation.
type bearing struct {
	TrackID        string    `json:"trackId,omitempty"`
	AzimuthDeg     float64   `json:"azimuthDeg"` // true north, [0,360)
	ElevationDeg   float64   `json:"elevationDeg"`
	SigmaAzDeg     float64   `json:"sigmaAzDeg,omitempty"`
	SigmaElDeg     float64   `json:"sigmaElDeg,omitempty"`
	LengthM        float64   `json:"lengthM"`
	Reason         string    `json:"reason"`
	Classification string    `json:"classification,omitempty"`
	Confidence     *float64  `json:"confidence,omitempty"`
	Band           string    `json:"band,omitempty"`
	ObservedAt     time.Time `json:"observedAt"`
}

// isAirborne reports a target said to be in the air, whose direction meets
// the ground only behind it.
func isAirborne(o *obs.Observation) bool {
	return o.TrackEnvironment == obs.EnvAir || o.EntityType == "DRONE" || o.EntityType == "AIRCRAFT"
}

// isGroundTarget reports a target said to be on the ground: by its
// environment, or with none by its radar classification.
func isGroundTarget(o *obs.Observation) bool {
	if o.TrackEnvironment == obs.EnvGround {
		return true
	}
	if o.TrackEnvironment != "" || o.Radar == nil {
		return false
	}
	c := o.Radar.Classification
	return c == "VEHICLE" || c == "PERSON"
}

// placeDirectionOnly places o on terrain, or returns a bearing to record instead.
//
// It answers one of three ways. Placed: b is nil and reason "", and o has
// the ground point as its position and altitude, a covariance and a
// circular error. A bearing: b is set, reason is b.Reason, and o is left as
// it was. Refused: b is nil and reason is the reject reason (bad_polar).
// All the terrain calls together wait at most the driver's terrainWait.
func (s *service) placeDirectionOnly(ctx context.Context, o *obs.Observation, e emplacement.Emplacement, org origin) (b *bearing, reason string) {
	b, reason, _ = s.place(ctx, o, e, org)
	return b, reason
}

// place is placeDirectionOnly, also returning the origin with the altitude
// it was resolved to, for the coverage check.
func (s *service) place(ctx context.Context, o *obs.Observation, e emplacement.Emplacement, org origin) (*bearing, string, origin) {
	p := o.Polar
	if p == nil || p.ElevationDeg == nil {
		return nil, "bad_polar", org
	}
	ref := p.AzimuthRef
	if ref == "" {
		ref = e.AzimuthReference
	}
	az, ok := trueAzimuth(p.AzimuthDeg, ref, org.heading)
	if !ok {
		return nil, "bad_polar", org
	}
	el := *p.ElevationDeg
	maxRange := s.d.maxRangeM
	if e.Coverage.MaxRangeM > 0 {
		maxRange = e.Coverage.MaxRangeM
	}
	sigAz := sigmaOr(p.SigmaAzimuthDeg, e.DefaultSigma.AzimuthDeg)
	sigEl := sigmaOr(p.SigmaElevationDeg, e.DefaultSigma.ElevationDeg)
	out := func(why string, length float64) (*bearing, string, origin) {
		return newBearing(o, az, el, sigAz, sigEl, length, why), why, org
	}

	// One wait for the whole detection, however many calls it takes.
	tctx, cancel := context.WithTimeout(ctx, s.d.terrainWait)
	defer cancel()
	terr := s.d.terrain

	// The origin's altitude: as given, else the ground under the site plus
	// the mounting height.
	why := ""
	if !org.hasAlt {
		switch {
		case e.HeightAboveGroundM == nil:
			why = reasonNoOriginAltitude
		case terr == nil:
			why = reasonTerrainUnavail
		default:
			g, err := terr.HeightHAE(tctx, org.at.Lat, org.at.Lon)
			if err != nil || !finite(g) {
				why = reasonTerrainUnavail
			} else {
				org.at.Alt, org.hasAlt = g+*e.HeightAboveGroundM, true
			}
		}
	}

	// An air target is somewhere along the line, short of where it meets
	// the ground, so it is never placed on the ground.
	if isAirborne(o) {
		length := maxRange
		if why == "" && terr != nil {
			if hit, err := terr.Intersect(tctx, org.at, az, el, maxRange); err == nil && hit.Found {
				length = hit.RangeM
			}
		}
		return out(reasonAirTarget, length)
	}
	if why != "" {
		return out(why, maxRange)
	}
	if terr == nil {
		return out(reasonTerrainUnavail, maxRange)
	}
	hit, err := terr.Intersect(tctx, org.at, az, el, maxRange)
	if err != nil {
		return out(reasonTerrainUnavail, maxRange)
	}
	if !hit.Found {
		return out(reasonNoGround, maxRange)
	}
	// Without an honest covariance the point is not placed at all.
	cov, ok := terr.DirectionCovariance(tctx, org.at, az, el, maxRange, sigAz, sigEl, hit)
	if !ok {
		return out(reasonTerrainUnavail, maxRange)
	}

	o.SetPosition(hit.At.Lat, hit.At.Lon)
	o.Altitude = obs.Float(hit.At.Alt)
	if o.TrackEnvironment == "" {
		o.TrackEnvironment = obs.EnvGround
	}
	o.PosCovariance = &cov
	if o.CircularError == nil || *o.CircularError <= 0 {
		if ce := geo.CEP95(cov[0], cov[1], cov[3]); ce > 0 && finite(ce) {
			o.CircularError = &ce
		}
	}
	return nil, "", org
}

// sigmaOr is the measurement's sigma, else the emplacement's, else the
// default.
func sigmaOr(sent, stored *float64) float64 {
	switch {
	case sent != nil:
		return *sent
	case stored != nil:
		return *stored
	}
	return defaultDirectionSigmaDeg
}

// newBearing is o's direction as a bearing, with what the camera said of it.
func newBearing(o *obs.Observation, az, el, sigAz, sigEl, length float64, reason string) *bearing {
	b := &bearing{AzimuthDeg: az, ElevationDeg: el, SigmaAzDeg: sigAz, SigmaElDeg: sigEl, LengthM: length, Reason: reason, ObservedAt: o.SourceTimestamp}
	if b.ObservedAt.IsZero() {
		b.ObservedAt = o.ReceivedAt
	}
	if c := o.EoIr; c != nil {
		b.TrackID, b.Classification, b.Band = c.CameraTrackID, c.Classification, c.Band
		if c.ClassificationConfidence != nil {
			v := *c.ClassificationConfidence
			b.Confidence = &v
		}
	}
	return b
}

// applyRadarTerrain clamps ground targets and sets Radar.HeightAboveGroundM for air ones.
//
// o has been placed from its range-polar measurement. The height above
// ground is the service's own figure: whatever the sender put there is
// cleared first, and set again only for an air target the terrain answered
// for. A ground target is put on the ground. When the terrain cannot answer
// (off, unavailable, or slower than terrainWait) the measured altitude is
// kept and terrainClampSkipped counts it.
func (s *service) applyRadarTerrain(ctx context.Context, o *obs.Observation) {
	if o.Radar == nil {
		return
	}
	o.Radar.HeightAboveGroundM = nil
	air := isAirborne(o)
	if (!air && !isGroundTarget(o)) || !o.HasPosition || o.Altitude == nil {
		return
	}
	if s.d.terrain == nil {
		s.d.terrainClampSkipped.Add(1)
		return
	}
	tctx, cancel := context.WithTimeout(ctx, s.d.terrainWait)
	defer cancel()
	g, err := s.d.terrain.HeightHAE(tctx, o.Latitude, o.Longitude)
	if err != nil || !finite(g) {
		s.d.terrainClampSkipped.Add(1)
		return
	}
	if air {
		o.Radar.HeightAboveGroundM = obs.Float(*o.Altitude - g)
		return
	}
	o.Altitude = obs.Float(g)
}
