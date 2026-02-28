package ingest

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sentinel/ingest-service/internal/models"
)

// Parser converts raw sensor data from multiple formats into the canonical
// EntityPosition model. It supports JSON, NMEA 0183, Cursor on Target (CoT),
// and AIS message formats.
type Parser struct{}

// NewParser creates a new multi-format message parser.
func NewParser() *Parser {
	return &Parser{}
}

// ParseGeneric attempts auto-detection of the message format and delegates
// to the appropriate parser. It tries JSON first, then checks for known
// protocol signatures.
func (p *Parser) ParseGeneric(sourceType string, data []byte) (*models.EntityPosition, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty payload")
	}

	trimmed := strings.TrimSpace(string(data))

	// Try JSON first (most common ingest format).
	if trimmed[0] == '{' {
		entity, err := p.ParseJSON(data)
		if err == nil {
			entity.Source = sourceType
			return entity, nil
		}
	}

	// Check for Cursor on Target XML envelope.
	if strings.HasPrefix(trimmed, "<?xml") || strings.HasPrefix(trimmed, "<event") {
		entity, err := p.ParseCoT(data)
		if err == nil {
			entity.Source = sourceType
			return entity, nil
		}
	}

	// Check for NMEA 0183 sentence.
	if strings.HasPrefix(trimmed, "$") || strings.HasPrefix(trimmed, "!") {
		// NMEA starts with $ for standard sentences, ! for AIS/VDM.
		if strings.HasPrefix(trimmed, "!") {
			entity, err := p.ParseAIS(data)
			if err == nil {
				entity.Source = sourceType
				return entity, nil
			}
		}
		entity, err := p.ParseNMEA(data)
		if err == nil {
			entity.Source = sourceType
			return entity, nil
		}
	}

	return nil, fmt.Errorf("unable to detect message format for payload: %s", truncate(trimmed, 100))
}

// ParseJSON parses a JSON-encoded entity position.
func (p *Parser) ParseJSON(data []byte) (*models.EntityPosition, error) {
	var entity models.EntityPosition
	if err := json.Unmarshal(data, &entity); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}

	if entity.EntityID == "" {
		entity.EntityID = uuid.New().String()
	}
	if entity.Timestamp.IsZero() {
		entity.Timestamp = time.Now().UTC()
	}
	if entity.EntityType == "" {
		entity.EntityType = models.EntityTypeUnknown
	}

	entity.RawData = data
	return &entity, nil
}

// ParseNMEA parses NMEA 0183 sentences. Supported sentence types:
//   - GGA: Global Positioning System Fix Data
//   - RMC: Recommended Minimum Specific GPS/Transit Data
func (p *Parser) ParseNMEA(data []byte) (*models.EntityPosition, error) {
	line := strings.TrimSpace(string(data))

	// Strip checksum if present.
	if idx := strings.Index(line, "*"); idx > 0 {
		line = line[:idx]
	}

	fields := strings.Split(line, ",")
	if len(fields) < 2 {
		return nil, fmt.Errorf("invalid NMEA sentence: too few fields")
	}

	sentenceType := fields[0]
	if len(sentenceType) >= 3 {
		sentenceType = sentenceType[len(sentenceType)-3:]
	}

	switch sentenceType {
	case "GGA":
		return p.parseNMEAGGA(fields, data)
	case "RMC":
		return p.parseNMEARMC(fields, data)
	default:
		return nil, fmt.Errorf("unsupported NMEA sentence type: %s", sentenceType)
	}
}

// parseNMEAGGA parses a GGA (Global Positioning System Fix Data) sentence.
// Format: $xxGGA,time,lat,N/S,lon,E/W,quality,numSV,HDOP,alt,M,sep,M,diffAge,diffStation*cs
func (p *Parser) parseNMEAGGA(fields []string, raw []byte) (*models.EntityPosition, error) {
	if len(fields) < 10 {
		return nil, fmt.Errorf("GGA sentence too short: %d fields", len(fields))
	}

	lat, err := parseNMEACoord(fields[2], fields[3])
	if err != nil {
		return nil, fmt.Errorf("parsing GGA latitude: %w", err)
	}

	lon, err := parseNMEACoord(fields[4], fields[5])
	if err != nil {
		return nil, fmt.Errorf("parsing GGA longitude: %w", err)
	}

	alt, _ := strconv.ParseFloat(fields[9], 64)

	ts := parseNMEATime(fields[1])

	return &models.EntityPosition{
		EntityID:   uuid.New().String(),
		EntityType: models.EntityTypeUnknown,
		Latitude:   lat,
		Longitude:  lon,
		Altitude:   alt,
		Timestamp:  ts,
		RawData:    raw,
	}, nil
}

