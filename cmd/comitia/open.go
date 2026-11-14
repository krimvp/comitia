// Opening the application API a command runs against: the embedded SQLite
// store, or the remote HTTP client when a server URL is configured.

package main

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
	"github.com/krimvp/comitia/pkg/httpapi"
	"github.com/krimvp/comitia/pkg/sqlite"
)

// remoteTimeout bounds each remote call; it stays above the longest server-side wait.
var remoteTimeout = comitia.MaxMessageWait + 10*time.Second

// openStore opens the embedded store; a test may wrap what it returns.
var openStore = func(path string) (comitia.Store, error) {
	st, e := sqlite.OpenSQLite(path)
	if e != nil {
		return nil, e
	}
	return st, nil
}

func open(cfg config) (comitia.API, func(), error) {
	if cfg.serverURL != "" {
		if u, e := url.Parse(cfg.serverURL); e == nil && strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/v1") {
			return nil, func() {}, comitia.Invalidf("server URL must be the base URL without /v1 (for example http://127.0.0.1:8080)")
		}
		c, e := httpapi.NewClientWithInstanceDefaults(cfg.serverURL, &http.Client{Timeout: remoteTimeout}, cfg.instances)
		return c, func() {}, e
	}
	store, e := openStore(cfg.db)
	if e != nil {
		return nil, func() {}, e
	}
	svc, e := comitia.New(comitia.Config{Store: store, BootstrapCredential: cfg.bootstrapCredential, MaxContextDepth: cfg.maxContextDepth, MaxChildContextsPerGrant: cfg.maxChildContextsPerGrant})
	if e != nil {
		store.Close()
		return nil, func() {}, e
	}
	return svc, func() { _ = store.Close() }, nil
}
