package comitia_test

import (
	"errors"
	"github.com/krimvp/comitia"
	"testing"
	"time"
)

type policyLookup struct {
	grants               map[string]comitia.Grant
	sessions             map[string]comitia.Session
	grantErr, sessionErr error
}

func (r policyLookup) Grant(id string) (comitia.Grant, error) {
	if r.grantErr != nil {
		return comitia.Grant{}, r.grantErr
	}
	g, ok := r.grants[id]
	if !ok {
		return g, comitia.ErrNotFound
	}
	return g, nil
}
func (r policyLookup) Session(id string) (comitia.Session, error) {
	if r.sessionErr != nil {
		return comitia.Session{}, r.sessionErr
	}
	s, ok := r.sessions[id]
	if !ok {
		return s, comitia.ErrNotFound
	}
	return s, nil
}

var _ comitia.GrantLookup = policyLookup{}

func TestExternalGrantPolicyRejectsBrokenAncestry(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	failure := errors.New("backend unavailable")
	for _, kind := range []string{"missing grant", "missing session", "grant error", "session error", "cycle"} {
		t.Run(kind, func(t *testing.T) {
			root := comitia.Grant{ID: "root", SessionID: "session", IssuerSessionID: "session", ExpiresAt: now.Add(time.Hour)}
			leaf := root
			leaf.ID = "leaf"
			leaf.ParentGrantID = "root"
			lookup := policyLookup{grants: map[string]comitia.Grant{"root": root}, sessions: map[string]comitia.Session{"session": {ID: "session", ExpiresAt: now.Add(2 * time.Hour)}}}
			want := comitia.ErrNotFound
			switch kind {
			case "missing grant":
				delete(lookup.grants, "root")
			case "missing session":
				delete(lookup.sessions, "session")
			case "grant error":
				lookup.grantErr = failure
				want = failure
			case "session error":
				lookup.sessionErr = failure
				want = failure
			case "cycle":
				root.ParentGrantID = "leaf"
				lookup.grants["root"] = root
				want = comitia.ErrInvalid
			}
			policy := comitia.NewGrantEvaluator(lookup)
			ok, err := policy.Effective(leaf, now)
			if ok {
				t.Fatal("broken ancestry authorized")
			}
			if want == failure {
				if !errors.Is(err, failure) {
					t.Fatalf("storage failure hidden: %v", err)
				}
			} else if err != nil {
				t.Fatalf("invalid ancestry should be excluded: %v", err)
			}
			if _, err := policy.End(leaf.ID); !errors.Is(err, want) {
				t.Fatalf("archive error=%v want=%v", err, want)
			}
		})
	}
}

type mixedPolicyLookup struct {
	policyLookup
	failures map[string]error
}

func (r mixedPolicyLookup) Session(id string) (comitia.Session, error) {
	if err := r.failures[id]; err != nil {
		return comitia.Session{}, err
	}
	return r.policyLookup.Session(id)
}

func TestExternalGrantPolicyPreservesMixedLookupErrors(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	failure := errors.New("backend unavailable")
	for _, invalid := range []error{comitia.ErrNotFound, comitia.ErrInvalid} {
		for _, failedID := range []string{"owner", "issuer"} {
			t.Run(invalid.Error()+"/"+failedID, func(t *testing.T) {
				otherID := "owner"
				if failedID == otherID {
					otherID = "issuer"
				}
				lookup := mixedPolicyLookup{failures: map[string]error{failedID: failure, otherID: invalid}}
				g := comitia.Grant{ID: "grant", SessionID: "owner", IssuerSessionID: "issuer", ExpiresAt: now.Add(time.Hour)}
				policy := comitia.NewGrantEvaluator(lookup)
				for i := 0; i < 2; i++ {
					ok, err := policy.Effective(g, now)
					if ok || !errors.Is(err, failure) {
						t.Errorf("Effective: ok=%t err=%v; want backend failure", ok, err)
					}
					if _, err := policy.End(g.ID); !errors.Is(err, failure) {
						t.Errorf("End: err=%v; want backend failure", err)
					}
				}
			})
		}
	}
}
