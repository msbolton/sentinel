package wire

import (
	"reflect"
	"testing"

	"google.golang.org/protobuf/proto"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/ingest-service/internal/obs"
)

// serverless clears the fields the ingest service owns, which FromProto ignores.
func serverless(m *sentinelv1.Observation) *sentinelv1.Observation {
	c := proto.Clone(m).(*sentinelv1.Observation)
	c.ObservationId, c.FeedId, c.Driver, c.Decoder = "", "", "", ""
	c.ReceivedAt, c.ReplicaId, c.FencingToken, c.Raw = nil, "", 0, nil
	c.EmplacementId, c.Exercise = "", false
	return c
}

func TestFromProtoRoundTripsEveryPlatform(t *testing.T) {
	speed, course := 12.5, 271.0
	variants := map[string]func(o *obs.Observation){
		"adsb": func(o *obs.Observation) {},
		"ais": func(o *obs.Observation) {
			o.ADSB = nil
			o.AIS = &obs.AISData{MMSI: "244123456", NavStatus: "MOORED", ShipType: 70, LengthOverall: 200}
		},
		"tle": func(o *obs.Observation) {
			o.ADSB = nil
			o.TLE = &obs.TLEData{NoradID: 25544, SatName: "ISS", Line1: "1 ...", Line2: "2 ..."}
		},
		"cot": func(o *obs.Observation) { o.ADSB = nil; o.CoT = &obs.CoTData{UID: "u-1", CoTType: "a-f-G", How: "m-g"} },
		"link16": func(o *obs.Observation) {
			o.ADSB = nil
			o.Link16 = &obs.Link16Data{TrackNumber: 1234, JSeriesLabel: "J3.2", Quality: 7}
		},
		"uav": func(o *obs.Observation) { o.ADSB = nil; o.UAV = &obs.UAVData{Make: "DJI", Model: "M300"} },
		"kinematics": func(o *obs.Observation) {
			o.SpeedKnots, o.Course = &speed, &course
			o.Acceleration = &[3]float64{0.1, 0.2, 0.3}
			o.Orientation = &[3]float64{10, 2, 1}
			ce := 25.0
			o.CircularError = &ce
			o.PosCovariance = &[6]float64{1, 2, 3, 4, 5, 6}
			o.PosVelCovariance = &[9]float64{1, 2, 3, 4, 5, 6, 7, 8, 9}
			o.VelCovariance = &[6]float64{6, 5, 4, 3, 2, 1}
			o.Dimensions = &[3]float64{30, 20, 5}
			o.CountryOfOrigin, o.Affiliation, o.OperationalStatus, o.Classification = "US", "FRIENDLY", "OPERATIONAL", "SECRET"
		},
	}
	for name, apply := range variants {
		o := sample()
		apply(o)
		m := ToProto(o)
		got := ToProto(FromProto(m))
		if !proto.Equal(serverless(m), got) {
			t.Errorf("%s: round trip differs\nwant %v\ngot  %v", name, serverless(m), got)
		}
	}
}

func TestFromProtoIgnoresServerFieldsAndUnknownEnums(t *testing.T) {
	m := ToProto(sample())
	m.EntityType = sentinelv1.EntityType(999)
	o := FromProto(m)
	if o.ID != "" || o.FeedID != "" || o.Driver != "" || o.Decoder != "" || !o.ReceivedAt.IsZero() || o.ReplicaID != "" || o.FencingToken != 0 || o.Raw != nil {
		t.Errorf("server fields copied: %+v", o)
	}
	if o.EntityType != "" {
		t.Errorf("unknown enum = %q, want unspecified", o.EntityType)
	}
	// No aliasing: changing the decoded heading leaves the message alone.
	*o.Heading = 1
	if m.GetHeading() == 1 {
		t.Error("FromProto must copy optional values, not share them")
	}
}

// fill gives every field of the struct a distinct non-zero value, so a
// swapped or dropped field shows in a round trip.
func fill(t *testing.T, v any) {
	t.Helper()
	s := reflect.ValueOf(v).Elem()
	for i := 0; i < s.NumField(); i++ {
		f := s.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString(s.Type().Field(i).Name)
		case reflect.Int:
			f.SetInt(int64(i + 1))
		case reflect.Float64:
			f.SetFloat(float64(i) + 0.5)
		case reflect.Bool:
			f.SetBool(true)
		default:
			t.Fatalf("fill: unhandled kind %s for %s", f.Kind(), s.Type().Field(i).Name)
		}
	}
}

func TestFromProtoCopiesEveryPlatformField(t *testing.T) {
	adsb, ais, tle, l16, cot, uav := &obs.ADSBData{}, &obs.AISData{}, &obs.TLEData{}, &obs.Link16Data{}, &obs.CoTData{}, &obs.UAVData{}
	for _, d := range []any{adsb, ais, tle, l16, cot, uav} {
		fill(t, d)
	}
	ais.NavStatus = "MOORED" // an enum, so it must be a declared name
	for name, o := range map[string]*obs.Observation{
		"adsb": {ADSB: adsb}, "ais": {AIS: ais}, "tle": {TLE: tle}, "link16": {Link16: l16}, "cot": {CoT: cot}, "uav": {UAV: uav},
	} {
		got := FromProto(ToProto(o))
		if !reflect.DeepEqual(got.ADSB, o.ADSB) || !reflect.DeepEqual(got.AIS, o.AIS) || !reflect.DeepEqual(got.TLE, o.TLE) ||
			!reflect.DeepEqual(got.Link16, o.Link16) || !reflect.DeepEqual(got.CoT, o.CoT) || !reflect.DeepEqual(got.UAV, o.UAV) {
			t.Errorf("%s: platform detail differs\nwant %+v\ngot  %+v", name, o, got)
		}
	}
}

func TestFromProtoCopiesPolarAndIgnoresServiceOwnedEmplacementFields(t *testing.T) {
	el, sr := 2.5, 40.0
	m := &sentinelv1.Observation{
		SourceEntityId: "trk-1",
		EmplacementId:  "11111111-1111-1111-1111-111111111111",
		Exercise:       true,
		Polar: &sentinelv1.PolarMeasurement{RangeM: 12000, AzimuthDeg: 45, ElevationDeg: &el,
			AzimuthReference: sentinelv1.AzimuthReference_AZIMUTH_REFERENCE_PLATFORM,
			Sigma:            &sentinelv1.PolarSigma{RangeM: &sr}},
	}
	o := FromProto(m)
	if o.EmplacementID != "" || o.Exercise {
		t.Errorf("a sender's emplacement_id and exercise must be ignored: %q %v", o.EmplacementID, o.Exercise)
	}
	p := o.Polar
	if p == nil || p.RangeM != 12000 || p.AzimuthDeg != 45 || p.ElevationDeg == nil || *p.ElevationDeg != 2.5 ||
		p.AzimuthRef != "PLATFORM" || p.SigmaRangeM == nil || *p.SigmaRangeM != 40 || p.SigmaAzimuthDeg != nil || p.RangeRateMps != nil {
		t.Fatalf("polar = %+v", p)
	}
	el = 9
	if *p.ElevationDeg != 2.5 {
		t.Errorf("the copy must not share the message's value")
	}
}
