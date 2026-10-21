package registration

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"mime"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is an in-process SMTP server for exercising the mailer.
type fakeSMTP struct {
	ln       net.Listener
	cert     tls.Certificate
	roots    *x509.CertPool
	implicit bool // TLS from the first byte
	starttls bool // advertise STARTTLS
	auth     bool // advertise AUTH PLAIN (after TLS only)
	hang     bool // accept, then never speak

	mu       sync.Mutex
	gotTLS   bool
	gotAuth  string
	gotFrom  string
	gotTo    []string
	gotData  string
	sessions int
}

func newFakeSMTP(t *testing.T, configure func(*fakeSMTP)) *fakeSMTP {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)

	s := &fakeSMTP{cert: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, roots: roots}
	configure(s)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.ln = ln
	t.Cleanup(func() { ln.Close() })
	go s.serve()
	return s
}

func (s *fakeSMTP) host() string { h, _, _ := net.SplitHostPort(s.ln.Addr().String()); return h }
func (s *fakeSMTP) port() string { _, p, _ := net.SplitHostPort(s.ln.Addr().String()); return p }

func (s *fakeSMTP) mailer(opts MailOptions) *SMTPMailer {
	m := NewSMTPMailer(s.host(), s.port(), `"Sentinel" <sentinel@example.test>`, opts)
	m.tlsConfig = &tls.Config{RootCAs: s.roots, ServerName: "localhost"}
	return m
}

func (s *fakeSMTP) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.session(c)
	}
}

func (s *fakeSMTP) session(c net.Conn) {
	defer c.Close()
	s.mu.Lock()
	s.sessions++
	s.mu.Unlock()
	if s.hang {
		time.Sleep(5 * time.Second)
		return
	}
	tlsOn := false
	if s.implicit {
		c = tls.Server(c, &tls.Config{Certificates: []tls.Certificate{s.cert}})
		tlsOn = true
	}
	r := bufio.NewReader(c)
	w := func(line string) { c.Write([]byte(line + "\r\n")) }
	w("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			if s.starttls && !tlsOn {
				w("250-fake")
				w("250 STARTTLS")
			} else if s.auth && tlsOn {
				w("250-fake")
				w("250 AUTH PLAIN")
			} else {
				w("250 fake")
			}
		case cmd == "STARTTLS":
			w("220 go ahead")
			c = tls.Server(c, &tls.Config{Certificates: []tls.Certificate{s.cert}})
			r = bufio.NewReader(c)
			w = func(line string) { c.Write([]byte(line + "\r\n")) }
			tlsOn = true
		case strings.HasPrefix(cmd, "AUTH PLAIN "):
			raw, _ := base64.StdEncoding.DecodeString(line[len("AUTH PLAIN "):])
			s.mu.Lock()
			s.gotAuth = strings.ReplaceAll(string(raw), "\x00", "|")
			s.mu.Unlock()
			w("235 ok")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			s.mu.Lock()
			s.gotFrom = line[len("MAIL FROM:"):]
			s.gotTLS = tlsOn
			s.mu.Unlock()
			w("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			s.mu.Lock()
			s.gotTo = append(s.gotTo, line[len("RCPT TO:"):])
			s.mu.Unlock()
			w("250 ok")
		case cmd == "DATA":
			w("354 go")
			var sb strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				sb.WriteString(l)
			}
			s.mu.Lock()
			s.gotData = sb.String()
			s.mu.Unlock()
			w("250 queued")
		case cmd == "QUIT":
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}

func TestSMTPMailerPlain(t *testing.T) {
	srv := newFakeSMTP(t, func(*fakeSMTP) {})
	err := srv.mailer(MailOptions{}).Send(context.Background(), "rae@example.test", "Hello", "line one\r\n.dot line\r\n")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.gotTLS {
		t.Error("expected a plaintext session")
	}
	if srv.gotFrom != "<sentinel@example.test>" || len(srv.gotTo) != 1 || srv.gotTo[0] != "<rae@example.test>" {
		t.Errorf("envelope: from=%q to=%v", srv.gotFrom, srv.gotTo)
	}
	if !strings.Contains(srv.gotData, "Subject: Hello\r\n") || !strings.Contains(srv.gotData, "line one") {
		t.Errorf("message: %q", srv.gotData)
	}
}

