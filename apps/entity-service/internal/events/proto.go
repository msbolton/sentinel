// Package events publishes the entity service's typed EntityEvent stream
// and, while older consumers remain, the legacy JSON events.
package events

import (
	"encoding/json"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sentinel/proto-gen/pbjson"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/entity-service/internal/store"
)

// pgTimeLayouts are the text forms Postgres renders timestamptz in, plus
// RFC 3339 for values that already went through Go.
var pgTimeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999-07",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05-07",
	"2006-01-02 15:04:05.999999999",
}

// ParseTime reads a timestamp as the store renders it. ok is false for an
// empty or unparseable value.
func ParseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range pgTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func ts(s string) *timestamppb.Timestamp {
	if t, ok := ParseTime(s); ok {
		return timestamppb.New(t)
	}
	return nil
}

func tsp(s *string) *timestamppb.Timestamp {
	if s == nil {
		return nil
	}
	return ts(*s)
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// EntityProto builds the wire snapshot of an entity row. identities and
// sources are what the caller knows about the entity; nil is fine.
func EntityProto(rec *store.EntityRecord, identities []*sentinelv1.Identity, sources []*sentinelv1.SourceContribution) *sentinelv1.Entity {
	e := &sentinelv1.Entity{
		Id:                    rec.ID,
		EntityType:            parseEnum[sentinelv1.EntityType](rec.EntityType),
		Name:                  rec.Name,
		Description:           str(rec.Description),
		Source:                parseEnum[sentinelv1.EntitySource](rec.Source),
		Classification:        parseEnum[sentinelv1.Classification](rec.Classification),
		Exercise:              rec.Exercise,
		Heading:               rec.Heading,
		SpeedKnots:            rec.SpeedKnots,
		Course:                rec.Course,
		Affiliations:          rec.Affiliations,
		MilStd_2525DSymbol:    str(rec.MilStd2525dSymbol),
		CreatedAt:             ts(rec.CreatedAt),
		UpdatedAt:             ts(rec.UpdatedAt),
		LastSeenAt:            tsp(rec.LastSeenAt),
		Affiliation:           parseEnum[sentinelv1.Affiliation](str(rec.Affiliation)),
		Characterization:      parseEnum[sentinelv1.CharacterizationState](str(rec.Characterization)),
		TrackEnvironment:      parseEnum[sentinelv1.TrackEnvironment](str(rec.TrackEnvironment)),
		TrackProcessingState:  parseEnum[sentinelv1.TrackProcessingState](str(rec.TrackProcessingState)),
		OperationalStatus:     parseEnum[sentinelv1.OperationalStatus](str(rec.OperationalStatus)),
		DamageAssessment:      parseEnum[sentinelv1.DamageAssessment](str(rec.DamageAssessment)),
		CountryOfOrigin:       str(rec.CountryOfOrigin),
		SourceEntityId:        str(rec.SourceEntityID),
		CircularError:         rec.CircularError,
		LastObservationSource: str(rec.LastObservationSource),
		FeedId:                str(rec.FeedID),
		LastFeedId:            str(rec.LastFeedID),
		AgeoutState:           parseEnum[sentinelv1.AgeoutState](rec.AgeoutState),
		Identities:            identities,
		Sources:               sources,
		LastObservedAt:        tsp(rec.LastObservedAt),
	}
	if e.Affiliations == nil {
		e.Affiliations = []string{}
	}
	if rec.IdentityConfidence != nil {
		e.IdentityConfidence = int32(*rec.IdentityConfidence)
	}
	if rec.DamageConfidence != nil {
		e.DamageConfidence = int32(*rec.DamageConfidence)
	}
	if rec.Position != nil {
		e.Position = &sentinelv1.Coordinate{Latitude: rec.Position.Lat, Longitude: rec.Position.Lon, AltitudeMeters: rec.Altitude}
	}
	if len(rec.Metadata) > 0 {
		e.Metadata = map[string]string{}
		for k, v := range rec.Metadata {
			switch x := v.(type) {
			case string:
				e.Metadata[k] = x
			default:
				b, _ := json.Marshal(x)
				e.Metadata[k] = string(b)
			}
		}
	}
	if rec.DimensionLength != nil || rec.DimensionWidth != nil || rec.DimensionHeight != nil {
		e.Dimensions = &sentinelv1.Dimensions{Length: deref(rec.DimensionLength), Width: deref(rec.DimensionWidth), Height: deref(rec.DimensionHeight)}
	}
	if len(rec.Kinematics) > 0 {
		e.Kinematics = kinematicsProto(rec.Kinematics)
	}
	if len(rec.PlatformData) > 0 {
		e.Platforms = platformsProto(rec.PlatformData)
	}
	return e
}

func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func parseEnum[E interface {
	~int32
	Descriptor() protoreflect.EnumDescriptor
}](name string) E {
	v, _ := pbjson.ParseEnum[E](name)
	return v
}

// kinematicsProto reads the kinematics JSONB ({"velocity": {north, east,
// up}, "acceleration": {...}, "positionCovariance": {pnPn, pnPe, pnPu,
// pePe, pePu, puPu}}).
func kinematicsProto(k map[string]interface{}) *sentinelv1.KinematicState {
	vec := func(v interface{}) (float64, float64, float64, bool) {
		m, ok := v.(map[string]interface{})
		if !ok {
			return 0, 0, 0, false
		}
		f := func(key string) float64 {
			if x, ok := m[key].(float64); ok {
				return x
			}
			return 0
		}
		return f("north"), f("east"), f("up"), true
	}
	out := &sentinelv1.KinematicState{}
	if n, e, u, ok := vec(k["velocity"]); ok {
		out.Velocity = &sentinelv1.GeodeticVelocity{North: n, East: e, Up: u}
	}
	if n, e, u, ok := vec(k["acceleration"]); ok {
		out.Acceleration = &sentinelv1.GeodeticAcceleration{North: n, East: e, Up: u}
	}
	if m, ok := k["positionCovariance"].(map[string]interface{}); ok {
		f := func(key string) float64 {
			if x, ok := m[key].(float64); ok {
				return x
			}
			return 0
		}
		out.PositionCovariance = &sentinelv1.PositionCovariance{
			PnPn: f("pnPn"), PnPe: f("pnPe"), PnPu: f("pnPu"),
			PePe: f("pePe"), PePu: f("pePu"), PuPu: f("puPu"),
		}
	}
	if out.Velocity == nil && out.Acceleration == nil && out.PositionCovariance == nil {
		return nil
	}
	return out
}

// platformsProto converts the platformData JSONB, keyed by detail kind,
// into typed detail blocks. Both the legacy snake_case keys and protojson's
// camelCase are accepted.
func platformsProto(pd map[string]interface{}) map[string]*sentinelv1.PlatformDetail {
	out := map[string]*sentinelv1.PlatformDetail{}
	for kind, raw := range pd {
		if raw == nil {
			continue
		}
		b, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		detail := &sentinelv1.PlatformDetail{}
		var inner proto.Message
		switch kind {
		case "ais":
			d := &sentinelv1.AisDetail{}
			detail.Detail = &sentinelv1.PlatformDetail_Ais{Ais: d}
			inner = d
		case "adsb":
			d := &sentinelv1.AdsbDetail{}
			detail.Detail = &sentinelv1.PlatformDetail_Adsb{Adsb: d}
			inner = d
		case "tle":
			d := &sentinelv1.TleDetail{}
			detail.Detail = &sentinelv1.PlatformDetail_Tle{Tle: d}
			inner = d
		case "link16":
			d := &sentinelv1.Link16Detail{}
			detail.Detail = &sentinelv1.PlatformDetail_Link16{Link16: d}
			inner = d
		case "cot":
			d := &sentinelv1.CotDetail{}
			detail.Detail = &sentinelv1.PlatformDetail_Cot{Cot: d}
			inner = d
		case "uav":
			d := &sentinelv1.UavDetail{}
			detail.Detail = &sentinelv1.PlatformDetail_Uav{Uav: d}
			inner = d
		case "rf":
			d := &sentinelv1.RfDetail{}
			detail.Detail = &sentinelv1.PlatformDetail_Rf{Rf: d}
			inner = d
		default:
			continue
		}
		if err := pbjson.Unmarshal(b, inner); err != nil {
			continue
		}
		out[kind] = detail
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
