package handler

import (
	"net/url"
	"reflect"
	"testing"
)

func TestParseSearchQuery(t *testing.T) {
	q, _ := url.ParseQuery("q=mason&types=VESSEL,AIRCRAFT&sources=AIS&sources=ADS_B&north=30&south=20&east=60&west=50&page=2&pageSize=5")
	got, err := parseSearchQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	if got.Q != "mason" || !reflect.DeepEqual(got.Types, []string{"VESSEL", "AIRCRAFT"}) || !reflect.DeepEqual(got.Sources, []string{"AIS", "ADS_B"}) {
		t.Errorf("filters = %+v", got)
	}
	if *got.North != 30 || *got.South != 20 || *got.East != 60 || *got.West != 50 || got.Page != 2 || got.PageSize != 5 {
		t.Errorf("bbox/paging = %+v", got)
	}
	defaults, _ := parseSearchQuery(url.Values{})
	if defaults.Page != 1 || defaults.PageSize != 20 || defaults.North != nil || defaults.Types != nil {
		t.Errorf("defaults = %+v", defaults)
	}
	for _, bad := range []string{"north=10&south=20", "east=10&west=20"} {
		q, _ := url.ParseQuery(bad)
		if _, err := parseSearchQuery(q); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestParseNearbyQuery(t *testing.T) {
	q, _ := url.ParseQuery("lat=25.5&lng=52.9&radiusKm=10&q=uss")
	got, err := parseNearbyQuery(q)
	if err != nil || got.Lat != 25.5 || got.Lng != 52.9 || got.RadiusKm != 10 || got.Q != "uss" || got.PageSize != 20 {
		t.Errorf("got %+v %v", got, err)
	}
	for _, bad := range []string{"", "lat=1&lng=2", "lat=91&lng=0&radiusKm=1", "lat=0&lng=181&radiusKm=1", "lat=0&lng=0&radiusKm=0.01", "lat=0&lng=0&radiusKm=99999", "lat=x&lng=0&radiusKm=1"} {
		q, _ := url.ParseQuery(bad)
		if _, err := parseNearbyQuery(q); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestParseStringSlice(t *testing.T) {
	q, _ := url.ParseQuery("types=A,%20B&types=C&types=")
	if got := parseStringSlice(q, "types"); !reflect.DeepEqual(got, []string{"A", "B", "C"}) {
		t.Errorf("got %v", got)
	}
	if parseStringSlice(url.Values{}, "types") != nil {
		t.Error("absent key should be nil")
	}
}
