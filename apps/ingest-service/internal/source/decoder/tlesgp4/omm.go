package tlesgp4

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	satellite "github.com/joshuaferrara/go-satellite"
)

// OMM is one CCSDS Orbit Mean-Elements Message as CelesTrak serves it in
// JSON (FORMAT=json). The fixed OMM headers (CENTER_NAME, REF_FRAME,
// TIME_SYSTEM, MEAN_ELEMENT_THEORY) are omitted by CelesTrak as constant.
type OMM struct {
	ObjectName         string  `json:"OBJECT_NAME"`
	ObjectID           string  `json:"OBJECT_ID"`
	Epoch              string  `json:"EPOCH"`
	MeanMotion         float64 `json:"MEAN_MOTION"`
	Eccentricity       float64 `json:"ECCENTRICITY"`
	Inclination        float64 `json:"INCLINATION"`
	RAOfAscNode        float64 `json:"RA_OF_ASC_NODE"`
	ArgOfPericenter    float64 `json:"ARG_OF_PERICENTER"`
	MeanAnomaly        float64 `json:"MEAN_ANOMALY"`
	EphemerisType      int     `json:"EPHEMERIS_TYPE"`
	ClassificationType string  `json:"CLASSIFICATION_TYPE"`
	NoradCatID         int     `json:"NORAD_CAT_ID"`
	ElementSetNo       int     `json:"ELEMENT_SET_NO"`
	RevAtEpoch         int     `json:"REV_AT_EPOCH"`
	BStar              float64 `json:"BSTAR"`
	MeanMotionDot      float64 `json:"MEAN_MOTION_DOT"`
	MeanMotionDDot     float64 `json:"MEAN_MOTION_DDOT"`
}

// ParseOMM decodes a CelesTrak JSON array of OMM records. An empty array
// is a valid, empty result.
func ParseOMM(body []byte) ([]OMM, error) {
	var out []OMM
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("omm json: %w", err)
	}
	return out, nil
}

// EpochTime parses the OMM epoch, which CelesTrak writes without a zone.
func (o OMM) EpochTime() (time.Time, error) {
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", time.RFC3339Nano, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, o.Epoch); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("omm epoch %q", o.Epoch)
}

// Lines renders the record as a synthetic two-line element set, which is
// the only input the SGP4 library (and the client's) accepts. A catalog
// number above 99999 does not fit the five-column field and is written
// modulo 100000; the real number travels beside the lines. The lines are
// validated before use because the library aborts the process on a field
// it cannot parse.
func (o OMM) Lines() (line1, line2 string, err error) {
	epoch, err := o.EpochTime()
	if err != nil {
		return "", "", err
	}
	if math.Abs(o.MeanMotionDot) >= 1 {
		return "", "", fmt.Errorf("mean motion dot %v out of range", o.MeanMotionDot)
	}
	nddot, err := exponentField(o.MeanMotionDDot)
	if err != nil {
		return "", "", fmt.Errorf("mean motion ddot: %w", err)
	}
	bstar, err := exponentField(o.BStar)
	if err != nil {
		return "", "", fmt.Errorf("bstar: %w", err)
	}
	class := "U"
	if o.ClassificationType != "" {
		class = o.ClassificationType[:1]
	}
	ephem := o.EphemerisType
	if ephem < 0 || ephem > 9 {
		ephem = 0
	}
	elset := o.ElementSetNo
	if elset < 0 || elset > 9999 {
		elset = 9999
	}
	rev := o.RevAtEpoch
	if rev < 0 || rev > 99999 {
		rev = rev % 100000
		if rev < 0 {
			rev = 0
		}
	}
	satnum := o.NoradCatID % 100000
	if satnum < 0 {
		satnum = 0
	}
	dayOfYear := float64(epoch.YearDay()) + float64(epoch.Hour()*3600+epoch.Minute()*60+epoch.Second())/86400 + float64(epoch.Nanosecond())/86400e9

	l1 := fmt.Sprintf("1 %05d%s %-8s %02d%012.8f %s%s %s %s %d %4d",
		satnum, class, designator(o.ObjectID), epoch.Year()%100, dayOfYear,
		signChar(o.MeanMotionDot), strings.TrimPrefix(fmt.Sprintf("%.8f", math.Abs(o.MeanMotionDot)), "0"),
		nddot, bstar, ephem, elset)
	l2 := fmt.Sprintf("2 %05d %8.4f %8.4f %07d %8.4f %8.4f %11.8f%5d",
		satnum, o.Inclination, o.RAOfAscNode, int(math.Round(o.Eccentricity*1e7)),
		o.ArgOfPericenter, o.MeanAnomaly, o.MeanMotion, rev)
	if len(l1) != 68 || len(l2) != 68 {
		return "", "", fmt.Errorf("element fields out of range (%d/%d columns)", len(l1), len(l2))
	}
	l1 += strconv.Itoa(Checksum(l1))
	l2 += strconv.Itoa(Checksum(l2))
	if err := ValidateLines(l1, l2); err != nil {
		return "", "", err
	}
	return l1, l2, nil
}

