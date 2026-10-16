package transport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

// TLSConfig builds a crypto/tls config from the options, or nil when TLS is
// off.
func (c Config) TLSConfig() (*tls.Config, error) {
	if c.TLS == nil || !c.TLS.Enabled {
		return nil, nil
	}
	t := &tls.Config{InsecureSkipVerify: c.TLS.InsecureSkipVerify, ServerName: c.TLS.ServerName, MinVersion: tls.VersionTLS12} //nolint:gosec // operator choice
	if c.TLS.CAFile != "" {
		pem, err := os.ReadFile(c.TLS.CAFile)
		if err != nil {
			return nil, fmt.Errorf("reading CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("CA file holds no certificates")
		}
		t.RootCAs = pool
	}
	if (c.TLS.CertFile == "") != (c.TLS.KeyFile == "") {
		return nil, errors.New("certFile and keyFile must be set together")
	}
	if c.TLS.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(c.TLS.CertFile, c.TLS.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("loading client certificate: %w", err)
		}
		t.Certificates = []tls.Certificate{cert}
	}
	return t, nil
}
