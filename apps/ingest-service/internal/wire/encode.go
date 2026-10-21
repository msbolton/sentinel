// Package wire is the single place the ingest service touches generated
// protobuf code: it turns an obs.Observation into the sentinel.v1.Observation
// bytes, Kafka key and headers.
package wire

import (
	"maps"
	"strconv"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonkafka "github.com/sentinel/go-common/kafka"
	"github.com/sentinel/proto-gen/pbjson"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/ingest-service/internal/obs"
)

// Producer is the producer header value.
const Producer = "ingest-service"

// ToProto builds the protobuf message.
func ToProto(o *obs.Observation) *sentinelv1.Observation {
	m := &sentinelv1.Observation{
		ObservationId:     o.ID,
		FeedId:            o.FeedID,
		Driver:            o.Driver,
		Decoder:           o.Decoder,
		SourceEntityId:    o.SourceEntityID,
		Source:            parse[sentinelv1.EntitySource](o.Source),
		SensorId:          o.SensorID,
		EntityType:        parse[sentinelv1.EntityType](o.EntityType),
		TrackEnvironment:  parse[sentinelv1.TrackEnvironment](o.TrackEnvironment),
		DisplayName:       o.DisplayName,
		CountryOfOrigin:   o.CountryOfOrigin,
		Affiliation:       parse[sentinelv1.Affiliation](o.Affiliation),
		OperationalStatus: parse[sentinelv1.OperationalStatus](o.OperationalStatus),
		Classification:    parse[sentinelv1.Classification](o.Classification),
		ReplicaId:         o.ReplicaID,
		FencingToken:      o.FencingToken,
		Heading:           o.Heading,
		SpeedKnots:        o.SpeedKnots,
		Course:            o.Course,
	}
	if !o.SourceTimestamp.IsZero() {
		m.SourceTimestamp = timestamppb.New(o.SourceTimestamp)
	}
	if !o.ReceivedAt.IsZero() {
		m.ReceivedAt = timestamppb.New(o.ReceivedAt)
	}
	for _, id := range o.Identities {
		m.Identities = append(m.Identities, &sentinelv1.Identity{Type: parse[sentinelv1.IdentityType](id.Type), Value: id.Value})
	}
	if o.HasPosition {
		m.Position = &sentinelv1.Coordinate{Latitude: o.Latitude, Longitude: o.Longitude, AltitudeMeters: o.Altitude}
	}
	if v := o.Velocity; v != nil {
		m.Velocity = &sentinelv1.GeodeticVelocity{North: v[0], East: v[1], Up: v[2]}
	}
	if a := o.Acceleration; a != nil {
		m.Acceleration = &sentinelv1.GeodeticAcceleration{North: a[0], East: a[1], Up: a[2]}
	}
	if r := o.Orientation; r != nil {
		m.Orientation = &sentinelv1.Orientation{Yaw: r[0], Pitch: r[1], Roll: r[2]}
	}
	if o.CircularError != nil {
		m.Uncertainty = &sentinelv1.MeasurementUncertainty{CircularError: *o.CircularError}
	}
	if c := o.PosCovariance; c != nil {
		m.PositionCovariance = &sentinelv1.PositionCovariance{PnPn: c[0], PnPe: c[1], PnPu: c[2], PePe: c[3], PePu: c[4], PuPu: c[5]}
	}
	if c := o.PosVelCovariance; c != nil {
		m.PositionVelocityCovariance = &sentinelv1.PositionVelocityCovariance{PnVn: c[0], PnVe: c[1], PnVu: c[2], PeVn: c[3], PeVe: c[4], PeVu: c[5], PuVn: c[6], PuVe: c[7], PuVu: c[8]}
	}
	if c := o.VelCovariance; c != nil {
		m.VelocityCovariance = &sentinelv1.VelocityCovariance{VnVn: c[0], VnVe: c[1], VnVu: c[2], VeVe: c[3], VeVu: c[4], VuVu: c[5]}
	}
	if d := o.Dimensions; d != nil {
		m.Dimensions = &sentinelv1.Dimensions{Length: d[0], Width: d[1], Height: d[2]}
	}
	m.EmplacementId, m.Exercise = o.EmplacementID, o.Exercise
	if p := o.Polar; p != nil {
		m.Polar = &sentinelv1.PolarMeasurement{
			RangeM: p.RangeM, AzimuthDeg: p.AzimuthDeg, ElevationDeg: p.ElevationDeg, RangeRateMps: p.RangeRateMps,
			AzimuthReference: parse[sentinelv1.AzimuthReference](p.AzimuthRef),
		}
		if p.SigmaRangeM != nil || p.SigmaAzimuthDeg != nil || p.SigmaElevationDeg != nil {
			m.Polar.Sigma = &sentinelv1.PolarSigma{RangeM: p.SigmaRangeM, AzimuthDeg: p.SigmaAzimuthDeg, ElevationDeg: p.SigmaElevationDeg}
		}
	}
	m.Platform = platform(o)
	if len(o.Raw) > 0 {
		m.Raw = &sentinelv1.RawPayload{ContentType: o.RawContentType, Data: o.Raw, Truncated: o.RawTruncated}
	}
	return m
}

