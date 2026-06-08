package scrapctl

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/petabytecl/scrap/internal/security"
)

func applyTLSDefaults(opts *commonOptions) {
	opts.tls = security.ClientTLSFiles{
		CertPath:   envString("SCRAP_TLS_SCRAPCTL_CERT", opts.tls.CertPath),
		KeyPath:    envString("SCRAP_TLS_SCRAPCTL_KEY", opts.tls.KeyPath),
		RootCAPath: envString("SCRAP_TLS_SCRAPCTL_CLIENT_CA", opts.tls.RootCAPath),
		ServerName: envString("SCRAP_TLS_SCRAPCTL_SERVER_NAME", opts.tls.ServerName),
	}
}

func withHTTPClientTLS(deps Deps, opts commonOptions, urls ...string) (Deps, error) {
	deps = deps.withDefaults()
	if !scrapctlTLSRequired(opts) {
		return deps, nil
	}
	tlsConfig, err := security.BuildMTLSClientConfig("SCRAP_TLS_SCRAPCTL", opts.tls)
	if err != nil {
		return Deps{}, fmt.Errorf("scrapctl TLS: %w", err)
	}
	if err := requireHTTPSURLs(urls); err != nil {
		return Deps{}, err
	}
	client, err := cloneHTTPClientWithTLS(deps.HTTPClient, tlsConfig)
	if err != nil {
		return Deps{}, err
	}
	deps.HTTPClient = client
	return deps, nil
}

func requireHTTPSURLs(rawURLs []string) error {
	for _, raw := range rawURLs {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" {
			return errors.New("scrapctl TLS requires https URLs")
		}
	}
	return nil
}

func scrapctlTLSRequired(opts commonOptions) bool {
	return tlsFilesPresent(opts.tls) || strings.TrimSpace(os.Getenv("SCRAP_SECURITY_MODE")) == string(security.ModeProduction)
}

func tlsFilesPresent(files security.ClientTLSFiles) bool {
	return strings.TrimSpace(files.CertPath) != "" ||
		strings.TrimSpace(files.KeyPath) != "" ||
		strings.TrimSpace(files.RootCAPath) != "" ||
		strings.TrimSpace(files.ServerName) != ""
}

func cloneHTTPClientWithTLS(base *http.Client, tlsConfig *tls.Config) (*http.Client, error) {
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	switch transport := base.Transport.(type) {
	case nil:
		cloned := http.DefaultTransport.(*http.Transport).Clone()
		cloned.TLSClientConfig = tlsConfig
		client.Transport = cloned
	case *http.Transport:
		cloned := transport.Clone()
		cloned.TLSClientConfig = tlsConfig
		client.Transport = cloned
	default:
		return nil, errors.New("scrapctl TLS requires an HTTP transport that can carry TLS config")
	}
	return &client, nil
}
