package tlesgp4

import (
	"math"
	"strings"
	"testing"
	"time"
)

// issOMM is the ISS element set of the text fixture as CelesTrak would
// serve it in JSON: 0.86671769 days into 2026-03-04 is 20:48:04.408416.
var issOMM = OMM{
	ObjectName: "ISS (ZARYA)", ObjectID: "1998-067A", Epoch: "2026-03-04T20:48:04.408416",
	MeanMotion: 15.48447334, Eccentricity: 0.0008177, Inclination: 51.6315, RAOfAscNode: 96.2009,
	ArgOfPericenter: 157.5272, MeanAnomaly: 202.6076, EphemerisType: 0, ClassificationType: "U",
	NoradCatID: 25544, ElementSetNo: 999, RevAtEpoch: 55558, BStar: 0.00017477, MeanMotionDot: 0.00009014, MeanMotionDDot: 0,
}

const issJSON = `[{"OBJECT_NAME":"ISS (ZARYA)","OBJECT_ID":"1998-067A","EPOCH":"2026-03-04T20:48:04.408416","MEAN_MOTION":15.48447334,"ECCENTRICITY":0.0008177,"INCLINATION":51.6315,"RA_OF_ASC_NODE":96.2009,"ARG_OF_PERICENTER":157.5272,"MEAN_ANOMALY":202.6076,"EPHEMERIS_TYPE":0,"CLASSIFICATION_TYPE":"U","NORAD_CAT_ID":25544,"ELEMENT_SET_NO":999,"REV_AT_EPOCH":55558,"BSTAR":0.00017477,"MEAN_MOTION_DOT":0.00009014,"MEAN_MOTION_DDOT":0}]`

func TestLinesFromOMMMatchTheTextSet(t *testing.T) {
	l1, l2, err := issOMM.Lines()
	if err != nil {
		t.Fatal(err)
	}
	if l1 != ISSLine1 {
		t.Errorf("line 1\n got %q\nwant %q", l1, ISSLine1)
	}
	if l2 != ISSLine2 {
		t.Errorf("line 2\n got %q\nwant %q", l2, ISSLine2)
	}
}

func TestLinesEdgeCases(t *testing.T) {
	t.Run("negative mean motion dot and zero bstar", func(t *testing.T) {
		o := issOMM
		o.MeanMotionDot = -0.00001234
		o.BStar = 0
		l1, _, err := o.Lines()
		if err != nil {
			t.Fatal(err)
		}
		if got := l1[33:43]; got != "-.00001234" {
			t.Errorf("ndot field = %q", got)
		}
		if got := l1[53:61]; got != " 00000+0" {
			t.Errorf("bstar field = %q", got)
		}
	})
	t.Run("six-digit catalog number keeps the real id", func(t *testing.T) {
		o := issOMM
		o.NoradCatID = 270001
		tle, err := FromOMM(o)
		if err != nil {
			t.Fatal(err)
		}
		if tle.Line1[2:7] != "70001" || tle.Line2[2:7] != "70001" {
			t.Errorf("placeholder satnum = %q / %q", tle.Line1[2:7], tle.Line2[2:7])
		}
		if tle.NoradID != 270001 {
			t.Errorf("NoradID = %d", tle.NoradID)
		}
		if o := Propagate(tle, time.Date(2026, 3, 4, 20, 0, 0, 0, time.UTC)); o == nil || o.SourceEntityID != "SAT-270001" || o.TLE.NoradID != 270001 {
			t.Errorf("observation keeps the real id: %+v", o)
		}
	})
	t.Run("empty object id leaves the designator blank", func(t *testing.T) {
		o := issOMM
		o.ObjectID = ""
		l1, _, err := o.Lines()
		if err != nil {
			t.Fatal(err)
		}
		if got := l1[9:17]; got != "        " {
			t.Errorf("designator field = %q", got)
		}
		if err := ValidateLines(l1, issOMMLine2(t)); err != nil {
			t.Errorf("blank designator must still validate: %v", err)
		}
	})
	t.Run("exponent carry", func(t *testing.T) {
		got, err := exponentField(0.99999999e-4)
		if err != nil || got != " 10000-3" {
			t.Errorf("carry = %q %v", got, err)
		}
		if got, _ := exponentField(-0.5); got != "-50000+0" {
			t.Errorf("negative = %q", got)
		}
		if got, _ := exponentField(12.5); got != " 12500+2" {
			t.Errorf("large = %q", got)
		}
		if _, err := exponentField(1e12); err == nil {
			t.Error("out-of-range exponent accepted")
		}
	})
	t.Run("bad epoch and out-of-range values are errors", func(t *testing.T) {
		o := issOMM
		o.Epoch = "yesterday"
		if _, _, err := o.Lines(); err == nil {
			t.Error("bad epoch accepted")
		}
		o = issOMM
		o.MeanMotionDot = 2
		if _, _, err := o.Lines(); err == nil {
			t.Error("mean motion dot >= 1 accepted")
		}
	})
}

