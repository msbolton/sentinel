package emplacement

import (
	"math"
	"slices"
	"strings"

	"github.com/sentinel/go-common/classification"
)

// maxRangeM bounds a coverage's reach (2000 km, beyond any ground sensor).
const maxRangeM = 2_000_000

// maxHeightAboveGroundM bounds a sensor's mounting height (1 km: a tall tower
// is a few hundred metres; more is a typo, or an altitude in the wrong field).
const maxHeightAboveGroundM = 1000

// Problem is one invalid field, as a JSON pointer and a message.
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ValidationError carries every problem found; the API answers 422 with them.
type ValidationError struct{ Problems []Problem }

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Problems))
	for _, p := range e.Problems {
		parts = append(parts, p.Path+" "+p.Message)
	}
	return "invalid emplacement: " + strings.Join(parts, "; ")
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// Validate checks e as it would be stored, the whole of it, so a PATCH that
// is valid field by field but leaves the emplacement invalid is refused. It
// normalises the boresight into [0, 360) and the classification's spelling.
func Validate(e *Emplacement) error {
	var problems []Problem
	add := func(path, msg string) { problems = append(problems, Problem{Path: path, Message: msg}) }

	switch {
	case e.Name == "":
		add("/name", "is required")
	case len(e.Name) > 255:
		add("/name", "must be at most 255 characters")
	}
	if e.Mobility != MobilityFixed && e.Mobility != MobilityMobile {
		add("/mobility", "must be FIXED or MOBILE")
	}
	if !slices.Contains(SensorTypes, e.SensorType) {
		add("/sensorType", "must be one of "+strings.Join(SensorTypes, ", "))
	}
	if e.AzimuthReference != AzimuthTrueNorth && e.AzimuthReference != AzimuthPlatform {
		add("/azimuthReference", "must be TRUE_NORTH or PLATFORM")
	}
	if e.Position == nil {
		if e.Mobility == MobilityFixed {
			add("/position", "is required for a FIXED emplacement")
		}
	} else {
		if !finite(e.Position.Lat) || e.Position.Lat < -90 || e.Position.Lat > 90 {
			add("/position/lat", "must be between -90 and 90")
		}
		if !finite(e.Position.Lon) || e.Position.Lon < -180 || e.Position.Lon > 180 {
			add("/position/lon", "must be between -180 and 180")
		}
	}
	if e.Altitude != nil && !finite(*e.Altitude) {
		add("/altitude", "must be a number")
	}
	if e.HeightAboveGroundM != nil && (!finite(*e.HeightAboveGroundM) || *e.HeightAboveGroundM < 0 || *e.HeightAboveGroundM > maxHeightAboveGroundM) {
		add("/heightAboveGroundM", "must be between 0 and 1000 (metres above the ground)")
	}
	if finite(e.BoresightDeg) {
		e.BoresightDeg = math.Mod(math.Mod(e.BoresightDeg, 360)+360, 360)
	} else {
		add("/boresightDeg", "must be a number")
	}

	if c := e.Coverage; !c.Empty() {
		if !finite(c.MinRangeM) || !finite(c.MaxRangeM) || c.MinRangeM < 0 || c.MinRangeM >= c.MaxRangeM || c.MaxRangeM > maxRangeM {
			add("/coverage", "needs 0 ≤ minRangeM < maxRangeM ≤ 2000000")
		}
		if !finite(c.AzimuthWidthDeg) || c.AzimuthWidthDeg <= 0 || c.AzimuthWidthDeg > 360 {
			add("/coverage/azimuthWidthDeg", "must be more than 0 and at most 360")
		}
		elevation := func(path string, v *float64) {
			if v != nil && (!finite(*v) || *v < -90 || *v > 90) {
				add(path, "must be between -90 and 90")
			}
		}
		elevation("/coverage/elevationMinDeg", c.ElevationMinDeg)
		elevation("/coverage/elevationMaxDeg", c.ElevationMaxDeg)
		if c.ElevationMinDeg != nil && c.ElevationMaxDeg != nil && *c.ElevationMinDeg >= *c.ElevationMaxDeg {
			add("/coverage", "elevationMinDeg must be below elevationMaxDeg")
		}
	}

	sigma := func(path string, v *float64) {
		if v != nil && (!finite(*v) || *v <= 0) {
			add(path, "must be more than 0")
		}
	}
	sigma("/defaultSigma/rangeM", e.DefaultSigma.RangeM)
	sigma("/defaultSigma/azimuthDeg", e.DefaultSigma.AzimuthDeg)
	sigma("/defaultSigma/elevationDeg", e.DefaultSigma.ElevationDeg)

	if classification.Valid(e.Classification) {
		e.Classification = classification.Normalize(e.Classification)
	} else {
		add("/classification", "must be UNCLASSIFIED, CONFIDENTIAL, SECRET or TOP_SECRET")
	}

	if e.Active && e.FeedID == nil {
		add("/feedId", "is required for an active emplacement: bind it to a connection, or deactivate it")
	}

	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}
