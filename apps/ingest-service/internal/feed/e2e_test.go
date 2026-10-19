package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/obs"
	"github.com/sentinel/ingest-service/internal/registry"
	_ "github.com/sentinel/ingest-service/internal/source/all" // the real tcp and udp drivers and the sentinel decoder
)

// The messages a sender puts on the wire in the end-to-end tests, in the JSON
// form of sentinel.v1.Observation.
const (
	// spoofedMsg claims the provenance only the ingest service may set, and the
	// lowest classification.
	spoofedMsg = `{"sourceEntityId":"truck-1","position":{"latitude":51.5,"longitude":-0.12},` +
		`"feedId":"spoof","driver":"spoof","decoder":"spoof","observationId":"spoof","fencingToken":"999",` +
		`"classification":"CLASSIFICATION_UNCLASSIFIED"}`
	// topSecretMsg carries the highest classification.
	topSecretMsg = `{"sourceEntityId":"truck-2","position":{"latitude":51.6,"longitude":-0.13},"classification":"CLASSIFICATION_TOP_SECRET"}`
	// identityOnlyMsg names its object by identity alone.
	identityOnlyMsg = `{"identities":[{"type":"IDENTITY_TYPE_MMSI","value":"235100247"}],"position":{"latitude":50.1,"longitude":-1.2}}`
	// garbageMsg is not JSON.
	garbageMsg = "not json"
)

// listenerRig is a dedicated listener replica running real drivers through the
// supervisor and the runner. Only what lies beyond them is in memory: the
// registry, the lease table and the publisher.
type listenerRig struct {
	sup     *Supervisor
	reg     *fakeRegistry
	leases  *fakeLeaser
	pub     *memPublisher
	metrics *metrics.Metrics
}

// runListener starts the feeds on the dedicated listener "r-listen", which
// leases each of them at fencing token 42, and stops it when the test ends.
func runListener(t *testing.T, feeds ...registry.Feed) *listenerRig {
	t.Helper()
	reg := newFakeRegistry()
	reg.set(feeds...)
	leases := newFakeLeaser("r-listen")
	for _, f := range feeds {
		leases.tokens[f.ID] = 41 // so the first acquire yields 42
	}
	rig := &listenerRig{reg: reg, leases: leases, pub: &memPublisher{}, metrics: metrics.New(nil)}
	// No Factory: the supervisor builds the drivers the way the server does.
	rig.sup = New(reg, rig.pub, zap.NewNop(), rig.metrics, Options{
		ReplicaID: "r-listen", Listener: true, ListenerHost: "127.0.0.1",
		Leaser: &listenerLeaser{replicaLeaser: &replicaLeaser{fakeLeaser: leases, me: "r-listen"}, listeners: []string{"r-listen"}, dedicated: []string{"r-listen"}},
	})
	t.Cleanup(rig.sup.shutdown)
	rig.sup.Reconcile(context.Background())
	return rig
}

func listenFeed(id, driverID, config string) registry.Feed {
	return registry.Feed{ID: id, Name: id, Driver: driverID, Enabled: true, Version: 1, Config: json.RawMessage(config)}
}

// freeTCPPort and freeUDPPort name a port nothing is bound to. The system
// hands out an ephemeral one, which is above the 1024 the drivers require.
func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

func loopback(port int) string { return fmt.Sprintf("127.0.0.1:%d", port) }

