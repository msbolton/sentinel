package wire

import (
	"math"
	"reflect"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/sentinel/go-common/geo"
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
		"rf": func(o *obs.Observation) {
			o.ADSB = nil
			bw, pri, pw, pwr := 20e6, 1000.0, 1.5, -62.0
			o.RF = &obs.RFData{
				FrequencyHz: 9.41e9, BandwidthHz: &bw, EmitterType: "RADAR_SEARCH",
				Modulation: "PULSE", PriUs: &pri, PulseWidthUs: &pw, ScanType: "CIRCULAR", PowerDbm: &pwr,
			}
		},
		"rf minimal": func(o *obs.Observation) {
			o.ADSB = nil
			o.RF = &obs.RFData{FrequencyHz: 1.2e9, EmitterType: "SATCOM"}
		},
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
		case reflect.Pointer:
			if f.Type().Elem().Kind() == reflect.Float64 {
				val := float64(i) + 0.25
				f.Set(reflect.ValueOf(&val))
				continue
			}
			if f.Type().Elem() == reflect.TypeOf([4]float64{}) {
				f.Set(reflect.ValueOf(&[4]float64{0.1, 0.2, 0.3, 0.4}))
				continue
			}
			fallthrough
		default:
			t.Fatalf("fill: unhandled kind %s for %s", f.Kind(), s.Type().Field(i).Name)
		}
	}
}

func TestFromProtoCopiesEveryPlatformField(t *testing.T) {
	adsb, ais, tle, l16, cot, uav, rf := &obs.ADSBData{}, &obs.AISData{}, &obs.TLEData{}, &obs.Link16Data{}, &obs.CoTData{}, &obs.UAVData{}, &obs.RFData{}
	radar, eoir := &obs.RadarData{}, &obs.EoIrData{}
	for _, d := range []any{adsb, ais, tle, l16, cot, uav, rf, radar, eoir} {
		fill(t, d)
	}
	ais.NavStatus = "MOORED" // an enum, so it must be a declared name
	radar.Mode, radar.Classification, eoir.Band = "SEARCH", "DRONE", "LWIR"
	for name, o := range map[string]*obs.Observation{
		"adsb": {ADSB: adsb}, "ais": {AIS: ais}, "tle": {TLE: tle}, "link16": {Link16: l16}, "cot": {CoT: cot}, "uav": {UAV: uav}, "rf": {RF: rf},
		"radar": {Radar: radar}, "eoir": {EoIr: eoir},
	} {
		got := FromProto(ToProto(o))
		if !reflect.DeepEqual(got.ADSB, o.ADSB) || !reflect.DeepEqual(got.AIS, o.AIS) || !reflect.DeepEqual(got.TLE, o.TLE) ||
			!reflect.DeepEqual(got.Link16, o.Link16) || !reflect.DeepEqual(got.CoT, o.CoT) || !reflect.DeepEqual(got.UAV, o.UAV) ||
			!reflect.DeepEqual(got.RF, o.RF) || !reflect.DeepEqual(got.Radar, o.Radar) || !reflect.DeepEqual(got.EoIr, o.EoIr) {
			t.Errorf("%s: platform detail differs\nwant %+v\ngot  %+v", name, o, got)
		}
	}
}

