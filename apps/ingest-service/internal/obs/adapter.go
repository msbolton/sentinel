package obs

import (
	"strconv"
	"strings"
	"time"

	"github.com/sentinel/ingest-service/internal/models"
)

// Decoder names for the polled sources, whose listeners decode in place.
const (
	DecoderOpenSky   = "opensky-states"
	DecoderADSBLol   = "adsblol"
	DecoderCelesTrak = "tle-sgp4"
	DecoderJSON      = "json"
	DecoderNMEAGPS   = "nmea-gps"
	DecoderNMEAAIS   = "nmea-ais"
	DecoderCoT       = "cot"
	DecoderSBS       = "sbs"
	DecoderLink16    = "link16"
)

// DriverFor maps a listener's source type to the driver name.
func DriverFor(sourceType string) string {
	switch sourceType {
	case models.SourceOpenSky, models.SourceADSBLol, models.SourceCelesTrak:
		return sourceType
	default:
		return "stream"
	}
}

// DecoderFor picks the decoder name for a report: the polled sources decode
// in place, everything else reports what parser branch produced it.
func DecoderFor(sourceType, parserDecoder string) string {
	switch sourceType {
	case models.SourceOpenSky:
		return DecoderOpenSky
	case models.SourceADSBLol:
		return DecoderADSBLol
	case models.SourceCelesTrak:
		return DecoderCelesTrak
	}
	if parserDecoder == "" {
		return DecoderJSON
	}
	return parserDecoder
}

// SourceFor maps source type and decoder to the bare EntitySource name.
func SourceFor(sourceType, decoder string) string {
	switch sourceType {
	case models.SourceOpenSky:
		return "OPENSKY"
	case models.SourceADSBLol:
		return "ADSB_LOL"
	case models.SourceCelesTrak:
		return "CELESTRAK"
	}
	switch decoder {
	case DecoderSBS:
		return "ADS_B"
	case DecoderNMEAAIS:
		return "AIS"
	case DecoderNMEAGPS:
		return "GPS"
	case DecoderLink16:
		return "LINK16"
	}
	return "UNSPECIFIED"
}

var entityTypes = map[string]string{
	models.EntityTypeAircraft:  "AIRCRAFT",
	models.EntityTypeVessel:    "VESSEL",
	models.EntityTypeVehicle:   "VEHICLE",
	models.EntityTypePerson:    "PERSON",
	models.EntityTypeSensor:    "SENSOR",
	models.EntityTypeSatellite: "SATELLITE",
}

// EntityTypeFor maps the legacy lower-case entity type to the enum name.
func EntityTypeFor(legacy string) string {
	if t, ok := entityTypes[strings.ToLower(legacy)]; ok {
		return t
	}
	upper := strings.ToUpper(legacy)
	for _, t := range entityTypes {
		if t == upper {
			return t
		}
	}
	return "UNKNOWN"
}

func contentTypeFor(decoder string) string {
	switch decoder {
	case DecoderJSON, DecoderOpenSky, DecoderADSBLol:
		return "application/json"
	case DecoderCoT:
		return "application/xml"
	case DecoderLink16:
		return "application/octet-stream"
	default:
		return "text/plain"
	}
}

