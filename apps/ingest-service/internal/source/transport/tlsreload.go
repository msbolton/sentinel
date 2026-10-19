package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// WithMaterial returns the config with m as its TLS secrets.
func (c Config) WithMaterial(m TLSMaterial) Config {
	c.CertPEM, c.KeyPEM, c.ClientCAPEM, c.CAPEM = m.CertPEM, m.KeyPEM, m.ClientCAPEM, m.CAPEM
	return c
}

// Reloading reports whether the config asks for TLS material to be read again:
// there is a way to read it, and a period to do it at.
func (c Config) Reloading() bool { return c.ReloadTLS != nil && c.ReloadEvery > 0 }

// TLSSource is the TLS config a transport uses now. It builds the first one
// from the config and, when the config reloads, builds it again from the
// reloaded material: a listener's Run polls it, a dialer asks for a fresh one
// (Fresh) before each dial. A config that cannot be built is dropped and the
// one in use stays, so a certificate half way through being rotated costs a
// retry, not the connection.
//
// A nil *TLSSource is a transport without TLS, and every method accepts it.
type TLSSource struct {
	cfg    Config
	finish func(*tls.Config) error
	log    *zap.Logger

	cur             atomic.Pointer[tls.Config]
	reloads, failed atomic.Int64

	mu     sync.Mutex // serializes reloads; guards logged. A reload reads files, so Apply, which health calls, does not take it
	logged string     // the failure last logged at Warn, so a repeat is not
	// lastErr is why the latest reload failed; "" when it succeeded.
	lastErr atomic.Pointer[string]
}

// NewTLSSource builds the first TLS config from cfg, or returns nil for a
// config without TLS. finish, if not nil, is the transport's own part of the
// config and runs on every config built, the first and each reload, before it
// is used: it sets what only that transport decides (a listener's session
// tickets and client authentication) and refuses a config that transport cannot
// use. It is called with nil when there is no TLS, so that a check that must
// hold without one (a mutual TLS listener with no TLS at all) is made.
func NewTLSSource(cfg Config, finish func(*tls.Config) error) (*TLSSource, error) {
	t, err := cfg.TLSConfig()
	if err != nil {
		return nil, err
	}
	if finish != nil {
		if err := finish(t); err != nil {
			return nil, err
		}
	}
	if t == nil {
		return nil, nil
	}
	log := cfg.Logger
	if log == nil {
		log = zap.NewNop()
	}
	s := &TLSSource{cfg: cfg, finish: finish, log: log}
	s.cur.Store(t)
	return s, nil
}

// Current is the config in use now.
func (s *TLSSource) Current() *tls.Config {
	if s == nil {
		return nil
	}
	return s.cur.Load()
}

// Fresh is the config to dial with: the one in use, after reading the material
// again when the config reloads. A reload that fails is counted and the config
// in use is returned.
func (s *TLSSource) Fresh() *tls.Config {
	if s == nil {
		return nil
	}
	if s.cfg.Reloading() {
		_ = s.Reload()
	}
	return s.Current()
}

// Reload reads the TLS material again and, when it makes a config this
// transport can use, swaps it in. Otherwise it keeps the config in use, counts
// the failure and returns why. Material that is the same as what is in use
// swaps nothing and counts nothing. Connections already made keep the session
// they have.
func (s *TLSSource) Reload() error {
	if s == nil || s.cfg.ReloadTLS == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next, err := s.build()
	if err != nil {
		s.failed.Add(1)
		text := err.Error()
		s.lastErr.Store(&text)
		if s.logged != text {
			s.logged = text
			s.log.Warn("TLS reload failed; keeping the certificate in use", zap.Error(err))
		} else {
			s.log.Debug("TLS reload failed again", zap.Error(err))
		}
		return err
	}
	s.lastErr.Store(nil)
	s.logged = ""
	if prev := s.cur.Load(); !sameMaterial(prev, next) {
		s.cur.Store(next)
		s.reloads.Add(1)
		s.log.Info("TLS material reloaded", zap.String("certNotAfter", certNotAfter(next)))
	}
	return nil
}

func (s *TLSSource) build() (*tls.Config, error) {
	m, err := s.cfg.ReloadTLS()
	if err != nil {
		return nil, err
	}
	t, err := s.cfg.WithMaterial(m).TLSConfig()
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, fmt.Errorf("the reloaded configuration has no TLS")
	}
	if s.finish != nil {
		if err := s.finish(t); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// Run reloads every ReloadEvery until ctx ends. It returns at once when the
// config does not reload.
func (s *TLSSource) Run(ctx context.Context) {
	if s == nil || !s.cfg.Reloading() {
		return
	}
	tick := time.NewTicker(s.cfg.ReloadEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			_ = s.Reload()
		}
	}
}

// Apply adds the TLS part of a transport's health to st: the reload counts, the
// expiry of the certificate in use (RFC 3339, UTC) when it has one, and, while
// the latest reload is failing, that in front of the transport's own last error:
// it is what the operator has to act on, and the other is kept after it. The error names a file or says why material did not parse; it
// holds no payload.
func (s *TLSSource) Apply(st *State) {
	if s == nil {
		return
	}
	if st.Detail == nil {
		st.Detail = map[string]any{}
	}
	st.Detail["tlsReloads"], st.Detail["tlsReloadFailed"] = s.reloads.Load(), s.failed.Load()
	if na := certNotAfter(s.cur.Load()); na != "" {
		st.Detail["tlsCertNotAfter"] = na
	}
	if failing := s.lastErr.Load(); failing != nil {
		msg := "TLS reload failed: " + *failing
		if st.LastError != "" {
			msg += "; " + st.LastError // a handshake, read or dial error is not hidden by it
		}
		st.LastError = msg
	}
}

// certNotAfter is when the config's certificate expires, as RFC 3339 text in
// UTC; empty when it has no certificate. X509KeyPair fills the leaf in.
func certNotAfter(t *tls.Config) string {
	if t == nil || len(t.Certificates) == 0 {
		return ""
	}
	leaf := t.Certificates[0].Leaf
	if leaf == nil {
		if len(t.Certificates[0].Certificate) == 0 {
			return ""
		}
		var err error
		if leaf, err = x509.ParseCertificate(t.Certificates[0].Certificate[0]); err != nil {
			return ""
		}
	}
	return leaf.NotAfter.UTC().Format(time.RFC3339)
}

// sameMaterial reports whether two configs hold the same certificates and the
// same CA pools, which is all a rotation changes.
func sameMaterial(a, b *tls.Config) bool {
	if a == nil || b == nil || len(a.Certificates) != len(b.Certificates) {
		return false
	}
	for i := range a.Certificates {
		x, y := a.Certificates[i].Certificate, b.Certificates[i].Certificate
		if len(x) != len(y) {
			return false
		}
		for j := range x {
			if !bytes.Equal(x[j], y[j]) {
				return false
			}
		}
	}
	return a.RootCAs.Equal(b.RootCAs) && a.ClientCAs.Equal(b.ClientCAs)
}