func TestFromProtoConvertsEllipseToCovarianceAndDerivesCEP95(t *testing.T) {
	m := &sentinelv1.Observation{
		SourceEntityId: "sig-1",
		Uncertainty:    &sentinelv1.MeasurementUncertainty{SemiMajor: 1800, SemiMinor: 420, Orientation: 32},
	}
	o := FromProto(m)
	if o.PosCovariance == nil {
		t.Fatal("expected PosCovariance to be populated from Uncertainty ellipse")
	}
	wantNN, wantNE, wantEE := geo.Cov2FromEllipse(1800, 420, 32)
	if math.Abs(o.PosCovariance[0]-wantNN) > 1e-6 || math.Abs(o.PosCovariance[1]-wantNE) > 1e-6 || math.Abs(o.PosCovariance[3]-wantEE) > 1e-6 {
		t.Errorf("PosCovariance = %v, want [%v, %v, 0, %v, 0, 0]", o.PosCovariance, wantNN, wantNE, wantEE)
	}
	wantCEP := geo.CEP95(wantNN, wantNE, wantEE)
	if o.CircularError == nil || math.Abs(*o.CircularError-wantCEP) > 1e-6 || *o.CircularError < 1450 || *o.CircularError > 1500 {
		t.Errorf("CircularError = %v, want %v (in [1450, 1500])", o.CircularError, wantCEP)
	}

	// Explicit PositionCovariance wins over Uncertainty ellipse axes, while still deriving 95% CEP when CircularError is 0.
	m2 := &sentinelv1.Observation{
		SourceEntityId:     "sig-2",
		Uncertainty:        &sentinelv1.MeasurementUncertainty{SemiMajor: 1800, SemiMinor: 420, Orientation: 32},
		PositionCovariance: &sentinelv1.PositionCovariance{PnPn: 10000, PePe: 10000},
	}
	o2 := FromProto(m2)
	if o2.PosCovariance == nil || o2.PosCovariance[0] != 10000 || o2.PosCovariance[3] != 10000 {
		t.Errorf("explicit PositionCovariance not preserved: %v", o2.PosCovariance)
	}
	if o2.CircularError == nil || math.Abs(*o2.CircularError-244.77) > 0.01 {
		t.Errorf("CircularError from explicit covariance = %v, want 244.77", o2.CircularError)
	}

	// Swapped axes (semi_major < semi_minor): (420, 1800, 32) puts the 420 m
	// axis along 32° and the 1800 m axis along 122°, so it must yield the
	// exact same covariance and CEP95 as (1800, 420, 122).
	mSwapped := &sentinelv1.Observation{
		SourceEntityId: "sig-swapped",
		Uncertainty:    &sentinelv1.MeasurementUncertainty{SemiMajor: 420, SemiMinor: 1800, Orientation: 32},
	}
	mRotated := &sentinelv1.Observation{
		SourceEntityId: "sig-rotated",
		Uncertainty:    &sentinelv1.MeasurementUncertainty{SemiMajor: 1800, SemiMinor: 420, Orientation: 122},
	}
	oSwapped := FromProto(mSwapped)
	oRotated := FromProto(mRotated)
	if oSwapped.PosCovariance == nil || oRotated.PosCovariance == nil {
		t.Fatal("expected non-nil PosCovariance for swapped and rotated ellipses")
	}
	for i := 0; i < 6; i++ {
		if math.Abs(oSwapped.PosCovariance[i]-oRotated.PosCovariance[i]) > 1e-6 {
			t.Errorf("PosCovariance[%d]: swapped=%v, rotated=%v", i, oSwapped.PosCovariance[i], oRotated.PosCovariance[i])
		}
	}
	if oSwapped.CircularError == nil || oRotated.CircularError == nil || math.Abs(*oSwapped.CircularError-*oRotated.CircularError) > 1e-6 {
		t.Errorf("CircularError: swapped=%v, rotated=%v", oSwapped.CircularError, oRotated.CircularError)
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

func TestFromProtoRadar(t *testing.T) {
	rr, rcs, snr, q, conf, h := -12.5, 3.25, 21.0, 0.8, 0.9, 42.0
	m := &sentinelv1.Observation{SourceEntityId: "trk-r", Platform: &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Radar{Radar: &sentinelv1.RadarDetail{
		RangeRateMps: &rr, RcsDbsm: &rcs, SnrDb: &snr, Mode: sentinelv1.RadarMode_RADAR_MODE_SEARCH, TrackQuality: &q,
		Classification: sentinelv1.RadarClass_RADAR_CLASS_DRONE, ClassificationConfidence: &conf, RadarTrackId: "R-7", HeightAboveGroundM: &h,
	}}}}
	o := FromProto(m)
	r := o.Radar
	if r == nil || o.EoIr != nil || o.RF != nil {
		t.Fatalf("radar = %+v, eoir = %+v", r, o.EoIr)
	}
	if r.Classification != "DRONE" || r.Mode != "SEARCH" || r.RadarTrackID != "R-7" ||
		r.RangeRateMps == nil || *r.RangeRateMps != -12.5 || r.RcsDbsm == nil || *r.RcsDbsm != 3.25 || r.SnrDb == nil || *r.SnrDb != 21 ||
		r.TrackQuality == nil || *r.TrackQuality != 0.8 || r.ClassificationConfidence == nil || *r.ClassificationConfidence != 0.9 ||
		r.HeightAboveGroundM == nil || *r.HeightAboveGroundM != 42 {
		t.Errorf("radar = %+v", r)
	}
	if o2 := FromProto(&sentinelv1.Observation{Platform: &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Radar{Radar: &sentinelv1.RadarDetail{}}}}); o2.Radar == nil ||
		o2.Radar.Mode != "" || o2.Radar.Classification != "" || o2.Radar.SnrDb != nil {
		t.Errorf("an empty radar detail must keep absent values absent: %+v", o2.Radar)
	}
}

