package ontology

import (
	"sort"
	"strings"
)

// Approve marks every proposed node approved and records who accepted the
// document. Rejected nodes are left alone: a rejection is a decision, not pending
// work. The revision is bumped so the approval is visible in the artifact.
//
// Approval is the operator's act, not the machine review's: the rows keep their
// provenance (seed / llm-review / hs / …), and the document records the reviewer.
func Approve(document *Ontology, reviewer string) int {
	if document == nil {
		return 0
	}
	approved := 0
	for _, node := range document.Nodes {
		if node != nil && node.Status == StatusProposed {
			node.Status = StatusApproved
			approved++
		}
	}
	if reviewer = strings.TrimSpace(reviewer); reviewer != "" {
		document.ReviewedBy = reviewer
	}
	document.Revision = approvedRevision(document.Revision)
	return approved
}

// approvedRevision marks the revision as approved once, so re-running the command
// is idempotent.
func approvedRevision(revision string) string {
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return "approved"
	}
	if strings.HasSuffix(revision, "-approved") {
		return revision
	}
	return revision + "-approved"
}

// PendingApproval returns the nodes still awaiting review, in a stable order.
func (o *Ontology) PendingApproval() []string {
	var ids []string
	for id, node := range o.Nodes {
		if node != nil && node.Status == StatusProposed {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