// parseNMEARMC parses an RMC (Recommended Minimum) sentence.
// Format: $xxRMC,time,status,lat,N/S,lon,E/W,spd,cog,date,mv,mvE/W,posMode*cs
func (p *Parser) parseNMEARMC(fields []string, raw []byte) (*models.EntityPosition, error) {
	if len(fields) < 10 {
		return nil, fmt.Errorf("RMC sentence too short: %d fields", len(fields))
	}

	// Status check: A=active, V=void.
	if fields[2] != "A" {
		return nil, fmt.Errorf("RMC fix not active (status=%s)", fields[2])
	}

	lat, err := parseNMEACoord(fields[3], fields[4])
	if err != nil {
		return nil, fmt.Errorf("parsing RMC latitude: %w", err)
	}

	lon, err := parseNMEACoord(fields[5], fields[6])
	if err != nil {
		return nil, fmt.Errorf("parsing RMC longitude: %w", err)
	}

	speedKnots, _ := strconv.ParseFloat(fields[7], 64)
	course, _ := strconv.ParseFloat(fields[8], 64)

	ts := parseNMEADateTime(fields[1], fields[9])

	return &models.EntityPosition{
		EntityID:   uuid.New().String(),
		EntityType: models.EntityTypeUnknown,
		Latitude:   lat,
		Longitude:  lon,
		SpeedKnots: speedKnots,
		Course:     course,
		Heading:    course,
		Timestamp:  ts,
		RawData:    raw,
	}, nil
}

// parseNMEACoord converts an NMEA coordinate (DDDMM.MMMM) and hemisphere (N/S/E/W)
// to a decimal degrees value.
func parseNMEACoord(coord, hemisphere string) (float64, error) {
	if coord == "" {
		return 0, fmt.Errorf("empty coordinate")
	}

	val, err := strconv.ParseFloat(coord, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing coordinate %q: %w", coord, err)
	}

	degrees := float64(int(val / 100))
	minutes := val - degrees*100
	decimal := degrees + minutes/60.0

	if hemisphere == "S" || hemisphere == "W" {
		decimal = -decimal
	}

	return decimal, nil
}

// parseNMEATime parses an NMEA time field (HHMMSS.sss) into a time.Time.
func parseNMEATime(timeStr string) time.Time {
	if len(timeStr) < 6 {
		return time.Now().UTC()
	}

	hour, _ := strconv.Atoi(timeStr[0:2])
	min, _ := strconv.Atoi(timeStr[2:4])
	sec, _ := strconv.Atoi(timeStr[4:6])

	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), hour, min, sec, 0, time.UTC)
}

// parseNMEADateTime parses NMEA time (HHMMSS.sss) and date (DDMMYY) fields.
func parseNMEADateTime(timeStr, dateStr string) time.Time {
	if len(timeStr) < 6 || len(dateStr) < 6 {
		return time.Now().UTC()
	}

	hour, _ := strconv.Atoi(timeStr[0:2])
	min, _ := strconv.Atoi(timeStr[2:4])
	sec, _ := strconv.Atoi(timeStr[4:6])

	day, _ := strconv.Atoi(dateStr[0:2])
	month, _ := strconv.Atoi(dateStr[2:4])
	year, _ := strconv.Atoi(dateStr[4:6])
	year += 2000

	return time.Date(year, time.Month(month), day, hour, min, sec, 0, time.UTC)
}

// cotEvent represents the top-level Cursor on Target XML event.
type cotEvent struct {
	XMLName xml.Name `xml:"event"`
	UID     string   `xml:"uid,attr"`
	Type    string   `xml:"type,attr"`
	Time    string   `xml:"time,attr"`
	Start   string   `xml:"start,attr"`
	Stale   string   `xml:"stale,attr"`
	How     string   `xml:"how,attr"`
	Point   cotPoint `xml:"point"`
	Detail  cotDetail `xml:"detail"`
}

type cotPoint struct {
	Lat float64 `xml:"lat,attr"`
	Lon float64 `xml:"lon,attr"`
	Hae float64 `xml:"hae,attr"`
	CE  float64 `xml:"ce,attr"`
	LE  float64 `xml:"le,attr"`
}

type cotDetail struct {
	Track   cotTrack   `xml:"track"`
	Contact cotContact `xml:"contact"`
}

type cotTrack struct {
	Speed  float64 `xml:"speed,attr"`
	Course float64 `xml:"course,attr"`
}

type cotContact struct {
	Callsign string `xml:"callsign,attr"`
}

// ParseCoT parses a Cursor on Target (CoT) XML message, the standard
// military geospatial data exchange format.
func (p *Parser) ParseCoT(data []byte) (*models.EntityPosition, error) {
	var event cotEvent
	if err := xml.Unmarshal(data, &event); err != nil {
		return nil, fmt.Errorf("parsing CoT XML: %w", err)
	}

	ts, err := time.Parse(time.RFC3339, event.Time)
	if err != nil {
		ts = time.Now().UTC()
	}

	entityType := classifyCoTType(event.Type)

	name := event.Detail.Contact.Callsign
	if name == "" {
		name = event.UID
	}

	// Convert CoT speed (m/s) to knots.
	speedKnots := event.Detail.Track.Speed * 1.94384

	return &models.EntityPosition{
		EntityID:   event.UID,
		EntityType: entityType,
		Name:       name,
		Latitude:   event.Point.Lat,
		Longitude:  event.Point.Lon,
		Altitude:   event.Point.Hae,
		Heading:    event.Detail.Track.Course,
		SpeedKnots: speedKnots,
		Course:     event.Detail.Track.Course,
		Timestamp:  ts,
		RawData:    data,
	}, nil
}

