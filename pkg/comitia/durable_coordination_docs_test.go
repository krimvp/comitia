package comitia

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type coordinationDocRule struct {
	name, section string
	phrases       []string
}

// These external contracts have no in-process launcher/GitHub implementation.
// Pin the documented safety boundaries rather than treating schema tests as
// coverage of process supervision or one-shot external mutation.
func coordinationDocRules() []coordinationDocRule {
	const supervisor = "## Supervised process registry and steering"
	const merge = "## Exact-SHA certificates and merge reconciliation"
	return []coordinationDocRule{
		{"trusted-registry", supervisor, []string{"The launcher owns a durable process registry.", "must not replace the launcher's trusted registry"}},
		{"process-identity", supervisor, []string{"`launch_id`", "`generation`", "`host_id`", "`boot_id`", "`pid`", "`process_started_at`", "A matching PID alone is insufficient."}},
		{"launch-recovery", supervisor, []string{"Persist launch intent before spawning", "child identity and observed exit", "On launcher restart, reconcile registry entries"}},
		{"bounded-checkpoints", supervisor, []string{"`checkpoint_deadline`", "Have workers reach bounded checkpoints", "every 30 seconds", "bounded tool operations"}},
		{"separate-consumers", supervisor, []string{"`worker_instance`", "`watcher_instance`", "it has its own cursor", "does not advance the worker's business cursor", "presence proves only watcher API activity"}},
		{"steering-receipts", supervisor, []string{"`control_request_id`", "`control_receipt_id`", "`receipt.v1 accepted`", "`completed` with a checkpoint/result reference after applying it", "do not infer acknowledgement from its presence heartbeat or watcher output"}},
		{"safe-reclaim", supervisor, []string{"A missed checkpoint/deadline means unresolved or unresponsive, not dead.", "reconcile the process and outstanding external effects", "stop/confirm the old process through the launcher", "Never reclaim solely because last_seen is stale."}},
		{"external-secret-free-launcher", supervisor, []string{"Never store environment dumps", "credentials, claim tokens or secret instructions", "Launching, sending OS signals", "GitHub actions remain external."}},
		{"fresh-head-policy", merge, []string{"trusted policy", "authenticated requester", "required reviewer roles mapped to session IDs", "invalidates it on any newer changes-requested verdict", "Recheck GitHub head immediately before mutation", "A new push always needs a new request"}},
		{"one-shot-reservation", merge, []string{"Durably reserve `consumption_key`", "Record `reserved` before attempting GitHub", "same external lock/unique reservation", "cannot spend the certificate again", "expected-head precondition"}},
		{"ambiguous-merge-recovery", merge, []string{"If the response is lost or the process crashes, leave the operation unresolved.", "Query GitHub's PR state, recorded head and resulting merge commit before deciding whether to retry", "“PR closed” alone does not prove it merged", "Unknown results have no final receipt yet."}},
		{"receipt-retry-not-merge-retry", merge, []string{"If receipt append/response fails, retry the same message; do not run the merge again.", "a new attempt requires explicit policy authorization and a new certificate/key", "Comitia dedupe makes receipt publication recoverable, not GitHub mutation atomic"}},
	}
}

func coordinationDocSection(doc, heading string) string {
	_, section, found := strings.Cut(doc, heading)
	if !found {
		return ""
	}
	section, _, _ = strings.Cut(section, "\n## ")
	return strings.Join(strings.Fields(section), " ")
}

func checkCoordinationDocRule(doc string, rule coordinationDocRule) bool {
	section := coordinationDocSection(doc, rule.section)
	for _, phrase := range rule.phrases {
		if !strings.Contains(section, phrase) {
			return false
		}
	}
	return true
}

// durableCoordinationDoc is the document these tests pin, relative to this
// package's directory, pkg/comitia.
var durableCoordinationDoc = filepath.Join("..", "..", "docs", "durable-coordination.md")

func TestDurableCoordinationDocumentation(t *testing.T) {
	raw, err := os.ReadFile(durableCoordinationDoc)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range coordinationDocRules() {
		t.Run(rule.name, func(t *testing.T) {
			if !checkCoordinationDocRule(string(raw), rule) {
				t.Fatalf("missing documented %s contract in %s", rule.name, rule.section)
			}
		})
	}
}

func TestDurableCoordinationDocumentationMutationSensitivity(t *testing.T) {
	raw, err := os.ReadFile(durableCoordinationDoc)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, rule := range coordinationDocRules() {
		t.Run(rule.name, func(t *testing.T) {
			// Removing either entire recipe must not leave its original finding green.
			if checkCoordinationDocRule("", rule) {
				t.Fatal("removed recipe accepted")
			}
			// Corrupting each pinned boundary must also fail, retaining other contracts.
			for _, phrase := range rule.phrases {
				section := coordinationDocSection(doc, rule.section)
				corrupted := rule.section + "\n" + strings.ReplaceAll(section, phrase, "[contract removed]")
				if checkCoordinationDocRule(corrupted, rule) {
					t.Fatalf("undetected contract removal: %s", phrase)
				}
			}
		})
	}
}
