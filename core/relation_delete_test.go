package core

import (
	"errors"
	"testing"

	gquery "github.com/kran/gcmv2/query"
	"github.com/kran/gcmv2/types"
)

const deletePolicyTypes = `
types:
  account:
    fields:
      - { name: name, kind: text }
  contact:
    fields:
      - { name: name, kind: text }
  note:
    fields:
      - { name: text, kind: text }
      - { name: account, kind: ref, to: account }
  contract:
    fields:
      - { name: name, kind: text }
      - { name: account, kind: ref, to: account, required: true }
  employment:
    capabilities:
      relation: { from: contact, to: account }
    fields:
      - { name: contact, kind: ref, to: contact, required: true, on_delete: cascade }
      - { name: account, kind: ref, to: account, required: true, on_delete: cascade }
      - { name: title, kind: text }
`

func newDeletePolicyService(t *testing.T) *Service {
	t.Helper()
	return New(testDB(t), newTypes(t, deletePolicyTypes))
}

func TestDeletePolicies(t *testing.T) {
	service := newDeletePolicyService(t)
	account, _ := service.CreateNode(&Node{Type: "account", Display: "account", Fields: Fields{"name": "account"}})
	contact, _ := service.CreateNode(&Node{Type: "contact", Display: "contact", Fields: Fields{"name": "contact"}})
	note, _ := service.CreateNode(&Node{Type: "note", Display: "note", Fields: Fields{"text": "note", "account": account}})
	contract, _ := service.CreateNode(&Node{Type: "contract", Display: "contract", Fields: Fields{"name": "contract", "account": account}})
	employment, _ := service.CreateNode(&Node{Type: "employment", Display: "employment", Fields: Fields{
		"contact": contact, "account": account, "title": "manager",
	}})
	inbound, _, err := service.InEdges(account, "account", 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range inbound {
		if edge.FromNode == contract {
			if err := service.RemoveEdge(edge.ID); !errors.Is(err, ErrRequiredReference) {
				t.Fatalf("remove required ref = %v", err)
			}
		}
	}

	err = service.DeleteNode(account)
	if !errors.Is(err, ErrDeleteRestricted) {
		t.Fatalf("delete with required inbound ref = %v", err)
	}
	var restricted *DeleteRestrictedError
	if !errors.As(err, &restricted) || len(restricted.References) != 1 || restricted.References[0].SourceID != contract {
		t.Fatalf("restricted details = %#v", restricted)
	}
	if node, _ := service.GetNodeById(account); node == nil {
		t.Fatal("restricted delete must roll back")
	}

	if err := service.DeleteNode(contract); err != nil {
		t.Fatal(err)
	}
	noteBefore, _ := service.GetNodeById(note)
	if err := service.DeleteNode(account); err != nil {
		t.Fatal(err)
	}
	if node, _ := service.GetNodeById(employment); node != nil {
		t.Fatal("cascade relation node must be deleted")
	}
	if node, _ := service.GetNodeById(note); node == nil {
		t.Fatal("set_null source must remain")
	} else if node.Revision != noteBefore.Revision+1 {
		t.Fatalf("set_null source revision = %d, want %d", node.Revision, noteBefore.Revision+1)
	}
	if _, found, err := service.RefID(t.Context(), note, "account"); err != nil || found {
		t.Fatalf("set_null ref = found %v, err %v", found, err)
	}
}

func TestArchivePreservesEdgesAndRestore(t *testing.T) {
	service := newDeletePolicyService(t)
	account, _ := service.CreateNode(&Node{Type: "account", Display: "account", Fields: Fields{"name": "account"}})
	contract, _ := service.CreateNode(&Node{Type: "contract", Display: "contract", Fields: Fields{"name": "contract", "account": account}})
	node, _ := service.GetNodeById(account)
	if err := service.ArchiveNode(t.Context(), account, node.Revision); err != nil {
		t.Fatal(err)
	}
	name := "changed"
	if err := service.PatchNode(account, &NodePatch{Revision: &node.Revision, Display: &name}); !errors.Is(err, ErrNodeArchived) {
		t.Fatalf("patch archived node = %v", err)
	}
	if target, found, err := service.RefID(t.Context(), contract, "account"); err != nil || !found || target != account {
		t.Fatalf("archived ref = %d, %v, %v", target, found, err)
	}
	matching := queryAll(t, service, "contract", gquery.OneOf(gquery.Ref("account"), account))
	if len(matching) != 0 {
		t.Fatalf("query must hide archived relation target: %#v", matching)
	}
	expanded, err := service.Expand(t.Context(), contract, gquery.Expand(gquery.Ref("account")))
	if err != nil {
		t.Fatal(err)
	}
	if _, visible := expanded.Expand["account"]; visible {
		t.Fatalf("expand must hide archived relation target: %#v", expanded.Expand)
	}
	report, err := service.CheckRelations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !hasRelationIssue(report, RelationArchivedRequired) {
		t.Fatalf("missing archived required issue: %#v", report)
	}
	archived, _ := service.GetNodeById(account)
	if err := service.RestoreNode(t.Context(), account, archived.Revision); err != nil {
		t.Fatal(err)
	}
	report, err = service.CheckRelations(t.Context())
	if err != nil || !report.OK() {
		t.Fatalf("restored integrity = %#v, %v", report, err)
	}
}

func TestRelationIntegrityDetectsCorruption(t *testing.T) {
	service := newDeletePolicyService(t)
	accountA, _ := service.CreateNode(&Node{Type: "account", Display: "a", Fields: Fields{"name": "a"}})
	accountB, _ := service.CreateNode(&Node{Type: "account", Display: "b", Fields: Fields{"name": "b"}})
	contract, _ := service.CreateNode(&Node{Type: "contract", Display: "contract", Fields: Fields{"name": "contract", "account": accountA}})
	missing, _ := service.CreateNode(&Node{Type: "contract", Display: "missing", Fields: Fields{"name": "missing", "account": accountA}})
	_, err := service.db.Delete("edges", `from_node = #{1} AND field = 'account'`, missing).Exec()
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.db.Add(`INSERT INTO edges
		(from_node, field, to_node, sort, single_ref, symmetric, created_at)
		VALUES (#{1}, 'account', #{2}, 0, 0, 0, CURRENT_TIMESTAMP)`, contract, accountB).Exec()
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.db.Add(`INSERT INTO edges
		(from_node, field, to_node, sort, single_ref, symmetric, created_at)
		VALUES (#{1}, 'ghost', #{2}, 0, 0, 0, CURRENT_TIMESTAMP)`, contract, accountB).Exec()
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.CheckRelations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []RelationIssueKind{
		RelationCardinality, RelationMetadataMismatch, RelationUnknownField, RelationRequiredMissing,
	} {
		if !hasRelationIssue(report, kind) {
			t.Fatalf("missing %s in %#v", kind, report)
		}
	}
	if err := service.SyncRelationSchema(); err == nil {
		t.Fatal("schema synchronization must reject corrupt single-ref data")
	}
}

func hasRelationIssue(report RelationReport, kind RelationIssueKind) bool {
	for _, issue := range report.Issues {
		if issue.Kind == kind {
			return true
		}
	}
	return false
}

func TestRelationIntegrityDetectsTreeCycle(t *testing.T) {
	service := newTraverseService(t)
	a, _ := service.CreateNode(&Node{Type: "category", Display: "a", Fields: Fields{"name": "a"}})
	b, _ := service.CreateNode(&Node{Type: "category", Display: "b", Fields: Fields{"name": "b", "parent": a}})
	_, err := service.db.Add(`INSERT INTO edges
		(from_node, field, to_node, sort, single_ref, symmetric, created_at)
		VALUES (#{1}, 'parent', #{2}, 0, 1, 0, CURRENT_TIMESTAMP)`, a, b).Exec()
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.CheckRelations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !hasRelationIssue(report, RelationCycle) {
		t.Fatalf("missing cycle issue: %#v", report)
	}
}

func TestDeletePolicyDefaults(t *testing.T) {
	typeSet := newTypes(t, deletePolicyTypes)
	optional, _ := typeSet.Field("note", "account")
	if optional.OnDelete != types.OnDeleteSetNull {
		t.Fatalf("optional on_delete = %q", optional.OnDelete)
	}
	required, _ := typeSet.Field("contract", "account")
	if required.OnDelete != types.OnDeleteRestrict {
		t.Fatalf("required on_delete = %q", required.OnDelete)
	}
}
