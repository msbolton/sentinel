package export

import (
	"encoding/xml"
	"fmt"
	"io"
	"sort"
)

const kmlNS = "http://www.opengis.net/kml/2.2"

type kml struct {
	XMLName  xml.Name `xml:"kml"`
	NS       string   `xml:"xmlns,attr"`
	Document *kmlDocument
	Link     *networkLink `xml:"NetworkLink,omitempty"`
}

type kmlDocument struct {
	XMLName    xml.Name     `xml:"Document"`
	Name       string       `xml:"name"`
	Placemarks []placemark  `xml:"Placemark"`
}

type placemark struct {
	ID           string        `xml:"id,attr"`
	Name         string        `xml:"name"`
	Description  string        `xml:"description,omitempty"`
	TimeStamp    *kmlTimeStamp `xml:"TimeStamp,omitempty"`
	ExtendedData *extendedData `xml:"ExtendedData,omitempty"`
	Point        kmlPoint      `xml:"Point"`
}

type kmlTimeStamp struct {
	When string `xml:"when"`
}

type extendedData struct {
	Data []kmlData `xml:"Data"`
}

type kmlData struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value"`
}

type kmlPoint struct {
	AltitudeMode string `xml:"altitudeMode,omitempty"`
	Coordinates  string `xml:"coordinates"`
}

type networkLink struct {
	Name string  `xml:"name"`
	Link kmlLink `xml:"Link"`
}

type kmlLink struct {
	Href            string `xml:"href"`
	RefreshMode     string `xml:"refreshMode"`
	RefreshInterval int    `xml:"refreshInterval"`
	ViewRefreshMode string `xml:"viewRefreshMode"`
	ViewRefreshTime int    `xml:"viewRefreshTime"`
	ViewFormat      string `xml:"viewFormat"`
}

// WriteKML renders entities with a position as Placemarks.
func WriteKML(w io.Writer, entities []Entity, title string) error {
	doc := &kmlDocument{Name: title}
	for _, e := range entities {
		if e.Position == nil {
			continue
		}
		pm := placemark{ID: e.ID, Name: e.Name, Point: kmlPoint{Coordinates: fmt.Sprintf("%.6f,%.6f", e.Position.Lon, e.Position.Lat)}}
		if e.Altitude != nil {
			pm.Point.Coordinates += fmt.Sprintf(",%.1f", *e.Altitude)
			pm.Point.AltitudeMode = "absolute"
		}
		if e.Description != nil {
			pm.Description = *e.Description
		}
		if e.LastObservedAt != nil {
			pm.TimeStamp = &kmlTimeStamp{When: *e.LastObservedAt}
		}
		props := properties(e)
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ed := &extendedData{}
		for _, k := range keys {
			ed.Data = append(ed.Data, kmlData{Name: k, Value: fmt.Sprint(props[k])})
		}
		pm.ExtendedData = ed
		doc.Placemarks = append(doc.Placemarks, pm)
	}
	return writeKML(w, kml{NS: kmlNS, Document: doc})
}

// WriteNetworkLink renders a document whose single NetworkLink refreshes
// href every refreshSec seconds and after the view stops moving, passing
// the view as bbox=west,south,east,north.
func WriteNetworkLink(w io.Writer, href, title string, refreshSec int) error {
	if refreshSec < 5 {
		refreshSec = 5
	}
	return writeKML(w, kml{NS: kmlNS, Link: &networkLink{
		Name: title,
		Link: kmlLink{
			Href: href, RefreshMode: "onInterval", RefreshInterval: refreshSec,
			ViewRefreshMode: "onStop", ViewRefreshTime: 2,
			ViewFormat: "bbox=[bboxWest],[bboxSouth],[bboxEast],[bboxNorth]",
		},
	}})
}

func writeKML(w io.Writer, doc kml) error {
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}
