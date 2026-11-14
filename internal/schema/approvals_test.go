package schema

import (
	"encoding/json"
	"testing"
)

// sha is a full commit ID, as in the service tests of approvals.
const sha = "0123456789abcdef0123456789abcdef01234567"

func TestApprovalPayloadValidation(t *testing.T) {
	tests := []struct {
		name, payload string
		ok            bool
	}{
		{"approved commit", `{"artifact_refs":["git:` + sha + `"],"verdict":"approved","checks":["go test -race ./..."],"blocking_count":0}`, true},
		{"approved pr at sha", `{"artifact_refs":["pr:krimvp/comitia#41@` + sha + `"],"verdict":"approved","checks":["review"],"blocking_count":0,"summary":"LGTM"}`, true},
		{"changes requested", `{"artifact_refs":["git:` + sha + `"],"verdict":"changes-requested","checks":["review"],"blocking_count":2}`, true},
		{"other scheme", `{"artifact_refs":["sha256:feed"],"verdict":"approved","checks":["review"],"blocking_count":0}`, true},
		{"branch is not exact", `{"artifact_refs":["git:main"],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"uppercase pr scheme", `{"artifact_refs":["PR:owner/repo#41"],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"mixed-case git scheme", `{"artifact_refs":["Git:main"],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"short sha", `{"artifact_refs":["git:abcdef1"],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"pr without number", `{"artifact_refs":["pr:x@` + sha + `"],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"pr without sha", `{"artifact_refs":["pr:krimvp/comitia#41"],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"no scheme", `{"artifact_refs":["0123abcd"],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"whitespace", `{"artifact_refs":["git:` + sha + ` "],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"no refs", `{"artifact_refs":[],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"no checks", `{"artifact_refs":["git:` + sha + `"],"verdict":"approved","checks":[],"blocking_count":0}`, false},
		{"missing blocking count", `{"artifact_refs":["git:` + sha + `"],"verdict":"approved","checks":["review"]}`, false},
		{"negative blocking count", `{"artifact_refs":["git:` + sha + `"],"verdict":"changes-requested","checks":["review"],"blocking_count":-1}`, false},
		{"fractional blocking count", `{"artifact_refs":["git:` + sha + `"],"verdict":"changes-requested","checks":["review"],"blocking_count":1.5}`, false},
		{"approved with blockers", `{"artifact_refs":["git:` + sha + `"],"verdict":"approved","checks":["review"],"blocking_count":1}`, false},
		{"changes without blockers", `{"artifact_refs":["git:` + sha + `"],"verdict":"changes-requested","checks":["review"],"blocking_count":0}`, false},
		{"unknown verdict", `{"artifact_refs":["git:` + sha + `"],"verdict":"approve","checks":["review"],"blocking_count":0}`, false},
		{"unknown field", `{"artifact_refs":["git:` + sha + `"],"verdict":"approved","checks":["review"],"blocking_count":0,"merge":true}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidApproval(json.RawMessage(tc.payload)); (err == nil) != tc.ok {
				t.Fatalf("got %v want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestExactArtifactRef(t *testing.T) {
	sha256 := sha + "0123456789abcdef01234567"
	for ref, ok := range map[string]bool{
		"git:" + sha:                     true,
		"git:" + sha256:                  true,
		"pr:krimvp/comitia#41@" + sha:    true,
		"pr:krimvp/comitia#41@" + sha256: true,
		"sha256:feed":                    true,
		"ci+run.v2:1842":                 true,
		"git:abcdef1":                    false,
		"git:" + sha[:39]:                false,
		"git:0123456789ABCDEF0123456789ABCDEF01234567": false,
		"git:main":                    false,
		"GIT:main":                    false,
		"Git:main":                    false,
		"GIT:" + sha:                  false,
		"PR:owner/repo#41":            false,
		"Sha256:feed":                 false,
		"1git:" + sha:                 false,
		"pr:x@" + sha:                 false,
		"pr:x#@" + sha:                false,
		"pr:a#b#c@" + sha:             false,
		"pr:x#1@" + sha:               false,
		"pr:/repo#1@" + sha:           false,
		"pr:owner/#1@" + sha:          false,
		"pr:a/b/c#1@" + sha:           false,
		"pr:a/b#0@" + sha:             false,
		"pr:a/b#01@" + sha:            false,
		"pr:a/b#-1@" + sha:            false,
		"pr:a/b#1@abcdef1":            false,
		"pr:a/b#1@" + sha + "@" + sha: false,
		"pr:a/b#1":                    false,
		"main":                        false,
		":x":                          false,
		"git:":                        false,
	} {
		if ExactArtifactRef(ref) != ok {
			t.Errorf("%q: want %v", ref, ok)
		}
	}
}
