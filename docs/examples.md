# Library example

The `github.com/krimvp/comitia/pkg/comitia` package is the application surface used by every adapter. Embedded callers also import `github.com/krimvp/comitia/pkg/sqlite` and call `sqlite.OpenSQLite` to supply the driver; remote clients importing `comitia`, `httpapi` or `mcp` do not build or register it. The deprecated `comitia.OpenSQLite` requires an explicitly registered driver and returns `ErrInvalid` with migration guidance when none is registered.

The public packages are `github.com/krimvp/comitia/pkg/` followed by `comitia`, `httpapi`, `mcp`, `sqlite` or `storetest`. They used to be at the module root: replace `github.com/krimvp/comitia` with `github.com/krimvp/comitia/pkg/comitia`, and `github.com/krimvp/comitia/httpapi` with `github.com/krimvp/comitia/pkg/httpapi`, likewise for the others.

This program, [`examples/embedded/main.go`](../examples/embedded/main.go), opens a store in a temporary directory, bootstraps its owner, creates a context, appends and reads a message, and creates and claims a task. `go run ./examples/embedded` runs it, and its test checks the output; change both files together.

```go
// Command embedded runs Comitia inside a Go program: it opens a SQLite store,
// bootstraps the owner, creates a context, appends and reads a message, and
// creates and claims a task. docs/examples.md shows this program.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
	"github.com/krimvp/comitia/pkg/sqlite"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	dir, err := os.MkdirTemp("", "comitia-example-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	store, err := sqlite.OpenSQLite(filepath.Join(dir, "comitia.db"))
	if err != nil {
		return err
	}
	defer store.Close()

	service, err := comitia.New(comitia.Config{
		Store:               store,
		BootstrapCredential: "replace-with-a-one-time-secret",
	})
	if err != nil {
		return err
	}

	_, owner, err := service.Bootstrap(ctx, comitia.BootstrapInput{
		Credential: "replace-with-a-one-time-secret",
		AdminName:  "owner",
		SessionTTL: 24 * time.Hour,
	})
	if err != nil {
		return err
	}

	workspace, err := service.CreateWorkspace(ctx, owner.Token, "demo")
	if err != nil {
		return err
	}
	work, err := service.CreateContext(ctx, owner.Token, comitia.CreateContextInput{
		WorkspaceID: workspace.ID,
		Name:        "handoff",
	})
	if err != nil {
		return err
	}
	fmt.Printf("created context %s\n", work.Name)

	sent, err := service.AppendMessage(ctx, owner.Token, work.ID, comitia.MessageInput{
		DedupeKey: "review-request",
		Body:      "Please review the patch.",
	})
	if err != nil {
		return err
	}
	fmt.Printf("appended message %d\n", sent.Sequence)

	messages, err := service.ReplayMessages(ctx, owner.Token, work.ID, 0, comitia.DefaultListLimit)
	if err != nil {
		return err
	}
	for _, m := range messages {
		fmt.Printf("read message %d: %s\n", m.Sequence, m.Body)
	}

	task, err := service.CreateTask(ctx, owner.Token, work.ID, comitia.TaskInput{Title: "Review the patch"})
	if err != nil {
		return err
	}
	claim, err := service.ClaimTask(ctx, owner.Token, task.ID, comitia.DefaultLease)
	if err != nil {
		return err
	}
	fmt.Printf("claimed task %q: %s, lease version %d\n", claim.Task.Title, claim.Task.Status, claim.Task.LeaseVersion)
	return nil
}
```

It prints:

```text
created context handoff
appended message 1
read message 1: Please review the patch.
claimed task "Review the patch": leased, lease version 1
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