// classifyCoTType maps CoT type strings (2525C SIDC hierarchy) to
// internal entity type classifications.
func classifyCoTType(cotType string) string {
	parts := strings.Split(cotType, "-")
	if len(parts) < 3 {
		return models.EntityTypeUnknown
	}

	// CoT type format: a-f-A (atom, affiliation, dimension)
	// Dimension codes: A=air, G=ground, S=sea, U=subsurface
	switch strings.ToUpper(parts[2]) {
	case "A":
		return models.EntityTypeAircraft
	case "S":
		return models.EntityTypeVessel
	case "G":
		return models.EntityTypeVehicle
	default:
		return models.EntityTypeUnknown
	}
}

// ParseAIS parses AIS (Automatic Identification System) maritime messages.
// This handles the common VDM/VDO sentence wrapper and extracts basic
// position report data from message types 1, 2, and 3.
func (p *Parser) ParseAIS(data []byte) (*models.EntityPosition, error) {
	line := strings.TrimSpace(string(data))

	// Strip checksum.
	if idx := strings.Index(line, "*"); idx > 0 {
		line = line[:idx]
	}

	fields := strings.Split(line, ",")
	if len(fields) < 7 {
		return nil, fmt.Errorf("AIS sentence too short: %d fields", len(fields))
	}

	// Verify this is a VDM or VDO sentence.
	sentenceType := fields[0]
	if !strings.HasSuffix(sentenceType, "VDM") && !strings.HasSuffix(sentenceType, "VDO") {
		return nil, fmt.Errorf("not an AIS VDM/VDO sentence: %s", sentenceType)
	}

	// Multi-sentence messages are not supported in this basic parser.
	totalFragments := fields[1]
	if totalFragments != "1" {
		return nil, fmt.Errorf("multi-fragment AIS messages not supported (fragment count: %s)", totalFragments)
	}

	// Decode the AIS payload (6-bit ASCII armored).
	payload := fields[5]
	if len(payload) == 0 {
		return nil, fmt.Errorf("empty AIS payload")
	}

	bits := decodeAISPayload(payload)
	if len(bits) < 168 {
		return nil, fmt.Errorf("AIS payload too short for position report: %d bits", len(bits))
	}

	// Message type is first 6 bits.
	msgType := bitsToUint(bits, 0, 6)
	if msgType < 1 || msgType > 3 {
		return nil, fmt.Errorf("unsupported AIS message type: %d (only types 1-3 supported)", msgType)
	}

	mmsi := bitsToUint(bits, 8, 30)
	sog := float64(bitsToUint(bits, 50, 10)) / 10.0
	lon := float64(bitsToInt(bits, 61, 28)) / 600000.0
	lat := float64(bitsToInt(bits, 89, 27)) / 600000.0
	cog := float64(bitsToUint(bits, 116, 12)) / 10.0
	heading := float64(bitsToUint(bits, 128, 9))

	if heading == 511 {
		heading = cog
	}

	return &models.EntityPosition{
		EntityID:   fmt.Sprintf("MMSI-%d", mmsi),
		EntityType: models.EntityTypeVessel,
		Name:       fmt.Sprintf("MMSI %d", mmsi),
		Latitude:   lat,
		Longitude:  lon,
		Heading:    heading,
		SpeedKnots: sog,
		Course:     cog,
		Timestamp:  time.Now().UTC(),
		RawData:    data,
	}, nil
}

// decodeAISPayload converts 6-bit ASCII armored AIS payload to a bit array.
func decodeAISPayload(payload string) []byte {
	bits := make([]byte, 0, len(payload)*6)
	for _, c := range payload {
		val := int(c) - 48
		if val > 40 {
			val -= 8
		}
		for i := 5; i >= 0; i-- {
			if val&(1<<i) != 0 {
				bits = append(bits, 1)
			} else {
				bits = append(bits, 0)
			}
		}
	}
	return bits
}

// bitsToUint extracts an unsigned integer from a bit array.
func bitsToUint(bits []byte, start, length int) uint32 {
	var result uint32
	for i := 0; i < length && start+i < len(bits); i++ {
		result = (result << 1) | uint32(bits[start+i])
	}
	return result
}

// bitsToInt extracts a signed integer from a bit array (two's complement).
func bitsToInt(bits []byte, start, length int) int32 {
	val := bitsToUint(bits, start, length)
	if bits[start] == 1 {
		val |= ^((1 << length) - 1)
	}
	return int32(val)
}

// truncate shortens a string to maxLen characters, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
