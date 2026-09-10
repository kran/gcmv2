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
	items, total, err := service.QueryPage(t.Context(), ListQuery{
		Type: "article", Where: gquery.True(), Scope: scope,
		Page: gquery.Page{Number: 1, Size: 20},
	})
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
	items, err := service.Query(t.Context(), ListQuery{
		Type: "article", Scope: PolicyScope(nil), Page: gquery.Page{Size: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("nil policy returned %#v", items)
	}
}

func TestQueryWithoutPolicyScopeFails(t *testing.T) {
	service := newTestService(t)
	_, err := service.Query(t.Context(), ListQuery{
		Type: "article", Page: gquery.Page{Size: 20},
	})
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("error = %v, want ErrInvalidQuery", err)
	}
}