// FromOMM builds a propagatable TLE from an OMM record.
func FromOMM(o OMM) (TLE, error) {
	l1, l2, err := o.Lines()
	if err != nil {
		return TLE{}, err
	}
	epoch, _ := o.EpochTime()
	return TLE{
		Name:    strings.TrimSpace(o.ObjectName),
		Line1:   l1,
		Line2:   l2,
		NoradID: o.NoradCatID,
		Epoch:   epoch,
		sat:     satellite.TLEToSat(l1, l2, satellite.GravityWGS84),
	}, nil
}

// Checksum is the TLE line checksum: digits count their value, a minus
// sign counts one, everything else zero, modulo 10. It takes the first 68
// columns (or the whole line when shorter).
func Checksum(line string) int {
	sum := 0
	for i, c := range line {
		if i >= 68 {
			break
		}
		switch {
		case c >= '0' && c <= '9':
			sum += int(c - '0')
		case c == '-':
			sum++
		}
	}
	return sum % 10
}

// ValidateLines checks that two lines have the shape the SGP4 library
// slices, that every numeric field it reads parses, and that the
// checksums match, so a bad element set is an error here rather than a
// process abort in the library.
func ValidateLines(line1, line2 string) error {
	if len(line1) != 69 || len(line2) != 69 {
		return fmt.Errorf("tle lines must be 69 columns (%d/%d)", len(line1), len(line2))
	}
	if line1[0] != '1' || line2[0] != '2' {
		return fmt.Errorf("tle line numbers")
	}
	if Checksum(line1) != int(line1[68]-'0') || Checksum(line2) != int(line2[68]-'0') {
		return fmt.Errorf("tle checksum mismatch")
	}
	ints := []string{line1[2:7], line1[18:20], line2[2:7]}
	for _, s := range ints {
		if _, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err != nil {
			return fmt.Errorf("tle field %q: %w", s, err)
		}
	}
	floats := []string{
		line1[20:32],
		strings.ReplaceAll(line1[33:43], " ", ""),
		strings.ReplaceAll(line1[44:45]+"."+line1[45:50]+"e"+line1[50:52], " ", ""),
		strings.ReplaceAll(line1[53:54]+"."+line1[54:59]+"e"+line1[59:61], " ", ""),
		line2[8:16], line2[17:25], "." + line2[26:33], line2[34:42], line2[43:51], line2[52:63],
	}
	for _, s := range floats {
		if _, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err != nil {
			return fmt.Errorf("tle field %q: %w", s, err)
		}
	}
	return nil
}

// exponentField writes a value as the TLE 8-column form "±MMMMM±E"
// meaning ±0.MMMMM × 10^(±E); zero is " 00000+0".
func exponentField(v float64) (string, error) {
	if v == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return " 00000+0", nil
	}
	abs := math.Abs(v)
	exp := int(math.Floor(math.Log10(abs))) + 1
	mant := int(math.Round(abs / math.Pow(10, float64(exp)) * 1e5))
	if mant >= 100000 {
		mant /= 10
		exp++
	}
	if exp > 9 || exp < -9 {
		return "", fmt.Errorf("value %g has no two-line representation", v)
	}
	expSign := "+"
	if exp < 0 {
		expSign = "-"
		exp = -exp
	}
	return fmt.Sprintf("%s%05d%s%d", signChar(v), mant, expSign, exp), nil
}

func signChar(v float64) string {
	if v < 0 {
		return "-"
	}
	return " "
}

// designator turns an OBJECT_ID like "1998-067A" into the TLE form
// "98067A"; an empty or unparsable id leaves the field blank.
func designator(objectID string) string {
	id := strings.TrimSpace(objectID)
	parts := strings.SplitN(id, "-", 2)
	if len(parts) != 2 || len(parts[0]) != 4 {
		return ""
	}
	piece := parts[1]
	if len(piece) > 6 {
		piece = piece[:6]
	}
	return parts[0][2:] + piece
}

// epochFromLine1 reads the epoch of a text TLE (columns 19-32).
func epochFromLine1(line1 string) (time.Time, bool) {
	if len(line1) < 32 {
		return time.Time{}, false
	}
	yy, err := strconv.Atoi(strings.TrimSpace(line1[18:20]))
	if err != nil {
		return time.Time{}, false
	}
	days, err := strconv.ParseFloat(strings.TrimSpace(line1[20:32]), 64)
	if err != nil || days < 1 {
		return time.Time{}, false
	}
	year := 1900 + yy
	if yy < 57 {
		year = 2000 + yy
	}
	start := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	return start.Add(time.Duration((days - 1) * 86400 * float64(time.Second))), true
}
