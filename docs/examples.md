# Library example

The `github.com/krimvp/comitia` package is the application surface used by every adapter. Embedded callers also import `github.com/krimvp/comitia/sqlite` and call `sqlite.OpenSQLite` to supply the driver. Remote clients importing `comitia`, `httpapi` or `mcp` do not build or register the SQLite driver. This minimal embedded example creates a SQLite store, bootstraps its owner, and creates a workspace and context. To migrate an existing example, replace `comitia.OpenSQLite` with `sqlite.OpenSQLite` and add the SQLite import. The deprecated root constructor requires an explicitly registered driver and returns `ErrInvalid` with migration guidance when none is registered.

```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/sqlite"
)

func main() {
	ctx := context.Background()

	store, err := sqlite.OpenSQLite("comitia.db")
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	service, err := comitia.New(comitia.Config{
		Store:               store,
		BootstrapCredential: "replace-with-a-one-time-secret",
	})
	if err != nil {
		log.Fatal(err)
	}

	_, owner, err := service.Bootstrap(ctx, comitia.BootstrapInput{
		Credential: "replace-with-a-one-time-secret",
		AdminName:  "owner",
		SessionTTL: 24 * time.Hour,
	})
	if err != nil {
		log.Fatal(err)
	}

	workspace, err := service.CreateWorkspace(ctx, owner.Token, "demo")
	if err != nil {
		log.Fatal(err)
	}

	work, err := service.CreateContext(ctx, owner.Token, comitia.CreateContextInput{
		WorkspaceID: workspace.ID,
		Name:        "handoff",
	})
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("created context %s", work.ID)
}
```

`Bootstrap` succeeds only once for a database and is intentionally absent from the HTTP API. A real application should obtain the bootstrap credential from protected configuration, remove it after bootstrap, and rotate the time-bounded owner session before it expires.

For dependency injection or tests, set `Config.Clock`. Alternate stores implement `Store`; adapters and remote clients implement `API`.

To embed MCP over HTTP, mount `mcp.NewHTTPHandler` on an `http.Server` and set `ReadHeaderTimeout` on that server. Header parsing happens before the handler receives a request, so `mcp.HTTPConfig` has no listener timeout setting; its former inert `ReadHeaderTimeout` field has been removed. The `comitia mcp-http` listener uses a 10-second header timeout. For example, an embedding listener can use:

```go
server := &http.Server{
	Addr:              "127.0.0.1:8391",
	Handler:           mcp.NewHTTPHandler(api, mcp.HTTPConfig{}),
	ReadHeaderTimeout: 10 * time.Second,
}
```
