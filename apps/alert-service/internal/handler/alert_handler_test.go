package handler

import (
	"net/url"
	"reflect"
	"testing"
)

func TestParseAlertQuery(t *testing.T) {
	q, _ := url.ParseQuery("severity=HIGH&entityId=e1&types=GEOFENCE_ENTRY,SPEED_ANOMALY&types=CUSTOM&acknowledged=false&page=3&pageSize=10")
	p := parseAlertQuery(q)
	if *p.Severity != "HIGH" || *p.EntityID != "e1" || p.Page != 3 || p.PageSize != 10 {
		t.Errorf("params = %+v", p)
	}
	if !reflect.DeepEqual(p.Types, []string{"GEOFENCE_ENTRY", "SPEED_ANOMALY", "CUSTOM"}) {
		t.Errorf("types = %v", p.Types)
	}
	if p.Acknowledged == nil || *p.Acknowledged {
		t.Errorf("acknowledged = %v", p.Acknowledged)
	}
	d := parseAlertQuery(url.Values{})
	if d.Page != 1 || d.PageSize != 20 || d.Severity != nil || d.EntityID != nil || d.Acknowledged != nil || d.Types != nil {
		t.Errorf("defaults = %+v", d)
	}
	q, _ = url.ParseQuery("acknowledged=true&page=abc")
	a := parseAlertQuery(q)
	if a.Acknowledged == nil || !*a.Acknowledged || a.Page != 1 {
		t.Errorf("ack/paging = %+v", a)
	}
}
