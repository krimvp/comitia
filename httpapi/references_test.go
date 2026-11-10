package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi/internal/legacy"
)

func TestHTTPListReferences(t *testing.T) {
	svc, client, owner, c, _ := administrationServer(t)
	ctx := context.Background()
	for _, uri := range []string{"https://example.test/1", "https://example.test/2", "https://example.test/3"} {
		if _, err := client.AddReference(ctx, owner.Token, comitia.AddReferenceInput{ContextID: c.ID, Kind: "issue", URI: uri}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.AddReference(ctx, owner.Token, comitia.AddReferenceInput{ContextID: c.ID, Kind: "issue", URI: "no scheme"}); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("unschemed uri: %v", err)
	}
	direct, err := svc.ListReferences(ctx, owner.Token, c.ID, "", 100)
	if err != nil || len(direct.References) != 3 {
		t.Fatalf("direct: %+v %v", direct, err)
	}
	first, err := comitia.ListReferences(ctx, client, owner.Token, c.ID, "", 2)
	if err != nil || len(first.References) != 2 || !first.HasMore || first.References[0] != direct.References[0] {
		t.Fatalf("first: %+v %v", first, err)
	}
	rest, err := client.ListReferences(ctx, owner.Token, c.ID, first.NextAfter, 2)
	if err != nil || len(rest.References) != 1 || rest.HasMore || rest.References[0] != direct.References[2] {
		t.Fatalf("rest: %+v %v", rest, err)
	}
	if _, err := client.ListReferences(ctx, owner.Token, "missing", "", 2); !errors.Is(err, comitia.ErrForbidden) || errors.Is(err, ErrReferenceListUnsupported) {
		t.Fatalf("missing context: %v", err)
	}
	if _, err := client.ListReferences(ctx, owner.Token, c.ID, "", 0); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("limit: %v", err)
	}

	// Page keys keep snake_case in both representations; records use the negotiated spelling.
	for _, canonical := range []bool{false, true} {
		req := httptest.NewRequest(http.MethodPost, "/v1/references/list", strings.NewReader(fmt.Sprintf(`{"context_id":%q}`, c.ID)))
		req.Header.Set("Authorization", "Bearer "+owner.Token)
		req.Header.Set("Content-Type", "application/json")
		if canonical {
			req.Header.Set(wireHeader, canonicalWire)
		}
		rec := httptest.NewRecorder()
		NewHandler(svc).ServeHTTP(rec, req)
		var result struct {
			Page struct {
				References []map[string]json.RawMessage `json:"references"`
				HasMore    *bool                        `json:"has_more"`
				NextAfter  string                       `json:"next_after"`
			} `json:"reference_page"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &result) != nil {
			t.Fatalf("canonical=%t: %d %s", canonical, rec.Code, rec.Body.String())
		}
		key := map[bool]string{false: "URI", true: "uri"}[canonical]
		if len(result.Page.References) != 3 || result.Page.HasMore == nil || *result.Page.HasMore || result.Page.NextAfter == "" || string(result.Page.References[0][key]) != `"`+direct.References[0].URI+`"` {
			t.Fatalf("canonical=%t: %s", canonical, rec.Body.String())
		}
	}
}

func TestHTTPListReferencesAgainstOlderServer(t *testing.T) {
	svc, _, owner, c, _ := administrationServer(t)
	ctx := context.Background()
	old := httptest.NewServer(legacy.NewHandler(svc))
	defer old.Close()
	client, err := NewClientWithInstanceDefaults(old.URL, old.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.AddReference(ctx, owner.Token, comitia.AddReferenceInput{ContextID: c.ID, Kind: "issue", URI: "https://example.test/1"}); err != nil {
		t.Fatal(err)
	}
	if page, err := client.ListReferences(ctx, owner.Token, c.ID, "", 10); !errors.Is(err, ErrReferenceListUnsupported) || page.References != nil {
		t.Fatalf("old server: %+v %v", page, err)
	}
	// A Handler over an API without the capability reports it as unsupported.
	server := httptest.NewServer(NewHandler(struct{ comitia.API }{svc}))
	defer server.Close()
	client, err = NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListReferences(ctx, owner.Token, c.ID, "", 10); !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("plain API: %v", err)
	}
}
