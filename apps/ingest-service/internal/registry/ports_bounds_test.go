package registry_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/sentinel/ingest-service/internal/registry"
	_ "github.com/sentinel/ingest-service/internal/source/all"
	"github.com/sentinel/ingest-service/internal/source/driver"
)

// schemaPort digs the bounds of the port property out of a driver schema:
// properties.port, or, for the tcp driver, the listen branch of allOf, which is
// where the floor is stated for the listening side alone (connect may dial any port).
func schemaPort(t *testing.T, id string, listenOnly bool) (min, max float64) {
	t.Helper()
	def, ok := driver.Lookup(id)
	if !ok {
		t.Fatalf("driver %s is not registered", id)
	}
	var schema struct {
		Properties struct {
			Port struct{ Minimum, Maximum float64 } `json:"port"`
		} `json:"properties"`
		AllOf []struct {
			If struct {
				Properties struct {
					Mode struct{ Const string } `json:"mode"`
				} `json:"properties"`
			} `json:"if"`
			Then struct {
				Properties struct {
					Port struct{ Minimum float64 } `json:"port"`
				} `json:"properties"`
			} `json:"then"`
		} `json:"allOf"`
	}
	if err := json.Unmarshal(def.ConfigSchema, &schema); err != nil {
		t.Fatal(err)
	}
	min, max = schema.Properties.Port.Minimum, schema.Properties.Port.Maximum
	if listenOnly {
		min = 0
		for _, c := range schema.AllOf {
			if c.If.Properties.Mode.Const == "listen" {
				min = c.Then.Properties.Port.Minimum
			}
		}
	}
	return min, max
}

// The registry decides which ports a listening connection may take, and the tcp
// and udp drivers repeat its range in their code and their schemas. A range
// that drifts in one of them is a port the form accepts and the service refuses,
// or the other way round.
func TestListenPortBoundsAreTheRegistrys(t *testing.T) {
	for _, tc := range []struct {
		id         string
		listenOnly bool
		config     func(port int) string
	}{
		{"tcp", true, func(p int) string { return fmt.Sprintf(`{"mode":"listen","port":%d,"address":"127.0.0.1"}`, p) }},
		{"udp", false, func(p int) string { return fmt.Sprintf(`{"port":%d,"address":"127.0.0.1"}`, p) }},
	} {
		min, max := schemaPort(t, tc.id, tc.listenOnly)
		if int(min) != registry.MinPort || int(max) != registry.MaxPort {
			t.Errorf("%s schema: port bounds %v-%v, want the registry's %d-%d", tc.id, min, max, registry.MinPort, registry.MaxPort)
		}
		for port, ok := range map[int]bool{registry.MinPort - 1: false, registry.MinPort: true, registry.MaxPort: true, registry.MaxPort + 1: false} {
			_, err := driver.New(driver.Spec{FeedID: "f", Driver: tc.id, Config: json.RawMessage(tc.config(port))}, driver.Deps{})
			if ok && err != nil {
				t.Errorf("%s: port %d (inside %d-%d) refused: %v", tc.id, port, registry.MinPort, registry.MaxPort, err)
			}
			if !ok && err == nil {
				t.Errorf("%s: port %d (outside %d-%d) accepted", tc.id, port, registry.MinPort, registry.MaxPort)
			}
			if !ok && err != nil && !strings.Contains(err.Error(), fmt.Sprintf("%d-%d", registry.MinPort, registry.MaxPort)) {
				t.Errorf("%s: port %d refused with %q, which does not state the range %d-%d", tc.id, port, err, registry.MinPort, registry.MaxPort)
			}
		}
	}
}
