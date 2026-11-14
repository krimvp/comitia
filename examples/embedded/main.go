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

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/sqlite"
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
