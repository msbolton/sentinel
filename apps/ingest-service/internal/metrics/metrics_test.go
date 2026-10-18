package metrics

import (
	"reflect"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// A collector declared on Metrics but never built in New is a nil pointer,
// and the first WithLabelValues on it crashes the process. Checking every
// collector field guards the ones added later as well.
func TestNewCreatesEveryCollector(t *testing.T) {
	collector := reflect.TypeOf((*prometheus.Collector)(nil)).Elem()
	m := reflect.ValueOf(*New(nil))
	for i := 0; i < m.NumField(); i++ {
		field := m.Type().Field(i)
		if field.Type.Implements(collector) && m.Field(i).IsZero() {
			t.Errorf("Metrics.%s is declared but New never creates it", field.Name)
		}
	}
}

// The supervisor increments these when it loses a lease or hands a feed to
// its preferred replica; they must be exported under the documented names,
// labelled by feed.
func TestLeaseCountersAreExported(t *testing.T) {
	m := New(nil)
	m.LeasesLost.WithLabelValues("a").Inc()
	m.LeaseHandovers.WithLabelValues("a").Inc()

	families, err := m.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string][]string{} // exported family -> labels of its series
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			for _, l := range metric.GetLabel() {
				labels[family.GetName()] = append(labels[family.GetName()], l.GetName()+"="+l.GetValue())
			}
		}
	}
	for _, name := range []string{"sentinel_ingest_leases_lost_total", "sentinel_ingest_lease_handovers_total"} {
		if got := labels[name]; !reflect.DeepEqual(got, []string{"feed_id=a"}) {
			t.Errorf("%s: gathered labels %v, want [feed_id=a]", name, got)
		}
	}
}

// The supervisor reports the feeds that have no replica to run on under this
// name, one series per reason; dashboards and alerts depend on both.
func TestFeedsUnplacedIsExported(t *testing.T) {
	m := New(nil)
	m.FeedsUnplaced.WithLabelValues("no_listener").Set(2)
	m.FeedsUnplaced.WithLabelValues("no_ordinary_replica").Set(3)

	families, err := m.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{} // reason -> value
	for _, family := range families {
		if family.GetName() != "sentinel_ingest_feeds_unplaced" {
			continue
		}
		for _, metric := range family.GetMetric() {
			if len(metric.GetLabel()) != 1 || metric.GetLabel()[0].GetName() != "reason" {
				t.Errorf("labels = %v, want only reason", metric.GetLabel())
				continue
			}
			got[metric.GetLabel()[0].GetValue()] = metric.GetGauge().GetValue()
		}
	}
	if want := map[string]float64{"no_listener": 2, "no_ordinary_replica": 3}; !reflect.DeepEqual(got, want) {
		t.Errorf("sentinel_ingest_feeds_unplaced = %v, want %v", got, want)
	}
}
