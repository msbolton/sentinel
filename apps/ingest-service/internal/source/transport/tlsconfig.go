package transport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
)

// TLSConfig builds a crypto/tls config from the options, or nil when TLS
// is off. PEM material set from secret references wins over file paths.
// The same config serves both directions: RootCAs verify a server we
// dial, ClientCAs verify clients a listener accepts. It never sets
// ClientAuth: RequireClientCert is a listener setting that tcpserver acts
// on, and every transport that dials ignores it.
func (c Config) TLSConfig() (*tls.Config, error) {
	if c.TLS == nil || !c.TLS.Enabled {
		return nil, nil
	}
	t := &tls.Config{InsecureSkipVerify: c.TLS.InsecureSkipVerify, ServerName: c.TLS.ServerName, MinVersion: tls.VersionTLS12} //nolint:gosec // operator choice
	ca, err := pemOrFile(c.CAPEM, c.TLS.CAFile, "CA", c.FileDirs)
	if err != nil {
		return nil, err
	}
	if ca != nil {
		if t.RootCAs, err = pool(ca, "CA"); err != nil {
			return nil, err
		}
	}
	cert, err := pemOrFile(c.CertPEM, c.TLS.CertFile, "certificate", c.FileDirs)
	if err != nil {
		return nil, err
	}
	key, err := pemOrFile(c.KeyPEM, c.TLS.KeyFile, "key", c.FileDirs)
	if err != nil {
		return nil, err
	}
	if (cert == nil) != (key == nil) {
		return nil, errors.New("a certificate and its key must be set together")
	}
	if cert != nil {
		pair, err := tls.X509KeyPair(cert, key)
		if err != nil {
			return nil, fmt.Errorf("loading certificate: %w", err)
		}
		t.Certificates = []tls.Certificate{pair}
	}
	clientCA, err := pemOrFile(c.ClientCAPEM, c.TLS.ClientCAFile, "client CA", c.FileDirs)
	if err != nil {
		return nil, err
	}
	if clientCA != nil {
		if t.ClientCAs, err = pool(clientCA, "client CA"); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// pemOrFile returns the PEM from a secret reference, or else the contents of
// file. The file is read each time, by the path as configured, never a path
// resolved once, so a rotated certificate (a mounted secret whose link is
// re-pointed) is followed by whatever builds a TLS config again: a listener's
// reload, a dialer's next dial (see TLSSource; an MQTT broker over ws or wss, or
// behind all_proxy, is the one exception and keeps its start-time config). With dirs set (a non-nil list)
// the read is confined to those folders, links followed inside them and never
// out; with nil dirs it is a plain read.
func pemOrFile(pemBytes []byte, file, what string, dirs []string) ([]byte, error) {
	if len(pemBytes) > 0 {
		return pemBytes, nil
	}
	if file == "" {
		return nil, nil
	}
	var b []byte
	var err error
	if dirs == nil {
		b, err = ReadFile(file)
	} else {
		b, err = ReadUnder(file, dirs)
		if errors.Is(err, ErrOutsideDirs) {
			return nil, fmt.Errorf("reading %s file: %s", what, OutsideMessage(file))
		}
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s file: %w", what, err)
	}
	return b, nil
}

func pool(pemBytes []byte, what string) (*x509.CertPool, error) {
	p := x509.NewCertPool()
	if !p.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("%s holds no certificates", what)
	}
	return p, nil
}
