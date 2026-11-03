package engines

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// outboundTransport retains OS trust and adds explicitly configured roots. Build
// it per operation so changing SSL_CERT_FILE does not inherit Go's cached roots.
// Verification (including hostname verification) is never disabled.
func outboundTransport() (*http.Transport, error) {
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	add := func(path string, required bool) error {
		b, e := os.ReadFile(path)
		if e != nil {
			if required {
				return fmt.Errorf("cannot read configured TLS certificate file")
			}
			return nil
		}
		if !roots.AppendCertsFromPEM(b) && required {
			return fmt.Errorf("configured TLS certificate file contains no certificates")
		}
		return nil
	}
	if f := os.Getenv("SSL_CERT_FILE"); f != "" {
		if e := add(f, true); e != nil {
			return nil, e
		}
	}
	for _, dir := range filepath.SplitList(os.Getenv("SSL_CERT_DIR")) {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		entries, e := os.ReadDir(dir)
		if e != nil {
			return nil, fmt.Errorf("cannot read configured TLS certificate directory")
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				_ = add(filepath.Join(dir, entry.Name()), false)
			}
		}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	return tr, nil
}