func TestSMTPMailerOpportunisticSTARTTLS(t *testing.T) {
	// Empty MAIL_TLS keeps the old behaviour: upgrade when offered.
	srv := newFakeSMTP(t, func(s *fakeSMTP) { s.starttls = true })
	if err := srv.mailer(MailOptions{}).Send(context.Background(), "rae@example.test", "Hi", "b"); err != nil {
		t.Fatalf("send: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if !srv.gotTLS {
		t.Error("expected the session to be upgraded to TLS")
	}
}

func TestSMTPMailerSTARTTLSRequiredButMissing(t *testing.T) {
	srv := newFakeSMTP(t, func(*fakeSMTP) {})
	err := srv.mailer(MailOptions{TLS: "starttls"}).Send(context.Background(), "rae@example.test", "Hi", "b")
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("want a STARTTLS error, got %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.gotFrom != "" {
		t.Error("no mail may be sent when the required upgrade is unavailable")
	}
}

func TestSMTPMailerSTARTTLSWithAuth(t *testing.T) {
	srv := newFakeSMTP(t, func(s *fakeSMTP) { s.starttls = true; s.auth = true })
	m := srv.mailer(MailOptions{TLS: "starttls", Username: "relay", Password: "s3cret"})
	if err := m.Send(context.Background(), "rae@example.test", "Hi", "b"); err != nil {
		t.Fatalf("send: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if !srv.gotTLS {
		t.Error("expected TLS")
	}
	if srv.gotAuth != "|relay|s3cret" {
		t.Errorf("auth = %q", srv.gotAuth)
	}
}

func TestSMTPMailerImplicitTLS(t *testing.T) {
	srv := newFakeSMTP(t, func(s *fakeSMTP) { s.implicit = true; s.auth = true })
	m := srv.mailer(MailOptions{TLS: "tls", Username: "relay", Password: "s3cret"})
	if err := m.Send(context.Background(), "rae@example.test", "Hi", "b"); err != nil {
		t.Fatalf("send: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if !srv.gotTLS || srv.gotAuth != "|relay|s3cret" {
		t.Errorf("tls=%v auth=%q", srv.gotTLS, srv.gotAuth)
	}
}

func TestSMTPMailerRejectsUntrustedCertificate(t *testing.T) {
	srv := newFakeSMTP(t, func(s *fakeSMTP) { s.implicit = true })
	m := srv.mailer(MailOptions{TLS: "tls"})
	m.tlsConfig = &tls.Config{ServerName: "localhost"} // system roots only
	if err := m.Send(context.Background(), "rae@example.test", "Hi", "b"); err == nil {
		t.Fatal("a self-signed certificate must not be trusted")
	}
}

func TestSMTPMailerNeverAuthenticatesInPlaintext(t *testing.T) {
	srv := newFakeSMTP(t, func(s *fakeSMTP) { s.auth = true })
	m := srv.mailer(MailOptions{TLS: "none", Username: "relay", Password: "s3cret"})
	err := m.Send(context.Background(), "rae@example.test", "Hi", "b")
	if err == nil || !strings.Contains(err.Error(), "unencrypted") {
		t.Fatalf("want a refusal to authenticate, got %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.gotAuth != "" {
		t.Errorf("credentials were sent in plaintext: %q", srv.gotAuth)
	}
}

func TestSMTPMailerDeadline(t *testing.T) {
	srv := newFakeSMTP(t, func(s *fakeSMTP) { s.hang = true })
	m := srv.mailer(MailOptions{})
	m.deadline = 200 * time.Millisecond
	start := time.Now()
	if err := m.Send(context.Background(), "rae@example.test", "Hi", "b"); err == nil {
		t.Fatal("want a timeout error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("send blocked for %v", elapsed)
	}
}

func TestSMTPMailerDialTimeout(t *testing.T) {
	m := NewSMTPMailer("10.255.255.1", "25", "sentinel@example.test", MailOptions{})
	m.dialTimeout = 150 * time.Millisecond
	start := time.Now()
	if err := m.Send(context.Background(), "rae@example.test", "Hi", "b"); err == nil {
		t.Fatal("want a dial error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("dial blocked for %v", elapsed)
	}
}

func TestSMTPMailerRejectsHeaderInjection(t *testing.T) {
	srv := newFakeSMTP(t, func(*fakeSMTP) {})
	m := srv.mailer(MailOptions{})
	for _, to := range []string{"a@example.test\r\nBcc: evil@example.test", "a@example.test\nBcc: x@example.test", "a@example.test\rX: y"} {
		if err := m.Send(context.Background(), to, "Hi", "b"); err == nil {
			t.Errorf("recipient %q accepted", to)
		}
	}
	bad := srv.mailer(MailOptions{})
	bad.from = "sentinel@example.test\r\nBcc: evil@example.test"
	if err := bad.Send(context.Background(), "a@example.test", "Hi", "b"); err == nil {
		t.Error("From with CRLF accepted")
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.sessions != 0 {
		t.Errorf("rejected mail must not reach the relay (%d sessions)", srv.sessions)
	}
}

func TestSMTPMailerEncodesSubject(t *testing.T) {
	srv := newFakeSMTP(t, func(*fakeSMTP) {})
	subject := "Sentinel — Registration Request Rejected\r\nBcc: evil@example.test"
	if err := srv.mailer(MailOptions{}).Send(context.Background(), "rae@example.test", subject, "b"); err != nil {
		t.Fatalf("send: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	head, _, _ := strings.Cut(srv.gotData, "\r\n\r\n")
	if strings.Contains(head, "Bcc:") && strings.Contains(head, "\r\nBcc:") {
		t.Errorf("subject injected a header:\n%s", head)
	}
	var got string
	for _, l := range strings.Split(head, "\r\n") {
		if strings.HasPrefix(l, "Subject: ") {
			got = strings.TrimPrefix(l, "Subject: ")
		}
	}
	if !strings.HasPrefix(got, "=?utf-8?q?") {
		t.Fatalf("subject not RFC 2047 encoded: %q", got)
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(got)
	if err != nil || !strings.HasPrefix(decoded, "Sentinel — Registration") {
		t.Errorf("decoded = %q, %v", decoded, err)
	}
}
