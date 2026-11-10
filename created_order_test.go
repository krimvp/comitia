package comitia

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTasksAndReferencesListChronologicallyWithinASecond(t *testing.T) {
	f := setup(t, filepath.Join(t.TempDir(), "order.db"))
	defer f.store.Close()
	ctx := context.Background()
	// "…05Z" sorts after "…05.25Z" as text; the store must order by time.
	*f.now = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	first, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "first"})
	if err != nil {
		t.Fatal(err)
	}
	firstRef, err := f.svc.AddReference(ctx, f.admin.Token, AddReferenceInput{ContextID: f.context.ID, Kind: "doc", URI: "urn:first"})
	if err != nil {
		t.Fatal(err)
	}
	*f.now = f.now.Add(250 * time.Millisecond)
	second, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "second"})
	if err != nil {
		t.Fatal(err)
	}
	secondRef, err := f.svc.AddReference(ctx, f.admin.Token, AddReferenceInput{ContextID: f.context.ID, Kind: "doc", URI: "urn:second"})
	if err != nil {
		t.Fatal(err)
	}
	// Rows in a fixed-width format and in the trimmed format older binaries
	// write must interleave with them by time.
	for _, row := range []struct{ id, created string }{{"fixed", "2026-01-02T03:04:05.100000000Z"}, {"trimmed", "2026-01-02T03:04:05.3Z"}} {
		if _, err := f.store.db.Exec("INSERT INTO tasks VALUES(?,?,?,?,'pending',NULL,0,NULL,'',?,?)", row.id, f.context.ID, row.id, "", row.created, row.created); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{first.ID, "fixed", second.ID, "trimmed"}
	for _, status := range []TaskStatus{"", TaskPending} {
		var got []string
		after := ""
		for pages := 0; ; pages++ {
			page, err := f.svc.ListTasksPage(ctx, f.admin.Token, f.context.ID, status, after, 1)
			if err != nil || pages > len(want) {
				t.Fatalf("status %q page %d: %+v %v", status, pages, page, err)
			}
			for _, task := range page.Tasks {
				got = append(got, task.ID)
			}
			after = page.NextAfter
			if !page.HasMore {
				break
			}
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("status %q order: %v want %v", status, got, want)
		}
	}
	// Cursors keep their trimmed wire form, so ones issued earlier still parse.
	page, err := f.svc.ListTasksPage(ctx, f.admin.Token, f.context.ID, "", encodeTaskCursor(first), 10)
	if err != nil || len(page.Tasks) != 3 || page.Tasks[0].ID != "fixed" || page.Tasks[2].ID != "trimmed" {
		t.Fatalf("after trimmed cursor: %+v %v", page, err)
	}
	if ts(first.CreatedAt) != "2026-01-02T03:04:05Z" {
		t.Fatalf("cursor time format changed: %s", ts(first.CreatedAt))
	}
	legacy, err := legacyTaskPage([]Task{second, first}, "", taskCursor{}, 10, "test")
	if err != nil || len(legacy.Tasks) != 2 || legacy.Tasks[0].ID != first.ID {
		t.Fatalf("legacy store order: %+v %v", legacy, err)
	}
	refs, err := f.svc.ListReferences(ctx, f.admin.Token, f.context.ID, "", 1)
	if err != nil || len(refs.References) != 1 || refs.References[0].ID != firstRef.ID {
		t.Fatalf("first reference page: %+v %v", refs, err)
	}
	refs, err = f.svc.ListReferences(ctx, f.admin.Token, f.context.ID, refs.NextAfter, 1)
	if err != nil || len(refs.References) != 1 || refs.References[0].ID != secondRef.ID || refs.HasMore {
		t.Fatalf("second reference page: %+v %v", refs, err)
	}
}

func TestCreatedOrderPagesSeekIndexes(t *testing.T) {
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "plan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	at := ts(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	tasks, taskArgs := taskPageQuery("c", "", TaskFilters{}, at, "id", 10)
	status, statusArgs := taskPageQuery("c", TaskPending, TaskFilters{}, at, "id", 10)
	refs, refArgs := referencePageQuery("c", at, "id", 10)
	for index, query := range map[string]struct {
		sql  string
		args []any
	}{"tasks_created_page": {tasks, taskArgs}, "tasks_status_created_page": {status, statusArgs}, "refs_created_page": {refs, refArgs}} {
		plan := queryPlan(t, st, query.sql, query.args...)
		// The cursor must bound the index range, not filter a context scan.
		if !strings.Contains(plan, "USING INDEX "+index+" (") || !strings.Contains(plan, "AND <expr>>") || strings.Contains(plan, "TEMP B-TREE") {
			t.Fatalf("%s page is not index-driven:\n%s", index, plan)
		}
	}
}
