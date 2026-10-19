package grpcin

import (
	"time"

	"github.com/sentinel/ingest-service/internal/source/driver"
	"github.com/sentinel/ingest-service/internal/source/transport"
)

// Health is safe to call at any time, also before Run and after it.
func (d *Driver) Health() driver.Health {
	detail := d.counts.Detail()
	senders, distinct := d.senders.Snapshot(time.Now(), sendersWindow)
	detail["senders"] = senders
	detail["distinctSenders"] = distinct
	var live int64
	if gl := d.gl.Load(); gl != nil {
		live = gl.Live()
	}
	detail["connections"] = live
	detail["listen"] = map[string]any{"protocol": "grpc", "host": d.host, "port": d.cfg.Port}
	detail["tls"] = d.tlsrc != nil
	detail["allowlist"] = allowDetail(len(d.cfg.Allow))
	detail["reportsOk"] = d.reportsOK.Load()
	detail["deadLettered"] = d.deadLettered.Load()
	d.rejectedMu.Lock()
	byReason := make(map[string]int64, len(d.rejected))
	for k, v := range d.rejected {
		byReason[k] = v
	}
	d.rejectedMu.Unlock()
	detail["reportsRejected"] = byReason
	st := transport.State{Connected: d.connected.Load(), Detail: detail}
	d.tlsrc.Apply(&st)
	return driver.Health{Connected: st.Connected, LastError: st.LastError, Detail: st.Detail}
}

// allowDetail is "open" for an empty allowlist, else the entry count.
func allowDetail(n int) any {
	if n == 0 {
		return "open"
	}
	return n
}
