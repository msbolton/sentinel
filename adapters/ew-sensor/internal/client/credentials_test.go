package client

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/test/bufconn"

	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"

	"github.com/sentinel/ew-sensor/internal/config"
)

const testToken = "si_0123456789abcdefSECRETPART"

func TestTokenCredentialsMetadata(t *testing.T) {
	for _, secure := range []bool{false, true} {
		c := NewTokenCredentials(testToken, secure)
		md, err := c.GetRequestMetadata(context.Background(), "uri")
		if err != nil {
			t.Fatal(err)
		}
		if len(md) != 1 || md["authorization"] != "Bearer "+testToken {
			t.Fatalf("metadata = %v, want exactly authorization: Bearer <token>", md)
		}
		if got := c.RequireTransportSecurity(); got != secure {
			t.Fatalf("RequireTransportSecurity = %v, want %v", got, secure)
		}
	}
}

func TestKeepaliveRespectsServerMinTime(t *testing.T) {
	k := KeepaliveParams()
	if k.Time < 10*time.Second || k.Time != 20*time.Second || k.Timeout != 10*time.Second || !k.PermitWithoutStream {
		t.Fatalf("keepalive = %+v, want Time 20s, Timeout 10s, PermitWithoutStream", k)
	}
}

// serveBufconn serves a fake ingest service on an in-memory listener and
// returns a dial option that reaches it.
func serveBufconn(t *testing.T, f *fake, opts ...grpc.ServerOption) grpc.DialOption {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(opts...)
	ingestv1.RegisterIngestServiceServer(srv, f)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) })
}

func getSessionWith(t *testing.T, conn config.Connection, dial grpc.DialOption, log *slog.Logger) error {
	t.Helper()
	opts, err := DialOptions(conn, log)
	if err != nil {
		t.Fatalf("DialOptions: %v", err)
	}
	cc, err := grpc.NewClient("passthrough:///bufnet", append(opts, dial)...)
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = ingestv1.NewIngestServiceClient(cc).GetSession(ctx, &ingestv1.GetSessionRequest{})
	return err
}

func TestInsecureSendsBearerAndWarns(t *testing.T) {
	f := newFake()
	dial := serveBufconn(t, f)
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	if err := getSessionWith(t, config.Connection{Token: testToken, Insecure: true}, dial, log); err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got := f.authorization(); len(got) != 1 || got[0] != "Bearer "+testToken {
		t.Fatalf("server saw authorization %q", got)
	}
	out := buf.String()
	if !strings.Contains(out, "in clear") {
		t.Fatalf("no clear-text warning logged: %s", out)
	}
	if strings.Contains(out, testToken) {
		t.Fatalf("token logged: %s", out)
	}
}

type testPKI struct {
	dir                       string
	caFile, certFile, keyFile string
	server                    tls.Certificate
	pool                      *x509.CertPool
}

func newTestPKI(t *testing.T) testPKI {
	t.Helper()
	dir := t.TempDir()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	leaf := func(serial int64, usage x509.ExtKeyUsage, dns []string) ([]byte, *ecdsa.PrivateKey) {
		k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "leaf"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}, DNSNames: dns,
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &k.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		return der, k
	}
	write := func(name, typ string, der []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	sDER, sKey := leaf(2, x509.ExtKeyUsageServerAuth, []string{"bufnet"})
	cDER, cKey := leaf(3, x509.ExtKeyUsageClientAuth, nil)
	cKeyDER, _ := x509.MarshalECPrivateKey(cKey)
	sKeyDER, _ := x509.MarshalECPrivateKey(sKey)
	server, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: sDER}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: sKeyDER}))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return testPKI{
		dir:      dir,
		caFile:   write("ca.pem", "CERTIFICATE", caDER),
		certFile: write("client.pem", "CERTIFICATE", cDER),
		keyFile:  write("client-key.pem", "EC PRIVATE KEY", cKeyDER),
		server:   server,
		pool:     pool,
	}
}

func TestTLSWithCA(t *testing.T) {
	pki := newTestPKI(t)
	f := newFake()
	dial := serveBufconn(t, f, grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{pki.server}})))
	if err := getSessionWith(t, config.Connection{Token: testToken, TLS: true, CA: pki.caFile}, dial, discardLogger()); err != nil {
		t.Fatalf("GetSession over TLS: %v", err)
	}
	if got := f.authorization(); len(got) != 1 || got[0] != "Bearer "+testToken {
		t.Fatalf("server saw authorization %q", got)
	}
}

func TestTLSWithoutCAUsesSystemRoots(t *testing.T) {
	pki := newTestPKI(t)
	f := newFake()
	dial := serveBufconn(t, f, grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{pki.server}})))
	err := getSessionWith(t, config.Connection{Token: testToken, TLS: true}, dial, discardLogger())
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("a private CA must not verify against the system roots, got %v", err)
	}
}

func TestTLSClientCertificate(t *testing.T) {
	pki := newTestPKI(t)
	f := newFake()
	dial := serveBufconn(t, f, grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{pki.server},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pki.pool,
	})))
	if err := getSessionWith(t, config.Connection{Token: testToken, TLS: true, CA: pki.caFile}, dial, discardLogger()); err == nil {
		t.Fatal("mTLS server accepted a client without a certificate")
	}
	conn := config.Connection{Token: testToken, TLS: true, CA: pki.caFile, Cert: pki.certFile, Key: pki.keyFile}
	if err := getSessionWith(t, conn, dial, discardLogger()); err != nil {
		t.Fatalf("GetSession with a client certificate: %v", err)
	}
}

func TestDialOptionsErrors(t *testing.T) {
	pki := newTestPKI(t)
	junk := filepath.Join(pki.dir, "junk.pem")
	if err := os.WriteFile(junk, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]config.Connection{
		"missing ca":     {Token: testToken, TLS: true, CA: filepath.Join(pki.dir, "nope.pem")},
		"ca without pem": {Token: testToken, TLS: true, CA: junk},
		"bad key pair":   {Token: testToken, TLS: true, Cert: pki.certFile, Key: junk},
		"no transport":   {Token: testToken},
	}
	for name, conn := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := DialOptions(conn, discardLogger())
			if err == nil {
				t.Fatal("want an error")
			}
			if strings.Contains(err.Error(), testToken) {
				t.Fatalf("error leaks the token: %v", err)
			}
		})
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(discard{}, nil)) }

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
