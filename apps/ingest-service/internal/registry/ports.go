package registry

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

// DefaultReservedPorts are the ports the Sentinel stack itself publishes
// (gateway and services, client, Postgres, Keycloak, MinIO, Kafka,
// OpenSearch, GeoServer, analytics, ingest management). A listening
// connection may not take them.
const DefaultReservedPorts = "3000-3005,4000,4010,4200,5000,5001,5432,8080,8600,9000,9001,9092,9093,9200,9600"

// PortSet is a set of port numbers.
type PortSet map[int]bool

// ParsePortSet parses "3000-3005,5432".
func ParsePortSet(spec string) (PortSet, error) {
	out := PortSet{}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			return nil, fmt.Errorf("reserved ports: %q is not a port", part)
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(strings.TrimSpace(hi)); err != nil {
				return nil, fmt.Errorf("reserved ports: %q is not a range", part)
			}
		}
		if a < 1 || b > 65535 || a > b {
			return nil, fmt.Errorf("reserved ports: %q is out of range", part)
		}
		for p := a; p <= b; p++ {
			out[p] = true
		}
	}
	return out, nil
}

// CheckPorts validates the port a listening connection binds: 1024-65535,
// not one of Sentinel's own, and not used by another listening connection
// of the same protocol (enabled or not, so enabling both later cannot
// clash). Connections that do not listen have nothing to check.
func CheckPorts(f Feed, others []Feed, reserved PortSet) []Problem {
	network, port, ok := driver.ListenOf(f.Driver, f.Config)
	if !ok {
		return nil
	}
	path := "/config/port"
	if f.Driver == "stream" {
		path = "/config/transport/address"
	}
	if port < 1024 || port > 65535 {
		return []Problem{{Path: path, Message: fmt.Sprintf("port %d is outside 1024-65535; ports below 1024 need root on the listener host", port)}}
	}
	if reserved[port] {
		return []Problem{{Path: path, Message: fmt.Sprintf("port %d is used by Sentinel itself", port)}}
	}
	for _, o := range others {
		if o.ID == f.ID {
			continue
		}
		if n, p, ok := driver.ListenOf(o.Driver, o.Config); ok && n == network && p == port {
			return []Problem{{Path: path, Message: fmt.Sprintf("port %d/%s is used by connection %s", port, network, o.ID)}}
		}
	}
	return nil
}

// CheckAllow validates a listening connection's allowlist (IPs or CIDRs):
// allow on tcp and udp, transport.allow on stream. A value that is not an
// array of strings is refused too. Connections that do not listen have no
// allowlist to check.
func CheckAllow(f Feed) []Problem {
	if _, _, listens := driver.ListenOf(f.Driver, f.Config); !listens {
		return nil
	}
	path, keys := "/config/allow", []string{"allow"}
	if f.Driver == "stream" {
		path, keys = "/config/transport/allow", []string{"transport", "allow"}
	}
	raw := rawAt(f.Config, keys...)
	if raw == nil {
		return nil
	}
	var list []string
	if json.Unmarshal(raw, &list) != nil {
		return []Problem{{Path: path, Message: "must be an array of IP addresses or CIDRs"}}
	}
	if _, err := transport.ParseAllowlist(list); err != nil {
		return []Problem{{Path: path, Message: err.Error()}}
	}
	return nil
}

// rawAt returns the value under the nested object keys, or nil when it is
// absent or the path crosses something that is not an object.
func rawAt(raw json.RawMessage, keys ...string) json.RawMessage {
	for _, k := range keys {
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			return nil
		}
		if raw = obj[k]; raw == nil {
			return nil
		}
	}
	return raw
}
