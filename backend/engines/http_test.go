package engines

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func localTLS(t *testing.T) (string, string) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "EasyRead test CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	root, e := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"localhost"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"ok"}]}`))
	}))
	s.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	s.StartTLS()
	t.Cleanup(s.Close)
	file := filepath.Join(t.TempDir(), "root.pem")
	if e = os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root}), 0600); e != nil {
		t.Fatal(e)
	}
	return s.URL, file
}
func TestOutboundTLSUntrustedCertificateRejected(t *testing.T) {
	raw, _ := localTLS(t)
	t.Setenv("SSL_CERT_FILE", "")
	t.Setenv("SSL_CERT_DIR", "")
	_, e := Models(context.Background(), Object{"base_url": strings.Replace(raw, "127.0.0.1", "localhost", 1)})
	if e == nil {
		t.Fatal("untrusted certificate accepted")
	}
}
func TestOutboundTLSExplicitTrustedCertificateUsed(t *testing.T) {
	raw, file := localTLS(t)
	t.Setenv("SSL_CERT_FILE", file)
	t.Setenv("SSL_CERT_DIR", "")
	models, e := Models(context.Background(), Object{"base_url": strings.Replace(raw, "127.0.0.1", "localhost", 1)})
	if e != nil || len(models) != 1 || models[0] != "ok" {
		t.Fatalf("%v %v", models, e)
	}
}
func TestOutboundTLSHostnameMismatchRejected(t *testing.T) {
	raw, file := localTLS(t)
	t.Setenv("SSL_CERT_FILE", file)
	t.Setenv("SSL_CERT_DIR", "")
	_, e := Models(context.Background(), Object{"base_url": raw})
	if e == nil {
		t.Fatal("hostname mismatch accepted")
	}
}
func TestOutboundTLSDirectoryAndInvalidExplicitFile(t *testing.T) {
	raw, file := localTLS(t)
	t.Setenv("SSL_CERT_FILE", "")
	t.Setenv("SSL_CERT_DIR", filepath.Dir(file))
	o := Object{"base_url": strings.Replace(raw, "127.0.0.1", "localhost", 1)}
	if _, e := Models(context.Background(), o); e != nil {
		t.Fatal(e)
	}
	t.Setenv("SSL_CERT_FILE", file+".missing")
	if _, e := Models(context.Background(), o); e == nil {
		t.Fatal("missing explicit root ignored")
	}
}
