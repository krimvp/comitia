package schema

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/krimvp/comitia/internal/model"
)

type ApprovalVerdict string

const (
	ApprovalApproved         ApprovalVerdict = "approved"
	ApprovalChangesRequested ApprovalVerdict = "changes-requested"
)

// ApprovalPayload is a reviewer's verdict on exact artifacts. Comitia checks its
// shape, not the review; a merge gate still decides whose approvals count.
type ApprovalPayload struct {
	ArtifactRefs  []string        `json:"artifact_refs"`
	Verdict       ApprovalVerdict `json:"verdict"`
	Checks        []string        `json:"checks"`
	BlockingCount *int            `json:"blocking_count"`
	Summary       string          `json:"summary,omitempty"`
}

func ValidApproval(raw json.RawMessage) error {
	var p ApprovalPayload
	if err := StrictPayload(raw, &p); err != nil {
		return err
	}
	if len(p.ArtifactRefs) == 0 || !NonEmpty(p.Checks) || p.BlockingCount == nil || *p.BlockingCount < 0 {
		return model.Invalidf("artifact_refs and checks required; blocking_count must be nonnegative")
	}
	for _, ref := range p.ArtifactRefs {
		if !ExactArtifactRef(ref) {
			return model.Invalidf("artifact_refs must identify exact artifacts")
		}
	}
	switch p.Verdict {
	case ApprovalApproved:
		if *p.BlockingCount != 0 {
			return model.Invalidf("blocking_count must be zero for approved verdict")
		}
	case ApprovalChangesRequested:
		if *p.BlockingCount == 0 {
			return model.Invalidf("blocking_count must be positive for changes-requested verdict")
		}
	default:
		return model.Invalidf("verdict must be approved or changes-requested")
	}
	return nil
}

// exactArtifactRef accepts "scheme:value" with a lowercase scheme and no
// whitespace. The git and pr schemes must pin a full commit (git:<sha>,
// pr:owner/repo#41@<sha>) so an approval never silently carries over to later
// pushes or matches an abbreviated prefix.
func ExactArtifactRef(ref string) bool {
	scheme, value, ok := strings.Cut(ref, ":")
	if !ok || !lowerScheme(scheme) || value == "" || len(ref) > 512 || strings.IndexFunc(ref, func(c rune) bool { return unicode.IsSpace(c) || unicode.IsControl(c) }) >= 0 {
		return false
	}
	switch scheme {
	case "git":
		return fullCommitSHA(value)
	case "pr":
		return pullRequestRef(value)
	}
	return true
}

func lowerScheme(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

// pullRequestRef parses exactly <owner>/<repo>#<positive int>@<sha>.
func pullRequestRef(v string) bool {
	if strings.Count(v, "#") != 1 || strings.Count(v, "@") != 1 {
		return false
	}
	repo, rest, _ := strings.Cut(v, "#")
	number, sha, ok := strings.Cut(rest, "@")
	owner, name, slash := strings.Cut(repo, "/")
	if !ok || !slash || owner == "" || name == "" || strings.Contains(name, "/") || number == "" || number[0] == '0' {
		return false
	}
	for _, c := range number {
		if c < '0' || c > '9' {
			return false
		}
	}
	return fullCommitSHA(sha)
}

// Full SHA-1 or SHA-256 object names only; abbreviations are not exact.
func fullCommitSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
