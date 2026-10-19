package transport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// caPEM is a self-signed CA certificate with the given common name.
func caPEM(t *testing.T, cn string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// mountedSecret lays out a folder like a mounted Kubernetes secret: ca.pem is
// a link to ..data/ca.pem, and ..data a link to a timestamped directory.
func mountedSecret(t *testing.T) (dir string, repoint func(version string, content []byte)) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "secret")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	repoint = func(version string, content []byte) {
		t.Helper()
		v := filepath.Join(dir, ".."+version)
		if err := os.MkdirAll(v, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(v, "ca.pem"), content, 0o600); err != nil {
			t.Fatal(err)
		}
		tmp := filepath.Join(dir, "..data_tmp")
		_ = os.Remove(tmp)
		if err := os.Symlink(".."+version, tmp); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if err := os.Rename(tmp, filepath.Join(dir, "..data")); err != nil { // atomic, as the kubelet does
			t.Fatal(err)
		}
	}
	return dir, repoint
}

func TestTLSFilesAreReadThroughTheFolderPolicyEveryTime(t *testing.T) {
	dir, repoint := mountedSecret(t)
	repoint("v1", []byte("one"))
	if err := os.Symlink(filepath.Join("..data", "ca.pem"), filepath.Join(dir, "ca.pem")); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "ca.pem")
	dirs := []string{dir}

	// Read through a ..data-style relative link.
	if got, err := pemOrFile(nil, file, "CA", dirs); err != nil || string(got) != "one" {
		t.Fatalf("first read: %q %v", got, err)
	}
	// Rotation: the link is re-pointed to a new directory and the old one is
	// deleted. The next read, by the same configured path, gets the new content.
	repoint("v2", []byte("two"))
	if err := os.RemoveAll(filepath.Join(dir, "..v1")); err != nil {
		t.Fatal(err)
	}
	if got, err := pemOrFile(nil, file, "CA", dirs); err != nil || string(got) != "two" {
		t.Errorf("after rotation: %q %v", got, err)
	}
	// A link re-pointed outside the folder is refused when it is read.
	outside := filepath.Join(filepath.Dir(dir), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "ca.pem"), []byte("stolen"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "..data_tmp")
	if err := os.Symlink(outside, tmp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}
	got, err := pemOrFile(nil, file, "CA", dirs)
	if err == nil || got != nil || !strings.Contains(err.Error(), "INGEST_SECRET_FILE_DIRS") || strings.Contains(err.Error(), "stolen") {
		t.Errorf("re-pointed out of the folder: %q %v", got, err)
	}
	// With no folders set the read is plain, as before.
	if got, err := pemOrFile(nil, file, "CA", nil); err != nil || string(got) != "stolen" {
		t.Errorf("unrestricted: %q %v", got, err)
	}
	// PEM from a secret reference wins and is never read from disk.
	if got, err := pemOrFile([]byte("pem"), file, "CA", dirs); err != nil || string(got) != "pem" {
		t.Errorf("pem: %q %v", got, err)
	}
}

// TLSConfig reads the files each time it is called, so a dial after a rotation
// trusts the new CA.
func TestTLSConfigFollowsARotatedCA(t *testing.T) {
	dir, repoint := mountedSecret(t)
	repoint("v1", caPEM(t, "ca-one"))
	if err := os.Symlink(filepath.Join("..data", "ca.pem"), filepath.Join(dir, "ca.pem")); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Kind: "tcpclient", TLS: &TLS{Enabled: true, CAFile: filepath.Join(dir, "ca.pem")}, FileDirs: []string{dir}}
	subjects := func() string {
		t.Helper()
		tc, err := cfg.TLSConfig()
		if err != nil {
			t.Fatal(err)
		}
		return string(tc.RootCAs.Subjects()[0]) //nolint:staticcheck // the pool has one CA; Subjects is enough to tell them apart
	}
	if got := subjects(); !strings.Contains(got, "ca-one") {
		t.Fatalf("first: %q", got)
	}
	repoint("v2", caPEM(t, "ca-two"))
	if err := os.RemoveAll(filepath.Join(dir, "..v1")); err != nil {
		t.Fatal(err)
	}
	if got := subjects(); !strings.Contains(got, "ca-two") {
		t.Errorf("after rotation: %q", got)
	}
}

func TestReadUnderRefusalIsErrOutsideDirs(t *testing.T) {
	dir, _ := mountedSecret(t)
	if _, err := ReadUnder("/etc/passwd", []string{dir}); !errors.Is(err, ErrOutsideDirs) {
		t.Errorf("err = %v", err)
	}
}