// Marshal serialises the observation.
func Marshal(o *obs.Observation) ([]byte, error) {
	return proto.Marshal(ToProto(o))
}

// Key is the Kafka key: the canonical identity, else the feed-local key.
func Key(o *obs.Observation) string {
	if id, ok := o.CanonicalIdentity(); ok {
		return commonkafka.ObservationKey(id.Type, id.Value, o.FeedID, o.SourceEntityID)
	}
	return commonkafka.ObservationKey("", "", o.FeedID, o.SourceEntityID)
}

// Headers are the standard protobuf headers plus the feed id and, when
// the feed runs under a lease, the fencing token.
func Headers(o *obs.Observation) map[string]string {
	extra := map[string]string{commonkafka.HeaderFeedID: o.FeedID}
	if o.FencingToken > 0 {
		extra[commonkafka.HeaderFence] = strconv.FormatInt(o.FencingToken, 10)
	}
	return commonkafka.ProtoHeaders(&sentinelv1.Observation{}, Producer, extra)
}

func platform(o *obs.Observation) *sentinelv1.PlatformDetail {
	switch {
	case o.ADSB != nil:
		a := o.ADSB
		return &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Adsb{Adsb: &sentinelv1.AdsbDetail{
			IcaoHex: a.ICAOHex, Registration: a.Registration, AircraftType: a.AircraftType, AircraftTypeName: a.AircraftTypeName,
			OperatorIcao: a.OperatorICAO, OperatorName: a.OperatorName, Squawk: a.Squawk, Emergency: a.Emergency, Mode1: a.Mode1, Mode2: a.Mode2,
			AircraftId: a.AircraftID, FlightAirborne: a.FlightAirborne, IndicatedAirSpeed: a.IndicatedAirSpd, TrueAirSpeed: a.TrueAirSpeed,
			GroundSpeed: a.GroundSpeed, MagneticHeading: a.MagneticHeading, Mode5FigureOfMerit: int32(a.Mode5FigureOfMerit),
			NationalOriginCode: int32(a.NationalOriginCode), MissionCode: int32(a.MissionCode), AltitudeBaro: a.AltitudeBaro,
			AltitudeGeom: a.AltitudeGeom, VerticalRate: a.VerticalRate, OnGround: a.OnGround, Category: a.Category,
			NacP: int32(a.NacP), NacV: int32(a.NacV), Sil: int32(a.SIL), SilType: a.SILType, Nic: int32(a.NIC), Rc: a.RC, Gva: int32(a.GVA), Sda: int32(a.SDA),
		}}}
	case o.AIS != nil:
		a := o.AIS
		return &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Ais{Ais: &sentinelv1.AisDetail{
			Mmsi: a.MMSI, Imo: a.IMO, Callsign: a.Callsign, VesselName: a.VesselName, ShipType: int32(a.ShipType), ShipTypeName: a.ShipTypeName,
			Flag: a.Flag, Destination: a.Destination, Eta: a.ETA, Draught: a.Draught, DimensionA: a.DimensionA, DimensionB: a.DimensionB,
			DimensionC: a.DimensionC, DimensionD: a.DimensionD, LengthOverall: a.LengthOverall, Beam: a.Beam,
			NavStatus: parse[sentinelv1.NavigationalStatus](a.NavStatus), RateOfTurn: a.RateOfTurn, SpeedOverGround: a.SpeedOverGround,
			CourseOverGround: a.CourseOverGround, TrueHeading: a.TrueHeading, PositionAccuracyHigh: a.PositionAccuracyHigh,
			SpecialManoeuvre: a.SpecialManoeuvre, MessageType: int32(a.MessageType), RepeatIndicator: int32(a.RepeatIndicator),
		}}}
	case o.TLE != nil:
		t := o.TLE
		return &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Tle{Tle: &sentinelv1.TleDetail{
			NoradId: int32(t.NoradID), IntlDesignator: t.IntlDesignator, SatName: t.SatName, Line1: t.Line1, Line2: t.Line2, Epoch: t.Epoch,
			Inclination: t.Inclination, Eccentricity: t.Eccentricity, Raan: t.RAAN, ArgOfPerigee: t.ArgOfPerigee, MeanAnomaly: t.MeanAnomaly,
			MeanMotion: t.MeanMotion, Period: t.Period, Apogee: t.Apogee, Perigee: t.Perigee, ObjectType: t.ObjectType, RcsSize: t.RCSSize,
			LaunchDate: t.LaunchDate, DecayDate: t.DecayDate, Country: t.Country,
		}}}
	case o.Link16 != nil:
		l := o.Link16
		return &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Link16{Link16: &sentinelv1.Link16Detail{
			TrackNumber: int32(l.TrackNumber), JSeriesLabel: l.JSeriesLabel, OriginatingUnit: l.OriginatingUnit, Quality: int32(l.Quality),
			ExerciseIndicator: l.ExerciseIndicator, SimulationIndicator: l.SimulationIndicator, ForceIdentity: l.ForceIdentity,
		}}}
	case o.CoT != nil:
		c := o.CoT
		return &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Cot{Cot: &sentinelv1.CotDetail{
			Uid: c.UID, CotType: c.CoTType, How: c.How, Ce: c.CE, Le: c.LE, StaleTime: c.StaleTime, AccessControl: c.AccessControl, Opex: c.Opex, Qos: c.QOS,
		}}}
	case o.UAV != nil:
		u := o.UAV
		return &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Uav{Uav: &sentinelv1.UavDetail{
			Make: u.Make, Model: u.Model, SerialNumber: u.SerialNumber, MacAddress: u.MACAddress, OperatingFrequency: u.OperatingFrequency, FrequencyRange: u.FrequencyRange,
		}}}
	case o.RF != nil:
		r := o.RF
		return &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Rf{Rf: &sentinelv1.RfDetail{
			FrequencyHz: r.FrequencyHz, BandwidthHz: copyFloat(r.BandwidthHz),
			EmitterType: r.EmitterType, Modulation: r.Modulation,
			PriUs: copyFloat(r.PriUs), PulseWidthUs: copyFloat(r.PulseWidthUs),
			ScanType: r.ScanType, PowerDbm: copyFloat(r.PowerDbm),
		}}}
	case len(o.Generic) > 0:
		return &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Generic{Generic: &sentinelv1.GenericDetail{Fields: maps.Clone(o.Generic)}}}
	}
	return nil
}

func parse[E interface {
	~int32
	Descriptor() protoreflect.EnumDescriptor
}](name string) E {
	v, _ := pbjson.ParseEnum[E](name)
	return v
}