func TestFromProtoEoIr(t *testing.T) {
	conf, h, v := 0.7, 12.0, 8.0
	m := &sentinelv1.Observation{SourceEntityId: "trk-e", Platform: &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Eoir{Eoir: &sentinelv1.EoIrDetail{
		Band: sentinelv1.EoIrBand_EO_IR_BAND_LWIR, Classification: "person", ClassificationConfidence: &conf,
		HorizontalFovDeg: &h, VerticalFovDeg: &v, BoundingBox: &sentinelv1.NormalizedBox{X: 0.1, Y: 0.2, Width: 0.3, Height: 0.4}, CameraTrackId: "C-3",
	}}}}
	o := FromProto(m)
	e := o.EoIr
	if e == nil || o.Radar != nil {
		t.Fatalf("eoir = %+v, radar = %+v", e, o.Radar)
	}
	if e.Band != "LWIR" || e.Classification != "person" || e.CameraTrackID != "C-3" ||
		e.ClassificationConfidence == nil || *e.ClassificationConfidence != 0.7 || e.HorizontalFovDeg == nil || *e.HorizontalFovDeg != 12 ||
		e.VerticalFovDeg == nil || *e.VerticalFovDeg != 8 || e.BoundingBox == nil || *e.BoundingBox != [4]float64{0.1, 0.2, 0.3, 0.4} {
		t.Errorf("eoir = %+v", e)
	}
	if o2 := FromProto(&sentinelv1.Observation{Platform: &sentinelv1.PlatformDetail{Detail: &sentinelv1.PlatformDetail_Eoir{Eoir: &sentinelv1.EoIrDetail{}}}}); o2.EoIr == nil ||
		o2.EoIr.BoundingBox != nil || o2.EoIr.Band != "" {
		t.Errorf("an empty eoir detail must keep absent values absent: %+v", o2.EoIr)
	}
}

func TestPolarDirectionOnlyRoundTrip(t *testing.T) {
	m := &sentinelv1.Observation{SourceEntityId: "trk-d", Polar: &sentinelv1.PolarMeasurement{AzimuthDeg: 30, DirectionOnly: true}}
	o := FromProto(m)
	if o.Polar == nil || !o.Polar.DirectionOnly {
		t.Fatalf("polar = %+v", o.Polar)
	}
	if got := ToProto(o).GetPolar(); !got.GetDirectionOnly() || got.GetAzimuthDeg() != 30 {
		t.Errorf("round trip lost direction_only: %v", got)
	}
	o.Polar.DirectionOnly = false
	if ToProto(o).GetPolar().GetDirectionOnly() {
		t.Error("direction_only must stay false when unset")
	}
}
