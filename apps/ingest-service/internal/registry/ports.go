package registry

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
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

// The ports a listening connection may bind. The floor is a policy, not a
// kernel limit: it leaves the well-known ports to the host's own services.
const (
	MinPort = 1024
	MaxPort = 65535
)

// SuggestFrom is the port the search for one to suggest starts at.
const SuggestFrom = 6000

// PortSet is a set of port numbers.
type PortSet map[int]bool

// PortRange is the ports From to To, both included; a single port has
// From == To.
type PortRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// Ranges lists the members of the set as ascending runs of consecutive ports.
// It is never nil, so that it marshals as [] for an empty set.
func (s PortSet) Ranges() []PortRange {
	ports := make([]int, 0, len(s))
	for p, member := range s {
		if member {
			ports = append(ports, p)
		}
	}
	slices.Sort(ports)
	out := []PortRange{}
	for _, p := range ports {
		if n := len(out); n > 0 && out[n-1].To+1 == p {
			out[n-1].To = p
		} else {
			out = append(out, PortRange{From: p, To: p})
		}
	}
	return out
}

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

// CheckPorts validates the port a listening connection binds: MinPort to
// MaxPort, not one of Sentinel's own, and not used by another listening
// connection of the same protocol (enabled or not, so enabling both later
// cannot clash). Connections that do not listen have nothing to check.
// PortsInUse and SuggestPort apply the same rules to the whole registry.
func CheckPorts(f Feed, others []Feed, reserved PortSet) []Problem {
	network, port, ok := driver.ListenOf(f.Driver, f.Config)
	if !ok {
		return nil
	}
	path := "/config/port"
	if f.Driver == "stream" {
		path = "/config/transport/address"
	}
	if port < MinPort || port > MaxPort {
		return []Problem{{Path: path, Message: fmt.Sprintf("port %d is outside %d-%d; %d is a policy floor, not a kernel limit, so the well-known ports stay with the host's own services", port, MinPort, MaxPort, MinPort)}}
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

// PortUse is one port a listening connection holds.
type PortUse struct {
	Protocol string `json:"protocol"` // tcp or udp
	Port     int    `json:"port"`
	FeedID   string `json:"feedId"`
	FeedName string `json:"feedName"`
	Enabled  bool   `json:"enabled"`
}

// PortsInUse lists the ports the listening connections among feeds bind,
// ordered by port, then protocol, then connection id. A disabled connection
// is listed, with Enabled false, because it keeps its port. Listeners are
// found with driver.ListenOf, as CheckPorts finds them, so that what is listed
// is what the check treats as used. A listener whose address has no usable
// port (ListenOf reports 0, which CheckPorts refuses) holds none.
func PortsInUse(feeds []Feed) []PortUse {
	out := []PortUse{}
	for _, f := range feeds {
		protocol, port, ok := driver.ListenOf(f.Driver, f.Config)
		if !ok || port < 1 || port > MaxPort {
			continue
		}
		out = append(out, PortUse{Protocol: protocol, Port: port, FeedID: f.ID, FeedName: f.Name, Enabled: f.Enabled})
	}
	slices.SortFunc(out, func(a, b PortUse) int {
		return cmp.Or(cmp.Compare(a.Port, b.Port), cmp.Compare(a.Protocol, b.Protocol), cmp.Compare(a.FeedID, b.FeedID))
	})
	return out
}

// SuggestPort returns a port a new listening connection of the protocol could
// take: the first from `from` upward, wrapping from MaxPort to MinPort, that
// CheckPorts would accept given taken and reserved. A TCP and a UDP connection
// may share a number, so only ports taken for the same protocol are skipped. A
// start outside MinPort-MaxPort begins at MinPort. It returns 0 when no port is
// free.
func SuggestPort(protocol string, from int, taken []PortUse, reserved PortSet) int {
	held := make(map[int]bool, len(taken))
	for _, u := range taken {
		if u.Protocol == protocol {
			held[u.Port] = true
		}
	}
	if from < MinPort || from > MaxPort {
		from = MinPort
	}
	const span = MaxPort - MinPort + 1
	for i := 0; i < span; i++ {
		if p := MinPort + (from-MinPort+i)%span; !reserved[p] && !held[p] {
			return p
		}
	}
	return 0
}

// CheckAllow validates a listening connection's allowlist (IPs or CIDRs):
// allow on tcp and udp, transport.allow on stream. A value that is not an
// array of strings is refused too. The keys are read the way the drivers
// decode them, ignoring case (see rawAt). Connections that do not listen have
// no allowlist to check.
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
// absent, the path crosses something that is not an object, or raw is not
// valid JSON. A key matches the way encoding/json matches an object key to a
// struct field: ignoring case, and when the object spells a key more than once
// the last one counts. The drivers decode their config that way, so a list
// under "Allow" reaches a listener and has to be checked like one under "allow".
func rawAt(raw json.RawMessage, keys ...string) json.RawMessage {
	if !json.Valid(raw) {
		return nil
	}
	for _, k := range keys {
		if raw = member(raw, k); raw == nil {
			return nil
		}
	}
	return raw
}

// member returns the value of the member of the JSON object raw that key
// names, or nil when raw is not an object or has no such member. raw must be
// valid JSON.
func member(raw json.RawMessage, key string) json.RawMessage {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil
	}
	var found json.RawMessage
	for dec.More() {
		name, err := dec.Token()
		if err != nil {
			return nil
		}
		var value json.RawMessage
		if dec.Decode(&value) != nil {
			return nil
		}
		if s, _ := name.(string); strings.EqualFold(s, key) {
			found = value
		}
	}
	return found
}
