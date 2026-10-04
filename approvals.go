package comitia

import (
	"encoding/json"
	"strings"
	"unicode"
)

const ApprovalSchema = "comitia.approval.v1"

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

func validApproval(raw json.RawMessage) error {
	var p ApprovalPayload
	if strictPayload(raw, &p) != nil || len(p.ArtifactRefs) == 0 || !nonempty(p.Checks) || p.BlockingCount == nil || *p.BlockingCount < 0 {
		return ErrInvalid
	}
	for _, ref := range p.ArtifactRefs {
		if !exactArtifactRef(ref) {
			return ErrInvalid
		}
	}
	switch p.Verdict {
	case ApprovalApproved:
		if *p.BlockingCount != 0 {
			return ErrInvalid
		}
	case ApprovalChangesRequested:
		if *p.BlockingCount == 0 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

// exactArtifactRef accepts "scheme:value" without whitespace. The git and pr
// schemes must pin a commit (git:<sha>, pr:owner/repo#41@<sha>) so an approval
// never silently carries over to later pushes.
func exactArtifactRef(ref string) bool {
	scheme, value, ok := strings.Cut(ref, ":")
	if !ok || scheme == "" || value == "" || len(ref) > 512 || strings.IndexFunc(ref, func(c rune) bool { return unicode.IsSpace(c) || unicode.IsControl(c) }) >= 0 {
		return false
	}
	switch scheme {
	case "git":
		return commitSHA(value)
	case "pr":
		at := strings.LastIndexByte(value, '@')
		return at > 0 && commitSHA(value[at+1:])
	}
	return true
}

func commitSHA(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