func issOMMLine2(t *testing.T) string {
	t.Helper()
	_, l2, err := issOMM.Lines()
	if err != nil {
		t.Fatal(err)
	}
	return l2
}

func TestChecksumAndValidate(t *testing.T) {
	if got := Checksum(ISSLine1); got != 9 {
		t.Errorf("ISS line 1 checksum = %d, want 9", got)
	}
	if got := Checksum(ISSLine2); got != 5 {
		t.Errorf("ISS line 2 checksum = %d, want 5", got)
	}
	if err := ValidateLines(ISSLine1, ISSLine2); err != nil {
		t.Errorf("real lines rejected: %v", err)
	}
	bad := ISSLine1[:68] + "0"
	if err := ValidateLines(bad, ISSLine2); err == nil {
		t.Error("wrong checksum accepted")
	}
	if err := ValidateLines(ISSLine1[:60], ISSLine2); err == nil {
		t.Error("short line accepted")
	}
	alpha := "1 A0001U 98067A   26063.86671769  .00009014  00000+0  17477-3 0  999"
	alpha += string(rune('0' + Checksum(alpha)))
	if err := ValidateLines(alpha, ISSLine2); err == nil {
		t.Error("alpha-5 satnum accepted; the library would abort on it")
	}
}

func TestFromOMMPropagatesLikeTheTextSet(t *testing.T) {
	fromOMM, err := FromOMM(issOMM)
	if err != nil {
		t.Fatal(err)
	}
	fromText, err := New(ISSName, ISSLine1, ISSLine2)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 3, 4, 20, 0, 0, 0, time.UTC)
	a, b := Propagate(fromOMM, at), Propagate(fromText, at)
	if a == nil || b == nil {
		t.Fatal("nil observation")
	}
	if math.Abs(a.Latitude-b.Latitude) > 1e-6 || math.Abs(a.Longitude-b.Longitude) > 1e-6 || math.Abs(*a.Altitude-*b.Altitude) > 1 {
		t.Errorf("omm %v,%v,%v vs text %v,%v,%v", a.Latitude, a.Longitude, *a.Altitude, b.Latitude, b.Longitude, *b.Altitude)
	}
	if a.TLE.Epoch == "" || !strings.HasPrefix(a.TLE.Epoch, "2026-03-04T20:48:04") {
		t.Errorf("epoch on the observation = %q", a.TLE.Epoch)
	}
	if b.TLE.Epoch == "" || !strings.HasPrefix(b.TLE.Epoch, "2026-03-04T20:48:04") {
		t.Errorf("epoch from the text path = %q", b.TLE.Epoch)
	}
	if a.TLE.IntlDesignator != "98067A" || a.DisplayName != "ISS (ZARYA)" {
		t.Errorf("elements = %+v", a.TLE)
	}
}

func TestParseOMM(t *testing.T) {
	set, err := ParseOMM([]byte(issJSON))
	if err != nil || len(set) != 1 || set[0].NoradCatID != 25544 || set[0].ObjectID != "1998-067A" {
		t.Errorf("set = %+v err = %v", set, err)
	}
	if set, err := ParseOMM([]byte("[]")); err != nil || len(set) != 0 {
		t.Errorf("empty = %v %v", set, err)
	}
	if _, err := ParseOMM([]byte("<html>")); err == nil {
		t.Error("html accepted")
	}
}

func TestEpochFromLine1(t *testing.T) {
	got, ok := epochFromLine1(ISSLine1)
	want := time.Date(2026, 3, 4, 20, 48, 4, 408416000, time.UTC)
	if !ok || got.Sub(want).Abs() > time.Millisecond {
		t.Errorf("epoch = %v ok=%v, want %v", got, ok, want)
	}
	if _, ok := epochFromLine1("1 25544U"); ok {
		t.Error("short line parsed")
	}
}
