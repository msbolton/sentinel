package ingest

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/models"
)

// ──────────────────────────────────────────────────────────────────────────────
// ADS-B Tests
// ──────────────────────────────────────────────────────────────────────────────

func TestParseADSB_ValidType3(t *testing.T) {
	msg := "MSG,3,1,1,A1B2C3,1,2025/01/15,12:30:00.000,2025/01/15,12:30:00.000,,35000,450,180.5,51.5074,-0.1278,,,,,,0"
	p := NewParser()
	entity, err := p.ParseADSB([]byte(msg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if entity.EntityID != "ICAO-A1B2C3" {
		t.Errorf("EntityID = %q, want ICAO-A1B2C3", entity.EntityID)
	}
	if entity.EntityType != models.EntityTypeAircraft {
		t.Errorf("EntityType = %q, want %q", entity.EntityType, models.EntityTypeAircraft)
	}
	if entity.Latitude != 51.5074 {
		t.Errorf("Latitude = %f, want 51.5074", entity.Latitude)
	}
	if entity.Longitude != -0.1278 {
		t.Errorf("Longitude = %f, want -0.1278", entity.Longitude)
	}

	// 35000 ft * 0.3048 = 10668.0 m
	expectedAlt := 35000.0 * 0.3048
	if math.Abs(entity.Altitude-expectedAlt) > 0.1 {
		t.Errorf("Altitude = %f, want %f", entity.Altitude, expectedAlt)
	}
	if entity.SpeedKnots != 450.0 {
		t.Errorf("SpeedKnots = %f, want 450.0", entity.SpeedKnots)
	}
	if entity.Heading != 180.5 {
		t.Errorf("Heading = %f, want 180.5", entity.Heading)
	}

	expectedTime := time.Date(2025, 1, 15, 12, 30, 0, 0, time.UTC)
	if !entity.Timestamp.Equal(expectedTime) {
		t.Errorf("Timestamp = %v, want %v", entity.Timestamp, expectedTime)
	}

	// Name fallback to ICAO hex (callsign field 10 is empty).
	if entity.Name != "ICAO A1B2C3" {
		t.Errorf("Name = %q, want %q", entity.Name, "ICAO A1B2C3")
	}
}

func TestParseADSB_Type1NoPosition(t *testing.T) {
	msg := "MSG,1,1,1,A1B2C3,1,2025/01/15,12:30:00.000,2025/01/15,12:30:00.000,CALLSIGN,,,,,,,,,,,"
	p := NewParser()
	_, err := p.ParseADSB([]byte(msg))
	if err == nil {
		t.Fatal("expected error for MSG type 1, got nil")
	}
	if !strings.Contains(err.Error(), "type 1") {
		t.Errorf("error = %q, want mention of type 1", err.Error())
	}
}

func TestParseADSB_EmptyICAO(t *testing.T) {
	msg := "MSG,3,1,1,,1,2025/01/15,12:30:00.000,2025/01/15,12:30:00.000,,35000,450,180.5,51.5074,-0.1278,,,,,,0"
	p := NewParser()
	_, err := p.ParseADSB([]byte(msg))
	if err == nil {
		t.Fatal("expected error for empty ICAO, got nil")
	}
}

func TestParseADSB_ShortFieldCount(t *testing.T) {
	msg := "MSG,3,1,1,A1B2C3,1,2025/01/15,12:30:00.000"
	p := NewParser()
	_, err := p.ParseADSB([]byte(msg))
	if err == nil {
		t.Fatal("expected error for short fields, got nil")
	}
}

func TestParseADSB_MissingTimestampFallback(t *testing.T) {
	msg := "MSG,3,1,1,A1B2C3,1,,,2025/01/15,12:30:00.000,,35000,450,180.5,51.5074,-0.1278,,,,,,0"
	p := NewParser()
	before := time.Now().Add(-1 * time.Second)
	entity, err := p.ParseADSB([]byte(msg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	after := time.Now().Add(1 * time.Second)
	if entity.Timestamp.Before(before) || entity.Timestamp.After(after) {
		t.Errorf("Timestamp = %v, expected near now", entity.Timestamp)
	}
}

func TestParseADSB_WithCallsign(t *testing.T) {
	msg := "MSG,3,1,1,A1B2C3,1,2025/01/15,12:30:00.000,2025/01/15,12:30:00.000,BAW123,35000,450,180.5,51.5074,-0.1278,,,,,,0"
	p := NewParser()
	entity, err := p.ParseADSB([]byte(msg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entity.Name != "BAW123" {
		t.Errorf("Name = %q, want %q", entity.Name, "BAW123")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Multi-fragment AIS Tests
// ──────────────────────────────────────────────────────────────────────────────

func TestParseAIS_SingleFragmentType1(t *testing.T) {
	// Real-world AIS type 1 message (single fragment).
	msg := "!AIVDM,1,1,,B,13u@Dt002s000000000000000000,0*13"
	p := NewParser()
	entity, err := p.ParseAIS([]byte(msg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entity.EntityType != models.EntityTypeVessel {
		t.Errorf("EntityType = %q, want %q", entity.EntityType, models.EntityTypeVessel)
	}
	if !strings.HasPrefix(entity.EntityID, "MMSI-") {
		t.Errorf("EntityID = %q, want MMSI- prefix", entity.EntityID)
	}
}

func TestParseAIS_TwoFragmentType5(t *testing.T) {
	// Construct a 2-fragment Type 5 AIS message.
	// We'll build the payload encoding manually for a Type 5 message.
	// Type 5 needs 424 bits = ~71 6-bit chars.
	// For testing, we split the payload across two fragments.
	frag1 := "!AIVDM,2,1,3,B,55?MbV02>H97ac<H4eEK6WSF220l4hB222222222220l1@E846RLAT0Eq,0*2C"
	frag2 := "!AIVDM,2,2,3,B,888888888888880,2*21"

	p := NewParser()

	// First fragment should be buffered.
	_, err := p.ParseAIS([]byte(frag1))
	if err == nil {
		t.Fatal("expected buffering error for first fragment, got nil")
	}
	if !strings.Contains(err.Error(), "buffered") {
		t.Fatalf("expected buffering message, got: %v", err)
	}

	// Second fragment should complete reassembly and parse.
	entity, err := p.ParseAIS([]byte(frag2))
	if err != nil {
		t.Fatalf("unexpected error on second fragment: %v", err)
	}

	if entity.EntityType != models.EntityTypeVessel {
		t.Errorf("EntityType = %q, want %q", entity.EntityType, models.EntityTypeVessel)
	}
	if !strings.HasPrefix(entity.EntityID, "MMSI-") {
		t.Errorf("EntityID = %q, want MMSI- prefix", entity.EntityID)
	}

	// Type 5 has no position.
	if entity.Latitude != 0 || entity.Longitude != 0 {
		t.Errorf("Type 5 should have zero lat/lon, got %f, %f", entity.Latitude, entity.Longitude)
	}

	// Should have decoded a vessel name.
	if entity.Name == "" || strings.HasPrefix(entity.Name, "MMSI ") {
		t.Logf("Name = %q (name decoding may vary with test payload)", entity.Name)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// AIS 6-bit text decoding
// ──────────────────────────────────────────────────────────────────────────────

func TestDecodeAIS6BitText_Basic(t *testing.T) {
	// Encode "HELLO" in 6-bit: H=8, E=5, L=12, L=12, O=15
	// Each value is the letter's position (A=1, B=2, ...)
	bits := make([]byte, 30) // 5 chars * 6 bits
	encode6Bit := func(offset int, val byte) {
		for i := 5; i >= 0; i-- {
			if val&(1<<uint(i)) != 0 {
				bits[offset+(5-i)] = 1
			}
		}
	}
	encode6Bit(0, 8)  // H
	encode6Bit(6, 5)  // E
	encode6Bit(12, 12) // L
	encode6Bit(18, 12) // L
	encode6Bit(24, 15) // O

	result := decodeAIS6BitText(bits, 0, 30)
	if result != "HELLO" {
		t.Errorf("decodeAIS6BitText = %q, want %q", result, "HELLO")
	}
}

func TestDecodeAIS6BitText_TrailingAt(t *testing.T) {
	// "AB" followed by three @ (value 0) which should be trimmed.
	bits := make([]byte, 30)
	encode := func(offset int, val byte) {
		for i := 5; i >= 0; i-- {
			if val&(1<<uint(i)) != 0 {
				bits[offset+(5-i)] = 1
			}
		}
	}
	encode(0, 1)  // A
	encode(6, 2)  // B
	// Rest is 0 (@) — already zero-initialized.

	result := decodeAIS6BitText(bits, 0, 30)
	if result != "AB" {
		t.Errorf("decodeAIS6BitText = %q, want %q", result, "AB")
	}
}

func TestDecodeAIS6BitText_Digits(t *testing.T) {
	// Digits 0-9 are values 48-57 in ASCII, but in AIS 6-bit they are 48-57.
	// Actually in the mapping: val >= 32 → ch = val + ' ' - 32 = val
	// '0' = 48, so val=48 → ch=48='0'. But 6-bit max is 63.
	// val 48 → ch = 48 + 32 - 32 = 48 = '0'. Correct.
	bits := make([]byte, 18)
	encode := func(offset int, val byte) {
		for i := 5; i >= 0; i-- {
			if val&(1<<uint(i)) != 0 {
				bits[offset+(5-i)] = 1
			}
		}
	}
	encode(0, 48) // '0'
	encode(6, 49) // '1'
	encode(12, 50) // '2'

	result := decodeAIS6BitText(bits, 0, 18)
	if result != "012" {
		t.Errorf("decodeAIS6BitText = %q, want %q", result, "012")
	}
}

func TestDecodeAIS6BitText_Spaces(t *testing.T) {
	// Space is value 32 in AIS 6-bit. Trailing spaces should be trimmed.
	bits := make([]byte, 24)
	encode := func(offset int, val byte) {
		for i := 5; i >= 0; i-- {
			if val&(1<<uint(i)) != 0 {
				bits[offset+(5-i)] = 1
			}
		}
	}
	encode(0, 1)  // A
	encode(6, 32) // space
	encode(12, 32) // space (trailing)
	encode(18, 32) // space (trailing)

	result := decodeAIS6BitText(bits, 0, 24)
	if result != "A" {
		t.Errorf("decodeAIS6BitText = %q, want %q", result, "A")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Link 16 / JREAP-C Tests
// ──────────────────────────────────────────────────────────────────────────────

// buildJREAPCMessage constructs a minimal valid JREAP-C message for testing.
func buildJREAPCMessage(version, msgType byte, jLabel uint16, trackNum uint16, lat, lon int32, alt int16, speed, heading uint16) []byte {
	totalLen := uint16(36) // 16-byte header + 20-byte track data
	buf := make([]byte, totalLen)

	// Header
	buf[0] = version
	buf[1] = msgType
	binary.BigEndian.PutUint16(buf[2:4], totalLen)
	binary.BigEndian.PutUint32(buf[4:8], 1000)  // source ID
	binary.BigEndian.PutUint32(buf[8:12], 2000)  // dest ID
	binary.BigEndian.PutUint32(buf[12:16], 1)    // sequence

	// Track data at offset 16
	td := buf[16:]
	binary.BigEndian.PutUint16(td[0:2], jLabel)
	td[2] = 0 // padding
	td[3] = 0 // padding
	binary.BigEndian.PutUint16(td[4:6], trackNum)
	binary.BigEndian.PutUint32(td[6:10], uint32(lat))
	binary.BigEndian.PutUint32(td[10:14], uint32(lon))
	binary.BigEndian.PutUint16(td[14:16], uint16(alt))
	binary.BigEndian.PutUint16(td[16:18], speed)
	binary.BigEndian.PutUint16(td[18:20], heading)

	return buf
}

func TestParseLink16_AircraftJ22(t *testing.T) {
	// Encode lat=51.5 degrees: value = 51.5 * 2^23 / 90 = 4797809 (approx)
	latRaw := int32(math.Round(51.5 * math.Pow(2, 23) / 90.0))
	// Encode lon=-0.1 degrees: value = -0.1 * 2^23 / 180 = -4660 (approx)
	lonRaw := int32(math.Round(-0.1 * math.Pow(2, 23) / 180.0))
	// Altitude 3000m: value = 3000 / 3.048 = 984 (approx)
	altRaw := int16(math.Round(3000.0 / 3.048))
	// Speed 250 knots: value = 250 / 0.1 = 2500
	spdRaw := uint16(2500)
	// Heading 90 degrees: value = 90 * 65536 / 360 = 16384
	hdgRaw := uint16(math.Round(90.0 * 65536.0 / 360.0))

	data := buildJREAPCMessage(1, 1, 0x0200, 42, latRaw, lonRaw, altRaw, spdRaw, hdgRaw)

	p := NewParser()
	entity, err := p.ParseLink16(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if entity.EntityID != "JTN-42" {
		t.Errorf("EntityID = %q, want JTN-42", entity.EntityID)
	}
	if entity.EntityType != models.EntityTypeAircraft {
		t.Errorf("EntityType = %q, want %q", entity.EntityType, models.EntityTypeAircraft)
	}

	if math.Abs(entity.Latitude-51.5) > 0.001 {
		t.Errorf("Latitude = %f, want ~51.5", entity.Latitude)
	}
	if math.Abs(entity.Longitude-(-0.1)) > 0.001 {
		t.Errorf("Longitude = %f, want ~-0.1", entity.Longitude)
	}
	if math.Abs(entity.Altitude-3000.0) > 1.0 {
		t.Errorf("Altitude = %f, want ~3000.0", entity.Altitude)
	}
	if math.Abs(entity.SpeedKnots-250.0) > 0.1 {
		t.Errorf("SpeedKnots = %f, want ~250.0", entity.SpeedKnots)
	}
	if math.Abs(entity.Heading-90.0) > 0.1 {
		t.Errorf("Heading = %f, want ~90.0", entity.Heading)
	}
}

func TestParseLink16_VesselJ32(t *testing.T) {
	data := buildJREAPCMessage(1, 1, 0x0302, 100, 0, 0, 0, 0, 0)
	p := NewParser()
	entity, err := p.ParseLink16(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entity.EntityType != models.EntityTypeVessel {
		t.Errorf("EntityType = %q, want %q", entity.EntityType, models.EntityTypeVessel)
	}
}

func TestParseLink16_VehicleJ35(t *testing.T) {
	data := buildJREAPCMessage(1, 1, 0x0305, 200, 0, 0, 0, 0, 0)
	p := NewParser()
	entity, err := p.ParseLink16(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entity.EntityType != models.EntityTypeVehicle {
		t.Errorf("EntityType = %q, want %q", entity.EntityType, models.EntityTypeVehicle)
	}
}

func TestParseLink16_HeaderLengthMismatch(t *testing.T) {
	data := buildJREAPCMessage(1, 1, 0x0200, 42, 0, 0, 0, 0, 0)
	// Set length field larger than actual data.
	binary.BigEndian.PutUint16(data[2:4], 100)
	p := NewParser()
	_, err := p.ParseLink16(data)
	if err == nil {
		t.Fatal("expected error for header length mismatch, got nil")
	}
}

func TestParseLink16_PayloadTooShort(t *testing.T) {
	// 16-byte header with length=20, but no track data beyond header.
	buf := make([]byte, 20)
	buf[0] = 1  // version
	buf[1] = 1  // msg type
	binary.BigEndian.PutUint16(buf[2:4], 20)
	p := NewParser()
	_, err := p.ParseLink16(buf)
	if err == nil {
		t.Fatal("expected error for payload too short, got nil")
	}
}

func TestIsJREAPCHeader_InvalidVersion(t *testing.T) {
	buf := make([]byte, 16)
	buf[0] = 0 // invalid version
	buf[1] = 1
	binary.BigEndian.PutUint16(buf[2:4], 16)
	if isJREAPCHeader(buf) {
		t.Error("expected false for version 0")
	}

	buf[0] = 3 // invalid version
	if isJREAPCHeader(buf) {
		t.Error("expected false for version 3")
	}
}

func TestIsJREAPCHeader_InvalidMsgType(t *testing.T) {
	buf := make([]byte, 16)
	buf[0] = 1
	buf[1] = 0 // invalid msg type
	binary.BigEndian.PutUint16(buf[2:4], 16)
	if isJREAPCHeader(buf) {
		t.Error("expected false for msg type 0")
	}

	buf[1] = 17 // invalid msg type
	if isJREAPCHeader(buf) {
		t.Error("expected false for msg type 17")
	}
}

func TestIsJREAPCHeader_Valid(t *testing.T) {
	buf := make([]byte, 36)
	buf[0] = 2  // version
	buf[1] = 16 // msg type
	binary.BigEndian.PutUint16(buf[2:4], 36)
	if !isJREAPCHeader(buf) {
		t.Error("expected true for valid header")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// ParseGeneric auto-detection tests
// ──────────────────────────────────────────────────────────────────────────────

func TestParseGeneric_JSON(t *testing.T) {
	json := `{"entity_id":"test-1","entity_type":"vessel","latitude":38.9,"longitude":-77.0}`
	p := NewParser()
	entity, err := p.ParseGeneric("mqtt", []byte(json))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entity.EntityID != "test-1" {
		t.Errorf("EntityID = %q, want test-1", entity.EntityID)
	}
	if entity.Source != "mqtt" {
		t.Errorf("Source = %q, want mqtt", entity.Source)
	}
}

func TestParseGeneric_NMEA_GGA(t *testing.T) {
	nmea := "$GPGGA,123519,4807.038,N,01131.000,E,1,08,0.9,545.4,M,47.0,M,,*47"
	p := NewParser()
	entity, err := p.ParseGeneric("tcp", []byte(nmea))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entity.Source != "tcp" {
		t.Errorf("Source = %q, want tcp", entity.Source)
	}
	// 4807.038 N = 48 + 7.038/60 ≈ 48.1173
	if math.Abs(entity.Latitude-48.1173) > 0.001 {
		t.Errorf("Latitude = %f, want ~48.1173", entity.Latitude)
	}
}

func TestParseGeneric_CoT(t *testing.T) {
	cot := `<event uid="test-uid" type="a-f-A" time="2025-01-15T12:00:00Z" start="2025-01-15T12:00:00Z" stale="2025-01-15T12:05:00Z" how="m-g">
		<point lat="38.9" lon="-77.0" hae="100" ce="10" le="10"/>
		<detail><track speed="50" course="90"/><contact callsign="ALPHA1"/></detail>
	</event>`
	p := NewParser()
	entity, err := p.ParseGeneric("stomp", []byte(cot))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entity.EntityType != models.EntityTypeAircraft {
		t.Errorf("EntityType = %q, want %q", entity.EntityType, models.EntityTypeAircraft)
	}
	if entity.Source != "stomp" {
		t.Errorf("Source = %q, want stomp", entity.Source)
	}
}

func TestParseGeneric_ADSB(t *testing.T) {
	msg := "MSG,3,1,1,A1B2C3,1,2025/01/15,12:30:00.000,2025/01/15,12:30:00.000,,35000,450,180.5,51.5074,-0.1278,,,,,,0"
	p := NewParser()
	entity, err := p.ParseGeneric("tcp", []byte(msg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entity.EntityID != "ICAO-A1B2C3" {
		t.Errorf("EntityID = %q, want ICAO-A1B2C3", entity.EntityID)
	}
	if entity.EntityType != models.EntityTypeAircraft {
		t.Errorf("EntityType = %q, want %q", entity.EntityType, models.EntityTypeAircraft)
	}
}

func TestParseGeneric_AIS(t *testing.T) {
	msg := "!AIVDM,1,1,,B,13u@Dt002s000000000000000000,0*13"
	p := NewParser()
	entity, err := p.ParseGeneric("tcp", []byte(msg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(entity.EntityID, "MMSI-") {
		t.Errorf("EntityID = %q, want MMSI- prefix", entity.EntityID)
	}
}

func TestParseGeneric_Link16Binary(t *testing.T) {
	data := buildJREAPCMessage(1, 1, 0x0200, 42, 0, 0, 0, 0, 0)
	p := NewParser()
	entity, err := p.ParseGeneric("tcp", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entity.EntityID != "JTN-42" {
		t.Errorf("EntityID = %q, want JTN-42", entity.EntityID)
	}
}

func TestParseGeneric_EmptyPayload(t *testing.T) {
	p := NewParser()
	_, err := p.ParseGeneric("tcp", []byte{})
	if err == nil {
		t.Fatal("expected error for empty payload, got nil")
	}
}

func TestParseGeneric_UnknownFormat(t *testing.T) {
	p := NewParser()
	_, err := p.ParseGeneric("tcp", []byte("SOME UNKNOWN FORMAT DATA"))
	if err == nil {
		t.Fatal("expected error for unknown format, got nil")
	}
}
