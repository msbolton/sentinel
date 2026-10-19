package mqtt

import (
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/source/transport"
	"github.com/sentinel/ingest-service/internal/source/transport/transporttest"
)

// The broker's certificate comes from a CA the config did not start with. Only
// a subscriber that builds its TLS config again at each connection, from the
// material the reload names, can complete the handshake.
func TestConnectReadsReloadedMaterial(t *testing.T) {
	t.Setenv("all_proxy", "") // a proxy would take the dial away from the subscriber
	oldCA, newCA := transporttest.NewCA(t), transporttest.NewCA(t)
	addr, handshakes := transporttest.TLSListener(t, newCA.Issue(t, "localhost"), nil)
	tr, err := New(transport.Config{Kind: "mqtt", BrokerURL: "ssl://" + addr, Topics: []string{"t"},
		TLS: &transport.TLS{Enabled: true, ServerName: "localhost"}, CAPEM: oldCA.CertPEM,
		ReloadTLS:   func() (transport.TLSMaterial, error) { return transport.TLSMaterial{CAPEM: newCA.CertPEM}, nil },
		ReloadEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	stop := transporttest.Run(t, tr, (&transporttest.Collector{}).Emit)
	defer stop()
	if !transporttest.WaitFor(func() bool { return handshakes.Load() >= 1 }, 3*time.Second) {
		t.Fatalf("the broker's certificate from the new CA was never trusted (LastError %q)", tr.State().LastError)
	}
}