// send writes each message as a sender would, a line on one TCP connection or
// a datagram from one UDP socket, and returns the sender's local address,
// which is the name the listener gives it. The socket stays open until the
// test ends.
func send(t *testing.T, network, addr string, msgs ...string) string {
	t.Helper()
	conn, err := net.Dial(network, addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	for _, m := range msgs {
		if _, err := conn.Write([]byte(m + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	return conn.LocalAddr().String()
}

// listenerDetail is the part of a listening feed's status detail that the
// tests read.
type listenerDetail struct {
	Connected   bool  `json:"connected"`
	Connections int   `json:"connections"`
	LeaseToken  int64 `json:"leaseToken"`
	Accepted    int64 `json:"accepted"`
	Listen      struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	} `json:"listen"`
	Senders []struct {
		Messages   int64  `json:"messages"`
		Malformed  int64  `json:"malformed"`
		LastReason string `json:"lastReason"`
	} `json:"senders"`
}

// snapshot is the status the replica would report for the feed, with its
// detail decoded.
func (r *listenerRig) snapshot(t *testing.T, id string) (registry.Status, listenerDetail) {
	t.Helper()
	st, ok := r.sup.Snapshot(id)
	if !ok {
		t.Fatalf("feed %s is not running on the listener", id)
	}
	var d listenerDetail
	if err := json.Unmarshal(st.Detail, &d); err != nil {
		t.Fatalf("feed %s: status detail %s: %v", id, st.Detail, err)
	}
	return st, d
}

// waitListening waits for the feed's transport to bind its port, and fails the
// test at once if the feed errors instead.
func (r *listenerRig) waitListening(t *testing.T, id string) {
	t.Helper()
	if !waitFor(func() bool {
		st, d := r.snapshot(t, id)
		if st.State == registry.StateError {
			t.Fatalf("feed %s failed to start: %s", id, st.LastError)
		}
		return d.Connected
	}, 5*time.Second) {
		t.Fatalf("feed %s did not bind its port", id)
	}
}

// listenerWant is what the four messages of a listening feed should have come
// to.
type listenerWant struct {
	feedID, driver string
	port           int
	sender         string // the sending socket's local address
	floor          string // the feed's classification
}

// checkDelivered waits until the first three messages have been published and
// the fourth dead-lettered, then checks what reached the publisher, the status
// the replica reports and the metrics.
func (r *listenerRig) checkDelivered(t *testing.T, w listenerWant) {
	t.Helper()
	delivered := func() bool {
		st, ok := r.sup.Snapshot(w.feedID)
		return ok && st.MessagesTotal == 3 && len(r.pub.deadLetters()) == 1 &&
			testutil.ToFloat64(r.metrics.ObservationsTotal.WithLabelValues(w.feedID, w.driver)) == 3
	}
	if !waitFor(delivered, 5*time.Second) {
		st, _ := r.sup.Snapshot(w.feedID)
		t.Fatalf("%s: %d observations and %d dead letters after 5 s, want 3 and 1 (state %q, last error %q)",
			w.feedID, len(r.pub.observations()), len(r.pub.deadLetters()), st.State, st.LastError)
	}

	// The runner stamps the provenance and raises the classification to the
	// feed's level, and the transport names the sender.
	type stamp struct {
		feed, driver, decoder, sensor, source, class string
		token                                        int64
	}
	published := r.pub.observations()
	if len(published) != 3 {
		t.Fatalf("%s: %d observations, want 3", w.feedID, len(published))
	}
	byEntity := map[string]*obs.Observation{}
	for _, o := range published {
		byEntity[o.SourceEntityID] = o
	}
	for _, c := range []struct{ entity, class string }{
		{"truck-1", w.floor},            // below the feed's level: raised to it
		{"truck-2", obs.ClassTopSecret}, // above it: kept
		{"MMSI:235100247", w.floor},     // identity only: its first identity stands in for the source entity id
	} {
		o := byEntity[c.entity]
		if o == nil {
			t.Errorf("%s: no observation for %s among %d", w.feedID, c.entity, len(published))
			continue
		}
		got := stamp{o.FeedID, o.Driver, o.Decoder, o.SensorID, o.Source, o.Classification, o.FencingToken}
		if want := (stamp{w.feedID, w.driver, "sentinel", w.sender, "OSINT", c.class, 42}); got != want {
			t.Errorf("%s: observation of %s is stamped %+v, want %+v", w.feedID, c.entity, got, want)
		}
		if o.ID == "" || o.ID == "spoof" {
			t.Errorf("%s: observation of %s has id %q, want one the runner computed", w.feedID, c.entity, o.ID)
		}
	}

	// What does not decode is dead-lettered with its provenance.
	dls := r.pub.deadLetters()
	if len(dls) != 1 {
		t.Fatalf("%s: %d dead letters, want 1", w.feedID, len(dls))
	}
	if dl := dls[0]; dl.FeedID != w.feedID || dl.Driver != w.driver || dl.Decoder != "sentinel" || dl.Sender != w.sender ||
		dl.Reason != "parse_error" || string(dl.Raw) != garbageMsg || dl.Cause == nil || dl.ReceivedAt.IsZero() {
		t.Errorf("%s: dead letter = %+v, want feed %s, driver %s, decoder sentinel, sender %s, reason parse_error, raw %q, a cause and a receive time",
			w.feedID, dl, w.feedID, w.driver, w.sender, garbageMsg)
	}

	// The status row counts what happened and says where senders connect.
	st, d := r.snapshot(t, w.feedID)
	if st.State != registry.StateRunning || st.Owner != "r-listen" || st.FencingToken != 42 || st.MessagesTotal != 3 || st.ErrorsTotal != 1 {
		t.Errorf("%s: status = state %q, owner %q, token %d, %d messages, %d errors; want running, r-listen, 42, 3 and 1",
			w.feedID, st.State, st.Owner, st.FencingToken, st.MessagesTotal, st.ErrorsTotal)
	}
	if d.LeaseToken != 42 || d.Accepted != 4 || d.Listen.Host != "127.0.0.1" || d.Listen.Port != w.port {
		t.Errorf("%s: detail = lease token %d, %d accepted, listening on %s:%d; want 42, 4, 127.0.0.1:%d",
			w.feedID, d.LeaseToken, d.Accepted, d.Listen.Host, d.Listen.Port, w.port)
	}
	if len(d.Senders) != 1 || d.Senders[0].Messages != 4 || d.Senders[0].Malformed != 1 || d.Senders[0].LastReason != "parse_error" {
		t.Errorf("%s: senders = %+v, want one that sent 4 messages, 1 of them malformed with reason parse_error", w.feedID, d.Senders)
	}

	// The same counts are exported.
	if n := testutil.ToFloat64(r.metrics.ObservationsTotal.WithLabelValues(w.feedID, w.driver)); n != 3 {
		t.Errorf("%s: observations_total = %v, want 3", w.feedID, n)
	}
	if n := testutil.ToFloat64(r.metrics.DecodeErrorsTotal.WithLabelValues(w.feedID, w.driver, "parse_error")); n != 1 {
		t.Errorf("%s: decode_errors_total{parse_error} = %v, want 1", w.feedID, n)
	}
}

// acceptedOnly lists a supervisor's listeners with only their accepted counter,
// so the listener collector built on it exports a single series, which
// testutil.ToFloat64 can read.
type acceptedOnly struct{ sup *Supervisor }

func (a acceptedOnly) ListenerStats() []metrics.ListenerStat {
	stats := a.sup.ListenerStats()
	for i, st := range stats {
		stats[i].Detail = map[string]any{"accepted": st.Detail["accepted"]}
	}
	return stats
}

// A TCP connection that listens, run for real by the supervisor and the
// runner: what a sender writes arrives stamped with the feed's provenance, at
// no less than its classification, and what cannot be decoded is dead-lettered
// with the sender and the decoder that refused it.
func TestTCPListenerEndToEnd(t *testing.T) {
	port := freeTCPPort(t)
	rig := runListener(t, listenFeed("site-tcp", "tcp", fmt.Sprintf(
		`{"mode":"listen","port":%d,"address":"127.0.0.1","encoding":"json","classification":"SECRET","defaultSource":"OSINT"}`, port)))
	rig.waitListening(t, "site-tcp")

	msgs := []string{spoofedMsg, topSecretMsg, identityOnlyMsg, garbageMsg}
	sender := send(t, "tcp", loopback(port), msgs...)
	rig.checkDelivered(t, listenerWant{feedID: "site-tcp", driver: "tcp", port: port, sender: sender, floor: obs.ClassSecret})

	// The listener collector over the same supervisor exports what the status
	// shows. accepted counts frames, so it equals the messages the sender wrote
	// on its one connection. ToFloat64 panics unless the collector exports
	// exactly one series, which also fails the test if the feed is not listed.
	if n := testutil.ToFloat64(metrics.NewListenerCollector(acceptedOnly{rig.sup})); n != float64(len(msgs)) {
		t.Errorf("site-tcp: sentinel_ingest_listener_events_total{event=\"accepted\"} = %v, want %d, the messages the sender wrote", n, len(msgs))
	}
}

// The same through a UDP connection, one datagram per message.
func TestUDPListenerEndToEnd(t *testing.T) {
	port := freeUDPPort(t)
	rig := runListener(t, listenFeed("site-udp", "udp", fmt.Sprintf(
		`{"port":%d,"address":"127.0.0.1","encoding":"json","classification":"CONFIDENTIAL","defaultSource":"OSINT"}`, port)))
	rig.waitListening(t, "site-udp")

	sender := send(t, "udp", loopback(port), spoofedMsg, topSecretMsg, identityOnlyMsg, garbageMsg)
	rig.checkDelivered(t, listenerWant{feedID: "site-udp", driver: "udp", port: port, sender: sender, floor: obs.ClassConfidential})
}

// Stopping the replica ends every listening feed: each writes its stopped
// status and releases its lease, the replica leaves the set, and the ports are
// free for the pod that replaces it, which binds the same ones on a
// host-network node.
func TestListenerShutdownReleasesLeasesAndPorts(t *testing.T) {
	feeds := []string{"site-tcp", "site-udp"}
	tcpPort, udpPort := freeTCPPort(t), freeUDPPort(t)
	rig := runListener(t,
		listenFeed("site-tcp", "tcp", fmt.Sprintf(`{"mode":"listen","port":%d,"address":"127.0.0.1","encoding":"json"}`, tcpPort)),
		listenFeed("site-udp", "udp", fmt.Sprintf(`{"port":%d,"address":"127.0.0.1","encoding":"json"}`, udpPort)))
	for _, id := range feeds {
		rig.waitListening(t, id)
	}
	tcpAddr, udpAddr := loopback(tcpPort), loopback(udpPort)

	// While the feeds run they hold their ports, and a sender is connected.
	if ln, err := net.Listen("tcp", tcpAddr); err == nil {
		ln.Close()
		t.Fatalf("tcp port %d can be bound while its feed runs", tcpPort)
	}
	if pc, err := net.ListenPacket("udp", udpAddr); err == nil {
		pc.Close()
		t.Fatalf("udp port %d can be bound while its feed runs", udpPort)
	}
	conn, err := net.Dial("tcp", tcpAddr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if !waitFor(func() bool { _, d := rig.snapshot(t, "site-tcp"); return d.Connections == 1 }, 5*time.Second) {
		t.Fatal("the listener did not accept the sender")
	}

	rig.sup.shutdown()

	for _, id := range feeds {
		if st := rig.reg.status(id); st.State != registry.StateStopped || st.FencingToken != 42 {
			t.Errorf("%s: status after shutdown = state %q, token %d; want stopped, 42", id, st.State, st.FencingToken)
		}
		if owner := rig.leases.owner(id); owner != "" {
			t.Errorf("%s: lease still held by %q after shutdown", id, owner)
		}
	}
	rig.leases.mu.Lock()
	released, left := slices.Sorted(slices.Values(rig.leases.released)), slices.Clone(rig.leases.left)
	rig.leases.mu.Unlock()
	if want := []string{"r-listen:site-tcp", "r-listen:site-udp"}; !slices.Equal(released, want) {
		t.Errorf("leases released = %v, want %v", released, want)
	}
	if !slices.Equal(left, []string{"r-listen"}) {
		t.Errorf("replicas left = %v, want [r-listen]", left)
	}
	rig.reg.mu.Lock()
	statusesReleased := slices.Clone(rig.reg.released)
	rig.reg.mu.Unlock()
	if !slices.Equal(statusesReleased, []string{"r-listen"}) {
		t.Errorf("statuses released for %v, want [r-listen]", statusesReleased)
	}

	ln, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		t.Errorf("tcp port %d is still held after shutdown: %v", tcpPort, err)
	} else {
		ln.Close()
	}
	pc, err := net.ListenPacket("udp", udpAddr)
	if err != nil {
		t.Errorf("udp port %d is still held after shutdown: %v", udpPort, err)
	} else {
		pc.Close()
	}
}
