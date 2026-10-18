package aismap

import (
	"errors"
	"testing"
	"time"

	ais "github.com/BertoldVdb/go-ais"

	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/source/decoder"
)

const mmsi = 244123456

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func voyage(dest string) ais.ShipStaticData {
	return ais.ShipStaticData{
		Header: ais.Header{MessageID: 5, UserID: mmsi}, Valid: true,
		ImoNumber: 9811000, CallSign: "PDAB@@@", Name: "EVER EXAMPLE@@@@", Type: 71,
		Dimension: ais.FieldDimension{A: 300, B: 99, C: 30, D: 29},
		Eta:       ais.FieldETA{Month: 10, Day: 20, Hour: 6, Minute: 30},
		MaximumStaticDraught: 14.5, Destination: dest,
	}
}

func position(sog float64) ais.PositionReport {
	return ais.PositionReport{
		Header: ais.Header{MessageID: 1, UserID: mmsi}, Valid: true,
		NavigationalStatus: 0, Sog: ais.Field10(sog), PositionAccuracy: true,
		Latitude: 51.95, Longitude: 4.05, Cog: 270, TrueHeading: 268,
	}
}

func mustMap(t *testing.T, m *Mapper, p ais.Packet, at time.Time) *obs.Observation {
	t.Helper()
	o, err := m.FromPacket(p, at)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func hasIdentity(o *obs.Observation, typ, value string) bool {
	for _, id := range o.Identities {
		if string(id.Type) == typ && id.Value == value {
			return true
		}
	}
	return false
}

func TestPositionCarriesEarlierStaticDetail(t *testing.T) {
	m := NewMapper("test")
	mustMap(t, m, voyage("ROTTERDAM"), t0)
	o := mustMap(t, m, position(12.3), t0.Add(time.Minute))

	a := o.AIS
	if o.Decoder != "test" || o.DisplayName != "EVER EXAMPLE" || a.VesselName != "EVER EXAMPLE" {
		t.Errorf("name = %q / %q (decoder %q)", o.DisplayName, a.VesselName, o.Decoder)
	}
	if a.ShipType != 71 || a.ShipTypeName != "Cargo" || a.Destination != "ROTTERDAM" || a.ETA != "10-20T06:30" || a.Draught != 14.5 {
		t.Errorf("voyage = %+v", a)
	}
	if a.IMO != "9811000" || a.Callsign != "PDAB" || !hasIdentity(o, string(obs.IdentityIMO), "9811000") || !hasIdentity(o, string(obs.IdentityCallsign), "PDAB") {
		t.Errorf("identities = %v (%+v)", o.Identities, a)
	}
	if a.LengthOverall != 399 || a.Beam != 59 || o.Dimensions == nil || *o.Dimensions != [3]float64{399, 59, 0} {
		t.Errorf("dimensions = %+v %v", a, o.Dimensions)
	}
	// The report's own kinematics are untouched.
	if !o.HasPosition || o.Latitude != 51.95 || a.SpeedOverGround != 12.3 || a.TrueHeading != 268 || a.MessageType != 1 || a.NavStatus != "UNDER_WAY_USING_ENGINE" {
		t.Errorf("position = %+v %+v", o, a)
	}
}

func TestOwnFieldsWinOverRememberedOnes(t *testing.T) {
	m := NewMapper("test")
	mustMap(t, m, voyage("ROTTERDAM"), t0)
	ext := ais.ExtendedClassBPositionReport{
		Header: ais.Header{MessageID: 19, UserID: mmsi}, Valid: true,
		Sog: 3, Latitude: 52, Longitude: 4, Cog: 90, TrueHeading: 511,
		Name: "RENAMED", Type: 37, Dimension: ais.FieldDimension{A: 10, B: 2, C: 2, D: 2},
	}
	o := mustMap(t, m, ext, t0.Add(time.Minute))
	if o.DisplayName != "RENAMED" || o.AIS.ShipType != 37 || o.AIS.LengthOverall != 12 {
		t.Errorf("own fields lost: %q %+v", o.DisplayName, o.AIS)
	}
	// What the type 19 said is now what later positions carry.
	p := mustMap(t, m, position(1), t0.Add(2*time.Minute))
	if p.DisplayName != "RENAMED" || p.AIS.ShipTypeName != "Pleasure Craft" || p.AIS.Destination != "ROTTERDAM" {
		t.Errorf("later position = %q %+v", p.DisplayName, p.AIS)
	}
}

func TestUnavailableShipTypeKeepsTheKnownOne(t *testing.T) {
	m := NewMapper("test")
	mustMap(t, m, voyage("X"), t0)
	b := ais.StaticDataReport{Header: ais.Header{MessageID: 24, UserID: mmsi}, Valid: true, PartNumber: true}
	b.ReportB.Valid = true
	b.ReportB.CallSign = "PDAB"
	o := mustMap(t, m, b, t0.Add(time.Minute))
	if o.AIS.ShipType != 71 || o.AIS.ShipTypeName != "Cargo" {
		t.Errorf("type 0 must not replace a known type: %+v", o.AIS)
	}
}

func TestVoyageReportClearsDestination(t *testing.T) {
	m := NewMapper("test")
	mustMap(t, m, voyage("ROTTERDAM"), t0)
	mustMap(t, m, voyage(""), t0.Add(time.Minute))
	if o := mustMap(t, m, position(5), t0.Add(2*time.Minute)); o.AIS.Destination != "" {
		t.Errorf("destination = %q, want cleared", o.AIS.Destination)
	}
}

func TestCallsignNeverReplacesAName(t *testing.T) {
	m := NewMapper("test")
	b := ais.StaticDataReport{Header: ais.Header{MessageID: 24, UserID: mmsi}, Valid: true, PartNumber: true}
	b.ReportB.Valid = true
	b.ReportB.CallSign = "PDAB"
	mustMap(t, m, b, t0)
	a := ais.StaticDataReport{Header: ais.Header{MessageID: 24, UserID: mmsi}, Valid: true}
	a.ReportA.Valid = true
	a.ReportA.Name = "NAMED"
	if o := mustMap(t, m, a, t0.Add(time.Minute)); o.DisplayName != "NAMED" || o.AIS.Callsign != "PDAB" {
		t.Errorf("part A after part B = %q %+v", o.DisplayName, o.AIS)
	}
}

func TestStaticDetailExpiresWithoutTraffic(t *testing.T) {
	m := NewMapper("test")
	mustMap(t, m, voyage("ROTTERDAM"), t0)
	// Traffic keeps it alive: a position every 12 h.
	mustMap(t, m, position(1), t0.Add(12*time.Hour))
	if o := mustMap(t, m, position(1), t0.Add(30*time.Hour)); o.AIS.VesselName != "EVER EXAMPLE" {
		t.Errorf("detail dropped while the vessel was heard: %+v", o.AIS)
	}
	// A day of silence drops it.
	if o := mustMap(t, m, position(1), t0.Add(55*time.Hour)); o.AIS.VesselName != "" || o.DisplayName != "MMSI 244123456" {
		t.Errorf("detail kept after a day of silence: %q %+v", o.DisplayName, o.AIS)
	}
	if m.Known() != 0 {
		t.Errorf("known = %d", m.Known())
	}
}

func TestPositionsAloneAreNotRemembered(t *testing.T) {
	m := NewMapper("test")
	mustMap(t, m, position(1), t0)
	aton := ais.AidsToNavigationReport{Header: ais.Header{MessageID: 21, UserID: 992441234}, Valid: true, Name: "BUOY 1", Latitude: 52, Longitude: 4}
	o := mustMap(t, m, aton, t0)
	if o.EntityType != obs.EntityUnknown || o.DisplayName != "BUOY 1" {
		t.Errorf("aid to navigation = %+v", o)
	}
	if m.Known() != 0 {
		t.Errorf("known = %d, want nothing remembered", m.Known())
	}
	m.Reset()
}

func TestSkipsAndErrors(t *testing.T) {
	m := NewMapper("test")
	if _, err := m.FromPacket(ais.BaseStationReport{Header: ais.Header{MessageID: 4}, Valid: true}, t0); !errors.Is(err, decoder.ErrSkip) || decoder.SkipReason(err) != "ais_base_station" {
		t.Errorf("base station: %v", err)
	}
	if _, err := m.FromPacket(ais.BinaryBroadcastMessage{Header: ais.Header{MessageID: 8}}, t0); !errors.Is(err, decoder.ErrSkip) || decoder.SkipReason(err) != "ais_type_8" {
		t.Errorf("type 8: %v", err)
	}
	if _, err := m.FromPacket(ais.PositionReport{Header: ais.Header{MessageID: 1, UserID: mmsi}}, t0); err == nil || errors.Is(err, decoder.ErrSkip) {
		t.Errorf("invalid position must be an error, got %v", err)
	}
	if _, err := m.FromPacket(nil, t0); err == nil {
		t.Error("nil packet must be an error")
	}
}
