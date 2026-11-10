package httpsd

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"time"

	"github.com/go-acme/lego/v4/lego"
)

// clientWithRoot builds an HTTP client trusting a custom CA bundle in
// addition to the system roots. Test rigs point acme_ca_root at the
// pebble minica.pem; production never sets it.
type clientWithRoot struct{ root *x509.CertPool }

func (c clientWithRoot) client() *http.Client {
	return &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{
				RootCAs:    c.root,
				MinVersion: tls.VersionTLS12,
			},
		},
	}
}

var _ = lego.LEDirectoryProduction // keep the import anchored for docs
