package core

import (
	"errors"
	"testing"

	gquery "github.com/kran/gcmv2/query"
)

func TestPolicyScopeCannotBeWeakenedByUserWhere(t *testing.T) {
	service := newTestService(t)
	_, err := service.CreateNode(t.Context(), &Node{
		Type: "article", Display: "public",
		Fields: Fields{"title": "public", "publication_state": "published"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateNode(t.Context(), &Node{
		Type: "article", Display: "draft",
		Fields: Fields{"title": "draft", "publication_state": "draft"},
	})
	if err != nil {
		t.Fatal(err)
	}

	scope := PolicyScope(gquery.EQ(gquery.Field("publication_state"), "published"))
	ptrs, total, err := countAndRead(t, service, NodeQuery{
		Type: "article", Where: gquery.True(), Scope: scope,
	}, 20, 0)
	items := nodeValues(ptrs)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].Display != "public" {
		t.Fatalf("scoped query = %#v, total=%d", items, total)
	}
}

func TestPolicyScopeNilDeniesAll(t *testing.T) {
	service := newTestService(t)
	_, err := service.CreateNode(t.Context(), &Node{
		Type: "article", Display: "public",
		Fields: Fields{"title": "public", "publication_state": "published"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ptrs, err := service.GetNodes(t.Context(), NodeQuery{
		Type: "article", Scope: PolicyScope(nil),
	}, 20, 0)
	items := nodeValues(ptrs)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("nil policy returned %#v", items)
	}
}

func TestQueryWithoutPolicyScopeFails(t *testing.T) {
	service := newTestService(t)
	_, err := service.GetNodes(t.Context(), NodeQuery{
		Type: "article",
	}, 20, 0)
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("error = %v, want ErrInvalidQuery", err)
	}
}
