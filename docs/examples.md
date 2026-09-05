# Library example

The `github.com/krimvp/comitia` package is the application surface used by every adapter. This minimal embedded example creates a SQLite store, bootstraps its owner, and creates a workspace and context.

```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/krimvp/comitia"
)

func main() {
	ctx := context.Background()

	store, err := comitia.OpenSQLite("comitia.db")
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
