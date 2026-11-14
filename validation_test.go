package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/krimvp/comitia/internal/model"
)

func TestValidationDetails(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	tests := []struct {
		name, detail string
		call         func() error
	}{
		{"title", "title required", func() error { _, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{}); return err }},
		{"limit", "limit", func() error { _, err := f.svc.ListTasks(ctx, f.admin.Token, f.context.ID, "", 0); return err }},
		{"instance", "agent_instance.id", func() error { _, err := model.NormalizeAgentInstance(&AgentInstance{ID: "secret value"}); return err }},
		{"resume instance", "agent_instance required", func() error {
			return f.svc.SetInstanceResume(ctx, f.admin.Token, f.context.ID, nil, json.RawMessage(`{}`))
		}},
		{"resume object", "resume must be a JSON object", func() error {
			return f.svc.SetInstanceResume(ctx, f.admin.Token, f.context.ID, &AgentInstance{ID: "worker"}, json.RawMessage(`["secret value"]`))
		}},
		{"presence instance", "agent_instance must be", func() error {
			id := "secret value"
			_, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 10, MessageFilters{AgentInstance: &id})
			return err
		}},
		{"unknown payload field", `unknown field "token"`, func() error {
			return validTaskStatus(json.RawMessage(`{"state":"done","summary":"ready","token":"secret value"}`))
		}},
		{"payload field", "summary", func() error { return validTaskStatus(json.RawMessage(`{"state":"done"}`)) }},
		{"payload type", "state", func() error { return validTaskStatus(json.RawMessage(`{"state":42,"summary":"ready"}`)) }},
		{"task rule", "task_id required", func() error {
			_, err := f.svc.normalizeEnvelope(&MessageEnvelope{Schema: TaskStatusSchema, Payload: json.RawMessage(`{"state":"done","summary":"ready"}`)}, "")
			return err
		}},
		{"propagate payload detail", "summary", func() error {
			_, err := f.svc.normalizeEnvelope(&MessageEnvelope{Schema: TaskStatusSchema, Payload: json.RawMessage(`{"state":"done"}`)}, "")
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.detail) || strings.Contains(err.Error(), "secret value") {
				t.Fatalf("detail: %v", err)
			}
		})
	}
}

// Explicit inventory of bare ErrInvalid returns that cannot yet be converted:
// none. Walk production sources so newly added sites must be converted or listed.
func TestBareInvalidReturns(t *testing.T) {
	var found []string
	fset := gotoken.NewFileSet()
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".") && path != "." {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			r, ok := n.(*ast.ReturnStmt)
			if !ok {
				return true
			}
			for _, expr := range r.Results {
				name := ""
				switch x := expr.(type) {
				case *ast.Ident:
					name = x.Name
				case *ast.SelectorExpr:
					name = x.Sel.Name
				}
				if name == "ErrInvalid" {
					found = append(found, fset.Position(expr.Pos()).String())
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(found)
	var unconverted []string
	if !reflect.DeepEqual(found, unconverted) {
		t.Fatalf("bare ErrInvalid returns (convert or list): %v", found)
	}
}
