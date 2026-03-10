package ingest

import (
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sentinel/ingest-service/internal/models"
)

// Parser converts raw sensor data from multiple formats into the canonical
// EntityPosition model. It supports JSON, NMEA 0183, Cursor on Target (CoT),
// AIS, ADS-B, and Link 16 message formats.
type Parser struct {
	mu              sync.Mutex
	aisFragments    map[string][]string   // keyed by sequential message ID
	aisFragmentTime map[string]time.Time  // arrival time for eviction
}

// NewParser creates a new multi-format message parser and starts a background
// goroutine to evict stale AIS fragment buffers.
func NewParser() *Parser {
	p := &Parser{
		aisFragments:    make(map[string][]string),
		aisFragmentTime: make(map[string]time.Time),
	}
	go p.evictStaleFragments()
	return p
}

// evictStaleFragments removes AIS fragment buffers older than 10 seconds.
func (p *Parser) evictStaleFragments() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		p.mu.Lock()
		now := time.Now()
		for k, t := range p.aisFragmentTime {
			if now.Sub(t) > 10*time.Second {
				delete(p.aisFragments, k)
				delete(p.aisFragmentTime, k)
			}
		}
		p.mu.Unlock()
	}
}

// ParseFormat dispatches to a specific parser by format string, skipping
// auto-detection. Falls back to ParseGeneric for unknown formats.
func (p *Parser) ParseFormat(format, sourceType string, data []byte) (*models.EntityPosition, error) {
	var entity *models.EntityPosition
	var err error

	switch format {
	case "json":
		entity, err = p.ParseJSON(data)
	case "nmea":
		entity, err = p.ParseNMEA(data)
	case "cot":
		entity, err = p.ParseCoT(data)
	case "ais":
		entity, err = p.ParseAIS(data)
	case "adsb":
		entity, err = p.ParseADSB(data)
	case "link16":
		entity, err = p.ParseLink16(data)
	default:
		return p.ParseGeneric(sourceType, data)
	}

	if err != nil {
		return nil, err
	}
	entity.Source = sourceType
	return entity, nil
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

	// Check for ADS-B SBS/BaseStation format (from dump1090 / FlightAware).
	if strings.HasPrefix(trimmed, "MSG,") {
		entity, err := p.ParseADSB(data)
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

	// Check for JREAP-C / Link 16 binary data (falls through all text checks).
	if len(data) >= 16 && isJREAPCHeader(data) {
		entity, err := p.ParseLink16(data)
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

	// Compute velocity components from speed (m/s) and course
	speedMS := event.Detail.Track.Speed
	courseRad := event.Detail.Track.Course * math.Pi / 180.0
	velNorth := speedMS * math.Cos(courseRad)
	velEast := speedMS * math.Sin(courseRad)

	// Infer track environment from CoT type dimension code
	trackEnv := cotDimensionToTrackEnvironment(event.Type)

	cotData := &models.CoTData{
		UID:       event.UID,
		CoTType:   event.Type,
		How:       event.How,
		CE:        event.Point.CE,
		LE:        event.Point.LE,
		StaleTime: event.Stale,
	}

	return &models.EntityPosition{
		EntityID:         event.UID,
		EntityType:       entityType,
		Name:             name,
		Latitude:         event.Point.Lat,
		Longitude:        event.Point.Lon,
		Altitude:         event.Point.Hae,
		Heading:          event.Detail.Track.Course,
		SpeedKnots:       speedKnots,
		Course:           event.Detail.Track.Course,
		Timestamp:        ts,
		RawData:          data,
		TrackEnvironment: trackEnv,
		CircularError:    event.Point.CE,
		VelocityNorth:    velNorth,
		VelocityEast:     velEast,
		CoTData:          cotData,
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
// Supports single-fragment messages (types 1-3) and multi-fragment messages
// (type 5 static/voyage data). Multi-fragment messages are buffered and
// reassembled before parsing.
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

	totalFragments := fields[1]
	fragmentNum := fields[2]
	seqMsgID := fields[3]
	payload := fields[5]

	if len(payload) == 0 {
		return nil, fmt.Errorf("empty AIS payload")
	}

	// Single-fragment message: parse immediately.
	if totalFragments == "1" {
		return p.parseAISPayload(payload, data)
	}

	// Multi-fragment message: buffer and reassemble.
	p.mu.Lock()
	defer p.mu.Unlock()

	total, err := strconv.Atoi(totalFragments)
	if err != nil || total < 2 {
		return nil, fmt.Errorf("invalid AIS fragment count: %s", totalFragments)
	}

	fragIdx, err := strconv.Atoi(fragmentNum)
	if err != nil || fragIdx < 1 || fragIdx > total {
		return nil, fmt.Errorf("invalid AIS fragment number: %s", fragmentNum)
	}

	key := seqMsgID
	if _, exists := p.aisFragments[key]; !exists {
		p.aisFragments[key] = make([]string, total)
		p.aisFragmentTime[key] = time.Now()
	}

	frags := p.aisFragments[key]
	if len(frags) != total {
		// Fragment count mismatch — discard old buffer and start fresh.
		frags = make([]string, total)
		p.aisFragments[key] = frags
		p.aisFragmentTime[key] = time.Now()
	}

	frags[fragIdx-1] = payload

	// Check if all fragments have arrived.
	for _, f := range frags {
		if f == "" {
			return nil, fmt.Errorf("AIS fragment %d/%d buffered, waiting for remaining", fragIdx, total)
		}
	}

	// All fragments received — reassemble and parse.
	fullPayload := strings.Join(frags, "")
	delete(p.aisFragments, key)
	delete(p.aisFragmentTime, key)

	return p.parseAISPayload(fullPayload, data)
}

// parseAISPayload decodes an AIS payload and dispatches by message type.
func (p *Parser) parseAISPayload(payload string, rawData []byte) (*models.EntityPosition, error) {
	bits := decodeAISPayload(payload)
	if len(bits) < 6 {
		return nil, fmt.Errorf("AIS payload too short: %d bits", len(bits))
	}

	msgType := bitsToUint(bits, 0, 6)

	switch {
	case msgType >= 1 && msgType <= 3:
		return p.parseAISPositionReport(bits, rawData)
	case msgType == 5:
		return p.parseAISType5(bits, rawData)
	default:
		return nil, fmt.Errorf("unsupported AIS message type: %d", msgType)
	}
}

// parseAISPositionReport extracts position data from AIS message types 1-3.
func (p *Parser) parseAISPositionReport(bits []byte, rawData []byte) (*models.EntityPosition, error) {
	if len(bits) < 168 {
		return nil, fmt.Errorf("AIS position report too short: %d bits (need 168)", len(bits))
	}

	msgType := bitsToUint(bits, 0, 6)
	repeatIndicator := bitsToUint(bits, 6, 2)
	mmsi := bitsToUint(bits, 8, 30)
	navStatus := bitsToUint(bits, 38, 4)
	rot := float64(bitsToInt(bits, 42, 8))
	sog := float64(bitsToUint(bits, 50, 10)) / 10.0
	posAccuracy := bitsToUint(bits, 60, 1)
	lon := float64(bitsToInt(bits, 61, 28)) / 600000.0
	lat := float64(bitsToInt(bits, 89, 27)) / 600000.0
	cog := float64(bitsToUint(bits, 116, 12)) / 10.0
	heading := float64(bitsToUint(bits, 128, 9))
	specialManoeuvre := bitsToUint(bits, 143, 2)

	trueHeading := heading
	if heading == 511 {
		heading = cog
	}

	mmsiStr := fmt.Sprintf("%d", mmsi)

	aisData := &models.AISData{
		MMSI:                 mmsiStr,
		NavStatus:            aisNavStatusString(navStatus),
		RateOfTurn:           rot,
		SpeedOverGround:      sog,
		CourseOverGround:     cog,
		TrueHeading:          trueHeading,
		PositionAccuracyHigh: posAccuracy == 1,
		SpecialManoeuvre:     specialManoeuvre == 1,
		MessageType:          int(msgType),
		RepeatIndicator:      int(repeatIndicator),
	}

	// Derive flag from MMSI MID (first 3 digits for 9-digit MMSI)
	if len(mmsiStr) == 9 {
		aisData.Flag = mmsiMIDToCountry(mmsiStr[:3])
	}

	// Circular error: DGPS ~1m, else ~10m
	ce := 10.0
	if posAccuracy == 1 {
		ce = 1.0
	}

	return &models.EntityPosition{
		EntityID:         fmt.Sprintf("MMSI-%d", mmsi),
		EntityType:       models.EntityTypeVessel,
		Name:             fmt.Sprintf("MMSI %d", mmsi),
		Latitude:         lat,
		Longitude:        lon,
		Heading:          heading,
		SpeedKnots:       sog,
		Course:           cog,
		Timestamp:        time.Now().UTC(),
		RawData:          rawData,
		TrackEnvironment: "SEA_SURFACE",
		CircularError:    ce,
		AISData:          aisData,
	}, nil
}

// parseAISType5 extracts static and voyage data from AIS message type 5.
// Type 5 messages are 424 bits and always span 2 fragments. They contain
// vessel name, callsign, dimensions, and voyage data but no position.
func (p *Parser) parseAISType5(bits []byte, rawData []byte) (*models.EntityPosition, error) {
	if len(bits) < 424 {
		return nil, fmt.Errorf("AIS type 5 too short: %d bits (need 424)", len(bits))
	}

	mmsi := bitsToUint(bits, 8, 30)
	imo := bitsToUint(bits, 40, 30)
	callsign := decodeAIS6BitText(bits, 70, 42)
	vesselName := decodeAIS6BitText(bits, 112, 120)
	shipType := bitsToUint(bits, 232, 8)
	dimA := float64(bitsToUint(bits, 240, 9))
	dimB := float64(bitsToUint(bits, 249, 9))
	dimC := float64(bitsToUint(bits, 258, 6))
	dimD := float64(bitsToUint(bits, 264, 6))
	draught := float64(bitsToUint(bits, 294, 8)) / 10.0
	destination := decodeAIS6BitText(bits, 302, 120)

	name := vesselName
	if name == "" {
		name = callsign
	}
	if name == "" {
		name = fmt.Sprintf("MMSI %d", mmsi)
	}

	mmsiStr := fmt.Sprintf("%d", mmsi)
	lengthOverall := dimA + dimB
	beam := dimC + dimD

	aisData := &models.AISData{
		MMSI:          mmsiStr,
		Callsign:      callsign,
		VesselName:    vesselName,
		ShipType:      int(shipType),
		ShipTypeName:  aisShipTypeName(shipType),
		DimensionA:    dimA,
		DimensionB:    dimB,
		DimensionC:    dimC,
		DimensionD:    dimD,
		LengthOverall: lengthOverall,
		Beam:          beam,
		Draught:       draught,
		Destination:   destination,
		MessageType:   5,
	}

	if imo > 0 {
		aisData.IMO = fmt.Sprintf("%d", imo)
	}
	if len(mmsiStr) == 9 {
		aisData.Flag = mmsiMIDToCountry(mmsiStr[:3])
	}

	return &models.EntityPosition{
		EntityID:         fmt.Sprintf("MMSI-%d", mmsi),
		EntityType:       models.EntityTypeVessel,
		Name:             name,
		Latitude:         0,
		Longitude:        0,
		Timestamp:        time.Now().UTC(),
		RawData:          rawData,
		TrackEnvironment: "SEA_SURFACE",
		DimensionLength:  lengthOverall,
		DimensionWidth:   beam,
		AISData:          aisData,
	}, nil
}

// decodeAIS6BitText decodes AIS 6-bit ASCII text from a bit array.
// Values 0-31 map to @A-Z[\]^_, values 32-63 map to  !"#$...0-9:;<=>?.
func decodeAIS6BitText(bits []byte, start, bitLength int) string {
	numChars := bitLength / 6
	result := make([]byte, 0, numChars)

	for i := 0; i < numChars; i++ {
		offset := start + i*6
		if offset+6 > len(bits) {
			break
		}
		val := bitsToUint(bits, offset, 6)
		var ch byte
		if val < 32 {
			ch = byte(val) + '@' // 0→@, 1→A, ... 26→Z
		} else {
			ch = byte(val) + ' ' - 32 // 32→ , 33→!, ... 48→0, ... 57→9
		}
		result = append(result, ch)
	}

	// Trim trailing @ and spaces.
	return strings.TrimRight(string(result), "@ ")
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

// ParseADSB parses ADS-B messages in SBS/BaseStation format (dump1090 output).
// Only transmission types 2 (surface position) and 3 (airborne position) carry
// latitude/longitude and are supported.
func (p *Parser) ParseADSB(data []byte) (*models.EntityPosition, error) {
	line := strings.TrimSpace(string(data))
	fields := strings.Split(line, ",")
	if len(fields) < 22 {
		return nil, fmt.Errorf("ADS-B SBS message too short: %d fields (need 22)", len(fields))
	}

	// Transmission type must be 2 (surface) or 3 (airborne) for position data.
	txType := strings.TrimSpace(fields[1])
	if txType != "2" && txType != "3" {
		return nil, fmt.Errorf("ADS-B transmission type %s has no position data (only types 2/3 supported)", txType)
	}

	icaoHex := strings.TrimSpace(fields[4])
	if icaoHex == "" {
		return nil, fmt.Errorf("ADS-B message missing ICAO hex address")
	}

	lat, err := strconv.ParseFloat(strings.TrimSpace(fields[14]), 64)
	if err != nil {
		return nil, fmt.Errorf("parsing ADS-B latitude: %w", err)
	}

	lon, err := strconv.ParseFloat(strings.TrimSpace(fields[15]), 64)
	if err != nil {
		return nil, fmt.Errorf("parsing ADS-B longitude: %w", err)
	}

	// Altitude in feet → meters.
	var altitude float64
	if alt := strings.TrimSpace(fields[11]); alt != "" {
		if v, err := strconv.ParseFloat(alt, 64); err == nil {
			altitude = v * 0.3048
		}
	}

	// Speed (already in knots).
	var speedKnots float64
	if spd := strings.TrimSpace(fields[12]); spd != "" {
		speedKnots, _ = strconv.ParseFloat(spd, 64)
	}

	// Track angle (heading/course).
	var heading float64
	if trk := strings.TrimSpace(fields[13]); trk != "" {
		heading, _ = strconv.ParseFloat(trk, 64)
	}

	// Callsign for name, fallback to ICAO hex.
	name := strings.TrimSpace(fields[10])
	if name == "" {
		name = fmt.Sprintf("ICAO %s", icaoHex)
	}

	// Vertical rate (field 16) in ft/min → m/s.
	var verticalRate float64
	if vr := strings.TrimSpace(fields[16]); vr != "" {
		if v, err := strconv.ParseFloat(vr, 64); err == nil {
			verticalRate = v * 0.00508 // ft/min → m/s
		}
	}

	// Squawk (field 17)
	squawk := strings.TrimSpace(fields[17])

	// Ground flag (field 21)
	onGround := strings.TrimSpace(fields[21]) == "-1"

	// Callsign from field 10 as aircraftId
	aircraftId := strings.TrimSpace(fields[10])

	ts := parseADSBTimestamp(strings.TrimSpace(fields[6]), strings.TrimSpace(fields[7]))

	adsbData := &models.ADSBData{
		ICAOHex:      icaoHex,
		Squawk:       squawk,
		AltitudeBaro: altitude,
		VerticalRate: verticalRate,
		OnGround:     onGround,
		AircraftID:   aircraftId,
		GroundSpeed:  speedKnots,
	}

	// Decode emergency from squawk
	switch squawk {
	case "7500":
		adsbData.Emergency = "HIJACK"
	case "7600":
		adsbData.Emergency = "RADIO_FAILURE"
	case "7700":
		adsbData.Emergency = "GENERAL_EMERGENCY"
	}

	entity := &models.EntityPosition{
		EntityID:         fmt.Sprintf("ICAO-%s", icaoHex),
		EntityType:       models.EntityTypeAircraft,
		Name:             name,
		Latitude:         lat,
		Longitude:        lon,
		Altitude:         altitude,
		Heading:          heading,
		SpeedKnots:       speedKnots,
		Course:           heading,
		Timestamp:        ts,
		RawData:          data,
		TrackEnvironment: "AIR",
		VelocityUp:       verticalRate,
		ADSBData:         adsbData,
	}

	return entity, nil
}

// parseADSBTimestamp parses SBS date and time fields ("2006/01/02" + "15:04:05.000").
// Falls back to current time if parsing fails.
func parseADSBTimestamp(dateStr, timeStr string) time.Time {
	if dateStr == "" || timeStr == "" {
		return time.Now().UTC()
	}

	combined := dateStr + " " + timeStr

	// Try with milliseconds first.
	if t, err := time.Parse("2006/01/02 15:04:05.000", combined); err == nil {
		return t.UTC()
	}

	// Try without milliseconds.
	if t, err := time.Parse("2006/01/02 15:04:05", combined); err == nil {
		return t.UTC()
	}

	return time.Now().UTC()
}

// ParseLink16 parses a JREAP-C encapsulated Link 16 binary message containing
// J-series track data. Extracts position, kinematics, and entity classification
// from the J-series label.
func (p *Parser) ParseLink16(data []byte) (*models.EntityPosition, error) {
	if len(data) < 16 {
		return nil, fmt.Errorf("JREAP-C message too short for header: %d bytes", len(data))
	}

	if !isJREAPCHeader(data) {
		return nil, fmt.Errorf("invalid JREAP-C header")
	}

	totalLen := binary.BigEndian.Uint16(data[2:4])

	// J-series track data starts at byte 16.
	trackOffset := 16
	if int(totalLen) < trackOffset+20 || len(data) < trackOffset+20 {
		return nil, fmt.Errorf("JREAP-C payload too short for track data: need %d bytes at offset %d", 20, trackOffset)
	}

	trackData := data[trackOffset:]

	jLabel := binary.BigEndian.Uint16(trackData[0:2])
	trackNumber := binary.BigEndian.Uint16(trackData[4:6])

	latRaw := int32(binary.BigEndian.Uint32(trackData[6:10]))
	lonRaw := int32(binary.BigEndian.Uint32(trackData[10:14]))
	altRaw := int16(binary.BigEndian.Uint16(trackData[14:16]))
	spdRaw := binary.BigEndian.Uint16(trackData[16:18])
	hdgRaw := binary.BigEndian.Uint16(trackData[18:20])

	lat := float64(latRaw) * 90.0 / math.Pow(2, 23)
	lon := float64(lonRaw) * 180.0 / math.Pow(2, 23)
	alt := float64(altRaw) * 3.048
	speed := float64(spdRaw) * 0.1
	heading := float64(hdgRaw) * 360.0 / 65536.0

	entityType := classifyJSeriesLabel(jLabel)
	trackEnv := jSeriesLabelToTrackEnvironment(jLabel)

	link16Data := &models.Link16Data{
		TrackNumber:  int(trackNumber),
		JSeriesLabel: fmt.Sprintf("%04X", jLabel),
	}

	return &models.EntityPosition{
		EntityID:         fmt.Sprintf("JTN-%d", trackNumber),
		EntityType:       entityType,
		Name:             fmt.Sprintf("JTN %d", trackNumber),
		Latitude:         lat,
		Longitude:        lon,
		Altitude:         alt,
		Heading:          heading,
		SpeedKnots:       speed,
		Course:           heading,
		Timestamp:        time.Now().UTC(),
		RawData:          data,
		TrackEnvironment: trackEnv,
		Link16Data:       link16Data,
	}, nil
}

// isJREAPCHeader validates a byte slice as a plausible JREAP-C header.
// Checks version (1-2), message type (1-16), and length field consistency.
func isJREAPCHeader(data []byte) bool {
	if len(data) < 16 {
		return false
	}

	version := data[0]
	if version < 1 || version > 2 {
		return false
	}

	msgType := data[1]
	if msgType < 1 || msgType > 16 {
		return false
	}

	totalLen := binary.BigEndian.Uint16(data[2:4])
	if totalLen < 16 || int(totalLen) > len(data) {
		return false
	}

	return true
}

// classifyJSeriesLabel maps a J-series label to an entity type.
// Major byte 0x02 = air, 0x03 with minor 0x02 = surface, 0x03 with minor 0x05 = land.
func classifyJSeriesLabel(label uint16) string {
	majorByte := label >> 8

	switch {
	case majorByte == 0x02:
		return models.EntityTypeAircraft
	case label == 0x0302:
		return models.EntityTypeVessel
	case label == 0x0305:
		return models.EntityTypeVehicle
	default:
		return models.EntityTypeUnknown
	}
}

// truncate shortens a string to maxLen characters, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// aisNavStatusString maps AIS navigational status codes to string constants.
func aisNavStatusString(status uint32) string {
	switch status {
	case 0:
		return "UNDER_WAY_USING_ENGINE"
	case 1:
		return "AT_ANCHOR"
	case 2:
		return "NOT_UNDER_COMMAND"
	case 3:
		return "RESTRICTED_MANOEUVRABILITY"
	case 4:
		return "CONSTRAINED_BY_DRAUGHT"
	case 5:
		return "MOORED"
	case 6:
		return "AGROUND"
	case 7:
		return "ENGAGED_IN_FISHING"
	case 8:
		return "UNDER_WAY_SAILING"
	case 14:
		return "AIS_SART"
	default:
		return "UNKNOWN"
	}
}

// aisShipTypeName maps AIS ship type codes (0-99) to human-readable names.
func aisShipTypeName(code uint32) string {
	switch {
	case code >= 20 && code <= 29:
		return "Wing in Ground"
	case code == 30:
		return "Fishing"
	case code == 31 || code == 32:
		return "Towing"
	case code == 33:
		return "Dredging"
	case code == 34:
		return "Diving Operations"
	case code == 35:
		return "Military Operations"
	case code == 36:
		return "Sailing"
	case code == 37:
		return "Pleasure Craft"
	case code >= 40 && code <= 49:
		return "High Speed Craft"
	case code == 50:
		return "Pilot Vessel"
	case code == 51:
		return "Search and Rescue"
	case code == 52:
		return "Tug"
	case code == 53:
		return "Port Tender"
	case code == 55:
		return "Law Enforcement"
	case code >= 60 && code <= 69:
		return "Passenger"
	case code >= 70 && code <= 79:
		return "Cargo"
	case code >= 80 && code <= 89:
		return "Tanker"
	default:
		return "Other"
	}
}

// mmsiMIDToCountry provides a basic mapping of MMSI MID codes to ISO 3166-1
// alpha-2 country codes for a subset of common maritime nations.
func mmsiMIDToCountry(mid string) string {
	midMap := map[string]string{
		"201": "GR", "211": "DE", "219": "DK", "220": "DK",
		"224": "ES", "225": "ES", "226": "FR", "227": "FR",
		"228": "FR", "229": "MT", "230": "FI", "231": "FO",
		"232": "GB", "233": "GB", "234": "GB", "235": "GB",
		"236": "GI", "237": "GR", "238": "HR", "239": "GR",
		"240": "GR", "241": "GR", "242": "MA", "243": "HU",
		"244": "NL", "245": "NL", "246": "NL", "247": "IT",
		"248": "MT", "249": "MT", "250": "IE", "255": "PT",
		"256": "MT", "257": "NO", "258": "NO", "259": "NO",
		"261": "PL", "263": "PT", "265": "SE", "266": "SE",
		"269": "CH", "270": "CZ", "271": "TR", "272": "UA",
		"273": "RU", "274": "MK", "275": "LV", "276": "EE",
		"277": "LT", "278": "SI", "279": "RS",
		"303": "US", "338": "US", "366": "US", "367": "US",
		"368": "US", "369": "US",
		"316": "CA",
		"401": "AF", "412": "CN", "413": "CN", "414": "CN",
		"431": "JP", "432": "JP", "440": "KR", "441": "KR",
		"501": "FR", "503": "AU", "506": "MM",
		"508": "MV", "510": "NZ", "512": "NZ",
		"525": "ID", "533": "MY", "548": "PH",
		"563": "SG", "564": "SG", "565": "SG", "566": "SG",
		"567": "TH",
		"601": "ZA", "603": "AO", "605": "DZ",
		"636": "LR", "637": "LR",
		"710": "BR", "720": "BO", "725": "CL", "730": "CO",
		"735": "EC", "740": "FK",
		"760": "MX",
	}
	if c, ok := midMap[mid]; ok {
		return c
	}
	return ""
}

// cotDimensionToTrackEnvironment infers track environment from CoT type dimension code.
func cotDimensionToTrackEnvironment(cotType string) string {
	parts := strings.Split(cotType, "-")
	if len(parts) < 3 {
		return "UNKNOWN"
	}
	switch strings.ToUpper(parts[2]) {
	case "A":
		return "AIR"
	case "S":
		return "SEA_SURFACE"
	case "G":
		return "GROUND"
	case "U":
		return "SUBSURFACE"
	default:
		return "UNKNOWN"
	}
}

// jSeriesLabelToTrackEnvironment infers track environment from J-series label.
func jSeriesLabelToTrackEnvironment(label uint16) string {
	majorByte := label >> 8
	switch {
	case majorByte == 0x02:
		return "AIR"
	case label == 0x0302:
		return "SEA_SURFACE"
	case label == 0x0305:
		return "GROUND"
	default:
		return "UNKNOWN"
	}
}
