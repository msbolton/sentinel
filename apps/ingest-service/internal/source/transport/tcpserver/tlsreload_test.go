package tcpserver

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sentinel/ingest-service/internal/source/transport"
	"github.com/sentinel/ingest-service/internal/source/transport/transporttest"
)

// probe dials addr without verifying anything: it asks which certificate is
// served, not whether it is trusted.
func probe(addr string, certs ...tls.Certificate) (*tls.Conn, error) {
	return tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, Certificates: certs}) //nolint:gosec // a test asking which certificate is served
}

// serial returns the serial of the certificate the listener at addr presents.
func serial(t *testing.T, addr string) *big.Int {
	t.Helper()
	c, err := probe(addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	return c.ConnectionState().PeerCertificates[0].SerialNumber
}

func leafOf(t *testing.T, p transporttest.Pair) *x509.Certificate {
	t.Helper()
	pair, err := tls.X509KeyPair(p.CertPEM, p.KeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf
}

func serialOf(t *testing.T, p transporttest.Pair) *big.Int { return leafOf(t, p).SerialNumber }

// writePair puts the certificate and key of p at cert.pem and key.pem in dir,
// all at once, as the kubelet rotates a mounted secret: both files go into a new
// folder, and the link ..data that cert.pem and key.pem go through is re-pointed
// at it by a rename. A reload that runs while this happens sees the old pair or
// the new one, never a certificate beside the other's key. (The tests of a
// half-written pair write one file through the link by hand.)
func writePair(t *testing.T, dir string, p transporttest.Pair) {
	t.Helper()
	version := fmt.Sprintf("..v%d", time.Now().UnixNano())
	if err := os.Mkdir(filepath.Join(dir, version), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, version, "key.pem"), p.KeyPEM)
	writeFile(t, filepath.Join(dir, version, "cert.pem"), p.CertPEM)
	tmp := filepath.Join(dir, "..data_tmp")
	_ = os.Remove(tmp)
	if err := os.Symlink(version, tmp); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"key.pem", "cert.pem"} {
		link := filepath.Join(dir, name)
		if _, err := os.Lstat(link); err != nil {
			if err := os.Symlink(filepath.Join("..data", name), link); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func writeFile(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// fileListener starts a TLS listener whose certificate and key are the files
// in a folder holding p, polled every `every` (a long time: the test reloads
// by hand).
func fileListener(t *testing.T, p transporttest.Pair, every time.Duration, mod func(*transport.Config)) (srv *Server, addr, dir string, stop func()) {
	t.Helper()
	dir = t.TempDir()
	writePair(t, dir, p)
	addr = transporttest.FreePort(t)
	cfg := transport.Config{Kind: "tcpserver", Address: addr, Framing: "line",
		TLS:       &transport.TLS{Enabled: true, CertFile: filepath.Join(dir, "cert.pem"), KeyFile: filepath.Join(dir, "key.pem")},
		ReloadTLS: func() (transport.TLSMaterial, error) { return transport.TLSMaterial{}, nil }, ReloadEvery: every}
	if mod != nil {
		mod(&cfg)
	}
	tr, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	stopRun := transporttest.Run(t, tr, (&transporttest.Collector{}).Emit)
	stop = func() { once.Do(stopRun) }
	t.Cleanup(stop)
	if !transporttest.WaitFor(func() bool {
		c, err := probe(addr)
		if err == nil {
			c.Close()
		}
		return err == nil
	}, 2*time.Second) {
		t.Fatal("listener did not come up")
	}
	return tr.(*Server), addr, dir, stop
}

func TestTLSReloadServesNewCertificate(t *testing.T) {
	ca := transporttest.NewCA(t)
	first, second := ca.Issue(t, "localhost"), ca.Issue(t, "localhost")
	srv, addr, dir, _ := fileListener(t, first, 10*time.Millisecond, nil)
	if got := serial(t, addr); got.Cmp(serialOf(t, first)) != 0 {
		t.Fatalf("served serial %v before the rotation, want %v", got, serialOf(t, first))
	}

	writePair(t, dir, second)
	if !transporttest.WaitFor(func() bool { return serial(t, addr).Cmp(serialOf(t, second)) == 0 }, 3*time.Second) {
		t.Fatalf("the listener still serves serial %v after the files were overwritten, want %v", serial(t, addr), serialOf(t, second))
	}
	detail := srv.State().Detail
	if detail["tlsReloads"] != int64(1) || detail["tlsReloadFailed"] != int64(0) {
		t.Errorf("after one rotation: tlsReloads = %v, tlsReloadFailed = %v, want 1 and 0", detail["tlsReloads"], detail["tlsReloadFailed"])
	}
}

func TestTLSReloadKeepsOldOnBadMaterial(t *testing.T) {
	ca := transporttest.NewCA(t)
	first, second := ca.Issue(t, "localhost"), ca.Issue(t, "localhost")
	srv, addr, dir, _ := fileListener(t, first, time.Hour, nil)

	// The certificate file is cut short, as a copy in progress leaves it.
	writeFile(t, filepath.Join(dir, "cert.pem"), first.CertPEM[:len(first.CertPEM)/2])
	if err := srv.tlsrc.Reload(); err == nil {
		t.Fatal("a certificate cut short was accepted")
	}
	st := srv.State()
	if st.Detail["tlsReloadFailed"] != int64(1) || st.Detail["tlsReloads"] != int64(0) {
		t.Errorf("tlsReloadFailed = %v, tlsReloads = %v, want 1 and 0", st.Detail["tlsReloadFailed"], st.Detail["tlsReloads"])
	}
	if !strings.Contains(st.LastError, "TLS reload failed") {
		t.Errorf("LastError = %q, want it to say the TLS reload failed", st.LastError)
	}
	if got := serial(t, addr); got.Cmp(serialOf(t, first)) != 0 {
		t.Errorf("served serial %v after a bad reload, want the old one %v", got, serialOf(t, first))
	}

	// Good material swaps in, and clears the error that was ours.
	writePair(t, dir, second)
	if err := srv.tlsrc.Reload(); err != nil {
		t.Fatalf("good material refused: %v", err)
	}
	if got := serial(t, addr); got.Cmp(serialOf(t, second)) != 0 {
		t.Errorf("served serial %v after good material, want %v", got, serialOf(t, second))
	}
	if st := srv.State(); st.LastError != "" {
		t.Errorf("LastError = %q after a good reload, want it cleared", st.LastError)
	}
}

// Review Focus 4. A rotation writes a certificate and its key as two files, and
// a reload can land between the writes: the new certificate beside the old key.
// That pair is refused and retried at the next tick; the old pair keeps being
// served, and the match, once written, is picked up.
func TestTLSReloadToleratesHalfWrittenPair(t *testing.T) {
	ca := transporttest.NewCA(t)
	first, second := ca.Issue(t, "localhost"), ca.Issue(t, "localhost")
	srv, addr, dir, _ := fileListener(t, first, time.Hour, nil)

	writeFile(t, filepath.Join(dir, "cert.pem"), second.CertPEM) // the new certificate, the old key
	if err := srv.tlsrc.Reload(); err == nil {
		t.Fatal("a new certificate with the old key was accepted")
	}
	detail := srv.State().Detail
	if detail["tlsReloadFailed"] != int64(1) {
		t.Errorf("tlsReloadFailed = %v, want 1", detail["tlsReloadFailed"])
	}
	if got := serial(t, addr); got.Cmp(serialOf(t, first)) != 0 {
		t.Errorf("served serial %v with half a pair on disk, want the old one %v", got, serialOf(t, first))
	}

	writeFile(t, filepath.Join(dir, "key.pem"), second.KeyPEM) // the matching key lands
	if err := srv.tlsrc.Reload(); err != nil {
		t.Fatalf("the completed pair was refused: %v", err)
	}
	if got := serial(t, addr); got.Cmp(serialOf(t, second)) != 0 {
		t.Errorf("served serial %v after the key was written, want %v", got, serialOf(t, second))
	}
	detail = srv.State().Detail
	if detail["tlsReloads"] != int64(1) || detail["tlsReloadFailed"] != int64(1) {
		t.Errorf("tlsReloads = %v, tlsReloadFailed = %v, want 1 and 1", detail["tlsReloads"], detail["tlsReloadFailed"])
	}
}

// An unchanged pair is not a rotation: tlsReloads counts the swaps.
func TestTLSReloadOfUnchangedMaterialIsNotCounted(t *testing.T) {
	first := transporttest.NewCA(t).Issue(t, "localhost")
	srv, _, _, _ := fileListener(t, first, time.Hour, nil)
	for i := 0; i < 3; i++ {
		if err := srv.tlsrc.Reload(); err != nil {
			t.Fatal(err)
		}
	}
	detail := srv.State().Detail
	if detail["tlsReloads"] != int64(0) || detail["tlsReloadFailed"] != int64(0) {
		t.Errorf("tlsReloads = %v, tlsReloadFailed = %v after reading the same files, want 0 and 0", detail["tlsReloads"], detail["tlsReloadFailed"])
	}
}

// A reload builds the whole listener config again, so it must keep what New
// decided: no session tickets, and the demand for client certificates.
func TestTLSReloadKeepsTicketsOffAndMutualTLS(t *testing.T) {
	ca := transporttest.NewCA(t)
	first, second := ca.Issue(t, "localhost"), ca.Issue(t, "localhost")
	srv, _, dir, _ := fileListener(t, first, time.Hour, func(c *transport.Config) {
		c.TLS.RequireClientCert = true
		c.ClientCAPEM = ca.CertPEM
		c.ReloadTLS = func() (transport.TLSMaterial, error) { return transport.TLSMaterial{ClientCAPEM: ca.CertPEM}, nil }
	})
	writePair(t, dir, second)
	if err := srv.tlsrc.Reload(); err != nil {
		t.Fatal(err)
	}
	cur := srv.tlsrc.Current()
	if !cur.SessionTicketsDisabled {
		t.Error("the reloaded config has session tickets enabled")
	}
	if cur.ClientAuth != tls.RequireAndVerifyClientCert || cur.ClientCAs == nil {
		t.Errorf("the reloaded config has ClientAuth %v and ClientCAs %v, want RequireAndVerifyClientCert and a pool", cur.ClientAuth, cur.ClientCAs)
	}
}

// Material that would turn the listener's mutual TLS off is refused, not
// swapped in: a client CA that vanished cannot leave senders unchecked.
func TestTLSReloadRefusesToDropTheClientCA(t *testing.T) {
	ca := transporttest.NewCA(t)
	first := ca.Issue(t, "localhost")
	var ref atomic.Pointer[[]byte]
	ref.Store(&ca.CertPEM)
	srv, _, _, _ := fileListener(t, first, time.Hour, func(c *transport.Config) {
		c.TLS.RequireClientCert = true
		c.ClientCAPEM = ca.CertPEM
		c.ReloadTLS = func() (transport.TLSMaterial, error) { return transport.TLSMaterial{ClientCAPEM: *ref.Load()}, nil }
	})
	none := []byte(nil)
	ref.Store(&none)
	if err := srv.tlsrc.Reload(); err == nil {
		t.Fatal("a reload without a client CA was accepted by a listener that requires client certificates")
	}
	if srv.tlsrc.Current().ClientAuth != tls.RequireAndVerifyClientCert || srv.tlsrc.Current().ClientCAs == nil {
		t.Error("the old mutual TLS config was not kept")
	}
}

func TestTLSReloadRotatesClientCA(t *testing.T) {
	serverCA, caA, caB := transporttest.NewCA(t), transporttest.NewCA(t), transporttest.NewCA(t)
	server := serverCA.Issue(t, "localhost")
	senderA, senderB := caA.Issue(t, "sender-a"), caB.Issue(t, "sender-b")
	var trusted atomic.Pointer[[]byte]
	trusted.Store(&caA.CertPEM)

	addr := transporttest.FreePort(t)
	tr, err := New(transport.Config{Kind: "tcpserver", Address: addr, Framing: "line",
		TLS: &transport.TLS{Enabled: true, RequireClientCert: true}, CertPEM: server.CertPEM, KeyPEM: server.KeyPEM, ClientCAPEM: caA.CertPEM,
		ReloadTLS: func() (transport.TLSMaterial, error) {
			return transport.TLSMaterial{CertPEM: server.CertPEM, KeyPEM: server.KeyPEM, ClientCAPEM: *trusted.Load()}, nil
		}, ReloadEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	col := &transporttest.Collector{}
	stop := transporttest.Run(t, tr, col.Emit)
	defer stop()
	srv := tr.(*Server)

	// sendAs connects with p as the client certificate and sends one line; it
	// reports whether the listener took the line.
	sendAs := func(p transporttest.Pair, line string) bool {
		pair, err := tls.X509KeyPair(p.CertPEM, p.KeyPEM)
		if err != nil {
			t.Fatal(err)
		}
		before := col.Count()
		var c *tls.Conn
		if !transporttest.WaitFor(func() bool {
			c, err = probe(addr, pair)
			return err == nil
		}, 2*time.Second) {
			t.Fatalf("dial: %v", err)
		}
		_ = c.SetDeadline(time.Now().Add(250 * time.Millisecond))
		_, _ = c.Write([]byte(line + "\n"))
		_, _ = bufio.NewReader(c).ReadByte() // surfaces the server's alert; a sender that was taken in hears nothing, and the deadline ends the wait
		c.Close()
		return transporttest.WaitFor(func() bool { return col.Count() > before }, 300*time.Millisecond)
	}

	if !sendAs(senderA, "a1") {
		t.Fatal("a sender signed by the first client CA was refused")
	}
	if sendAs(senderB, "b1") {
		t.Fatal("a sender signed by the second client CA was accepted before the rotation")
	}

	trusted.Store(&caB.CertPEM)
	if err := srv.tlsrc.Reload(); err != nil {
		t.Fatal(err)
	}
	if !sendAs(senderB, "b2") {
		t.Error("a sender signed by the new client CA was refused after the reload")
	}
	if sendAs(senderA, "a2") {
		t.Error("a sender signed by the retired client CA was accepted after the reload")
	}
}

// The reload reads the files the way the first read did: through the folder
// policy. A link re-pointed out of the allowed folder is refused at reload time
// and counts as a failed reload; the old certificate stays.
func TestTLSReloadFollowsTheFolderPolicy(t *testing.T) {
	ca := transporttest.NewCA(t)
	first := ca.Issue(t, "localhost")
	// The policy compares resolved paths, so the folders are resolved here too
	// (a temp folder can itself be reached through a link, on macOS).
	allowed, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	writeFile(t, filepath.Join(allowed, "key.pem"), first.KeyPEM)
	writeFile(t, filepath.Join(allowed, "cert-v1.pem"), first.CertPEM)
	writeFile(t, filepath.Join(outside, "cert.pem"), first.CertPEM) // a valid certificate, in the wrong folder
	link := filepath.Join(allowed, "cert.pem")
	if err := os.Symlink("cert-v1.pem", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	repoint := func(target string) {
		t.Helper()
		tmp := link + ".tmp"
		_ = os.Remove(tmp)
		if err := os.Symlink(target, tmp); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, link); err != nil { // atomic, as the kubelet does
			t.Fatal(err)
		}
	}

	tr, err := New(transport.Config{Kind: "tcpserver", Address: transporttest.FreePort(t), Framing: "line", FileDirs: []string{allowed},
		TLS:       &transport.TLS{Enabled: true, CertFile: link, KeyFile: filepath.Join(allowed, "key.pem")},
		ReloadTLS: func() (transport.TLSMaterial, error) { return transport.TLSMaterial{}, nil }, ReloadEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	srv := tr.(*Server)

	repoint(filepath.Join(outside, "cert.pem"))
	err = srv.tlsrc.Reload()
	if err == nil {
		t.Fatal("a link pointing out of the allowed folder was followed at reload time")
	}
	if !strings.Contains(err.Error(), transport.OutsideMessage(link)) {
		t.Errorf("error %q does not say the file is outside the allowed folders", err)
	}
	if srv.State().Detail["tlsReloadFailed"] != int64(1) {
		t.Errorf("tlsReloadFailed = %v, want 1", srv.State().Detail["tlsReloadFailed"])
	}

	// Pointed back inside, the link is followed again.
	repoint("cert-v1.pem")
	if err := srv.tlsrc.Reload(); err != nil {
		t.Fatalf("a link inside the folder was refused: %v", err)
	}
}

func TestTLSCertNotAfterIsReportedOnlyWithTLS(t *testing.T) {
	pair := transporttest.NewCA(t).Issue(t, "localhost")
	srv, _, _, _ := fileListener(t, pair, time.Hour, nil)
	got, ok := srv.State().Detail["tlsCertNotAfter"].(string)
	if !ok {
		t.Fatalf("tlsCertNotAfter = %#v, want RFC 3339 text", srv.State().Detail["tlsCertNotAfter"])
	}
	if want := leafOf(t, pair).NotAfter.UTC().Format(time.RFC3339); got != want || !strings.HasSuffix(got, "Z") {
		t.Errorf("tlsCertNotAfter = %q, want %q (UTC)", got, want)
	}

	plain, err := New(transport.Config{Kind: "tcpserver", Address: transporttest.FreePort(t), Framing: "line"})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"tlsReloads", "tlsReloadFailed", "tlsCertNotAfter"} {
		if _, has := plain.State().Detail[key]; has {
			t.Errorf("a plain listener reports %s", key)
		}
	}
}

// The poll runs with the transport and ends with it.
func TestTLSReloadLoopStopsWithTheTransport(t *testing.T) {
	first := transporttest.NewCA(t).Issue(t, "localhost")
	_, _, _, stop := fileListener(t, first, 5*time.Millisecond, nil)
	time.Sleep(30 * time.Millisecond)
	stop() // Run returns only once the loop has ended; transporttest fails the test if it hangs
}

// While a reload fails the transport's own last error is not lost: the reload
// error comes first and the other follows it.
func TestFailingReloadKeepsTheEarlierLastError(t *testing.T) {
	first := transporttest.NewCA(t).Issue(t, "localhost")
	srv, _, dir, _ := fileListener(t, first, time.Hour, nil)
	srv.Set(func(st *transport.State) { st.LastError = "read tcp 10.0.0.1:5000: connection reset" })
	if got := srv.State().LastError; got != "read tcp 10.0.0.1:5000: connection reset" {
		t.Fatalf("LastError = %q before any failure", got)
	}
	writeFile(t, filepath.Join(dir, "cert.pem"), first.CertPEM[:len(first.CertPEM)/2])
	if err := srv.tlsrc.Reload(); err == nil {
		t.Fatal("a certificate cut short was accepted")
	}
	got := srv.State().LastError
	if !strings.HasPrefix(got, "TLS reload failed: ") || !strings.HasSuffix(got, "; read tcp 10.0.0.1:5000: connection reset") {
		t.Errorf("LastError = %q, want the reload failure followed by the earlier error", got)
	}
	writePair(t, dir, first)
	if err := srv.tlsrc.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := srv.State().LastError; got != "read tcp 10.0.0.1:5000: connection reset" {
		t.Errorf("LastError = %q after the reload recovered, want the earlier error alone", got)
	}
}