// FromEntityPosition converts a legacy normalised report into an
// observation. msg supplies provenance (receive time, raw payload); rawMax
// bounds the raw bytes kept (0 keeps none).
//
// Legacy numeric fields cannot distinguish zero from unknown, so heading,
// speed, course, altitude and the vectors are carried only when non-zero,
// and a (0, 0) position is treated as no position.
func FromEntityPosition(e *models.EntityPosition, msg *models.IngestMessage, rawMax int) *Observation {
	sourceType := msg.SourceType
	if sourceType == "" {
		sourceType = e.Source
	}
	decoder := DecoderFor(sourceType, e.Decoder)
	o := &Observation{
		FeedID:            e.FeedID,
		Driver:            DriverFor(sourceType),
		Decoder:           decoder,
		SourceEntityID:    e.EntityID,
		Source:            SourceFor(sourceType, decoder),
		SourceTimestamp:   e.Timestamp,
		ReceivedAt:        msg.ReceivedAt,
		EntityType:        EntityTypeFor(e.EntityType),
		TrackEnvironment:  strings.ToUpper(e.TrackEnvironment),
		DisplayName:       e.Name,
		CountryOfOrigin:   e.CountryOfOrigin,
		Affiliation:       strings.ToUpper(e.Affiliation),
		OperationalStatus: strings.ToUpper(e.OperationalStatus),
		AIS:               e.AISData,
		ADSB:              e.ADSBData,
		TLE:               e.TLEData,
		Link16:            e.Link16Data,
		CoT:               e.CoTData,
		UAV:               e.UAVData,
	}
	if o.FeedID == "" {
		o.FeedID = msg.FeedID
	}
	if o.ReceivedAt.IsZero() {
		o.ReceivedAt = time.Now().UTC()
	}

	if !e.NoPosition && !(e.Latitude == 0 && e.Longitude == 0) {
		o.HasPosition = true
		o.Latitude, o.Longitude = e.Latitude, e.Longitude
		if e.Altitude != 0 {
			o.Altitude = ptr(e.Altitude)
		}
	}
	if e.Heading != 0 {
		o.Heading = ptr(e.Heading)
	}
	if e.SpeedKnots != 0 {
		o.SpeedKnots = ptr(e.SpeedKnots)
	}
	if e.Course != 0 {
		o.Course = ptr(e.Course)
	}
	if e.VelocityNorth != 0 || e.VelocityEast != 0 || e.VelocityUp != 0 {
		o.Velocity = &[3]float64{e.VelocityNorth, e.VelocityEast, e.VelocityUp}
	}
	if e.AccelNorth != 0 || e.AccelEast != 0 || e.AccelUp != 0 {
		o.Acceleration = &[3]float64{e.AccelNorth, e.AccelEast, e.AccelUp}
	}
	if e.Pitch != 0 || e.Roll != 0 {
		o.Orientation = &[3]float64{e.Heading, e.Pitch, e.Roll}
	}
	if e.CircularError != 0 {
		o.CircularError = ptr(e.CircularError)
	}
	if e.PosCovariance != [6]float64{} {
		c := e.PosCovariance
		o.PosCovariance = &c
	}
	if e.PosVelCovariance != [9]float64{} {
		c := e.PosVelCovariance
		o.PosVelCovariance = &c
	}
	if e.VelCovariance != [6]float64{} {
		c := e.VelCovariance
		o.VelCovariance = &c
	}
	if e.DimensionLength != 0 || e.DimensionWidth != 0 {
		o.Dimensions = &[3]float64{e.DimensionLength, e.DimensionWidth, 0}
	}

	o.Identities = identitiesFor(e, o.FeedID)

	if rawMax > 0 && len(msg.Payload) > 0 {
		o.RawContentType = contentTypeFor(decoder)
		if len(msg.Payload) > rawMax {
			o.Raw = append([]byte(nil), msg.Payload[:rawMax]...)
			o.RawTruncated = true
		} else {
			o.Raw = append([]byte(nil), msg.Payload...)
		}
	}

	o.ID = o.ComputeID()
	return o
}

// identitiesFor derives natural identities from the platform blocks, then
// from the legacy id prefixes, and always ends with a feed-local key so
// identities[0] is canonical whenever one is known.
func identitiesFor(e *models.EntityPosition, feedID string) []Identity {
	var canonical, attributes []Identity
	add := func(t, v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		id := Identity{Type: t, Value: v}
		for _, existing := range append(canonical, attributes...) {
			if existing == id {
				return
			}
		}
		if id.Canonical() {
			canonical = append(canonical, id)
		} else {
			attributes = append(attributes, id)
		}
	}

	if e.ADSBData != nil {
		add(IdentityICAO24, strings.ToUpper(e.ADSBData.ICAOHex))
		add(IdentityCallsign, e.ADSBData.AircraftID)
		add(IdentityRegistration, strings.ToUpper(e.ADSBData.Registration))
	}
	if e.AISData != nil {
		add(IdentityMMSI, digits(e.AISData.MMSI))
		add(IdentityIMO, digits(e.AISData.IMO))
		add(IdentityCallsign, e.AISData.Callsign)
	}
	if e.TLEData != nil && e.TLEData.NoradID > 0 {
		add(IdentityNoradID, strconv.Itoa(e.TLEData.NoradID))
	}
	if e.CoTData != nil {
		add(IdentityCoTUID, e.CoTData.UID)
	}
	if e.Link16Data != nil && e.Link16Data.TrackNumber > 0 {
		add(IdentityLink16TN, strconv.Itoa(e.Link16Data.TrackNumber))
	}

	switch {
	case strings.HasPrefix(e.EntityID, "ICAO-"):
		add(IdentityICAO24, strings.ToUpper(strings.TrimPrefix(e.EntityID, "ICAO-")))
	case strings.HasPrefix(e.EntityID, "MMSI-"):
		add(IdentityMMSI, digits(strings.TrimPrefix(e.EntityID, "MMSI-")))
	case strings.HasPrefix(e.EntityID, "SAT-"):
		add(IdentityNoradID, digits(strings.TrimPrefix(e.EntityID, "SAT-")))
	}

	out := append(canonical, attributes...)
	if len(canonical) == 0 {
		out = append([]Identity{{Type: IdentityFeedLocal, Value: feedID + ":" + e.EntityID}}, out...)
	}
	return out
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func ptr(v float64) *float64 { return &v }
