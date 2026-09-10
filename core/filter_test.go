package core

import (
	"context"
	"errors"
	"testing"

	gquery "github.com/kran/gcmv2/query"
)

func queryAll(t *testing.T, service *Service, typeName string, where gquery.Expr) []Node {
	t.Helper()
	list, _, err := service.QueryPage(t.Context(), ListQuery{
		Type: typeName, Where: where, Scope: BypassPolicy(),
		Page: gquery.Page{Number: 1, Size: 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestQueryBuilderScalarAndLogic(t *testing.T) {
	s := newTestService(t)
	s.CreateNode(t.Context(), &Node{Type: "article", Display: "甲", Fields: Fields{"title": "甲", "views": 100, "publication_state": "published"}})
	s.CreateNode(t.Context(), &Node{Type: "article", Display: "乙", Fields: Fields{"title": "乙", "views": 5, "publication_state": "published"}})
	s.CreateNode(t.Context(), &Node{Type: "article", Display: "丙", Fields: Fields{"title": "丙", "views": 100, "publication_state": "draft"}})

	where := gquery.And(
		gquery.EQ(gquery.Field("publication_state"), "published"),
		gquery.GT(gquery.Field("views"), 50),
	)
	list := queryAll(t, s, "article", where)
	if len(list) != 1 || list[0].Fields.Str("title") != "甲" {
		t.Fatalf("and query = %#v", list)
	}

	list = queryAll(t, s, "article", gquery.Or(
		gquery.EQ(gquery.Field("publication_state"), "draft"),
		gquery.EQ(gquery.Field("title"), "甲"),
	))
	if len(list) != 2 {
		t.Fatalf("or query length = %d", len(list))
	}

	list = queryAll(t, s, "article", gquery.Contains(gquery.System("display"), "乙"))
	if len(list) != 1 || list[0].Display != "乙" {
		t.Fatalf("contains query = %#v", list)
	}
}

func TestQueryBuilderRelations(t *testing.T) {
	s := newTestService(t)
	categoryID, _ := s.CreateNode(t.Context(), &Node{
		Type: "category", Display: "分类",
		Fields: Fields{"name": "分类", "publication_state": "published"},
	})
	articleID, _ := s.CreateNode(t.Context(), &Node{
		Type: "article", Display: "文章",
		Fields: Fields{"title": "文章", "categories": []any{categoryID}},
	})

	list := queryAll(t, s, "article", gquery.OneOf(gquery.Ref("categories"), categoryID))
	if len(list) != 1 || list[0].ID != articleID {
		t.Fatalf("out relation = %#v", list)
	}
	list = queryAll(t, s, "category", gquery.IsNotNull(gquery.Incoming("article", "categories")))
	if len(list) != 1 || list[0].ID != categoryID {
		t.Fatalf("incoming relation = %#v", list)
	}
	list = queryAll(t, s, "article", gquery.RelatedTo(
		gquery.Ref("categories"),
		gquery.EQ(gquery.Field("name"), "分类"),
	))
	if len(list) != 1 || list[0].ID != articleID {
		t.Fatalf("related query = %#v", list)
	}
}

func TestQuerySubtreeSet(t *testing.T) {
	s := newTestService(t)
	root, _ := s.CreateNode(t.Context(), &Node{
		Type: "category", Display: "根",
		Fields: Fields{"name": "根", "slug": "root", "publication_state": "published"},
	})
	child, _ := s.CreateNode(t.Context(), &Node{
		Type: "category", Display: "子",
		Fields: Fields{"name": "子", "publication_state": "published", "parent": root},
	})
	articleID, _ := s.CreateNode(t.Context(), &Node{
		Type: "article", Display: "文章",
		Fields: Fields{"title": "文章", "categories": []any{child}},
	})

	list := queryAll(t, s, "article", gquery.InSet(gquery.Ref("categories"), gquery.SubtreeOf("root")))
	if len(list) != 1 || list[0].ID != articleID {
		t.Fatalf("subtree query = %#v", list)
	}
}

func TestLispProducesTypedQuery(t *testing.T) {
	s := newTestService(t)
	s.CreateNode(t.Context(), &Node{Type: "article", Display: "甲", Fields: Fields{"title": "甲", "views": 100}})
	where, err := gquery.ParseLisp(`(and (= $title {:title}) (> $views 50))`, map[string]any{"title": "甲"})
	if err != nil {
		t.Fatal(err)
	}
	list := queryAll(t, s, "article", where)
	if len(list) != 1 {
		t.Fatalf("lisp query = %#v", list)
	}
}

func TestQuerySchemaValidation(t *testing.T) {
	s := newTestService(t)
	cases := []struct {
		name  string
		where gquery.Expr
		want  error
	}{
		{"unknown field", gquery.EQ(gquery.Field("missing"), "x"), ErrInvalidField},
		{"range on text", gquery.GT(gquery.Field("title"), "x"), ErrInvalidOperator},
		{"bad select", gquery.EQ(gquery.Field("publication_state"), "unknown"), ErrInvalidValue},
		{"relation as scalar", gquery.EQ(gquery.Ref("categories"), 1), ErrInvalidOperator},
		{"wrong incoming target", gquery.IsNotNull(gquery.Incoming("person", "name")), ErrInvalidField},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := s.Query(t.Context(), ListQuery{
				Type: "article", Where: test.where, Scope: BypassPolicy(),
				Page: gquery.Page{Size: 10},
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestQueryRequiresType(t *testing.T) {
	s := newTestService(t)
	_, err := s.Query(t.Context(), ListQuery{Page: gquery.Page{Size: 10}})
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("error = %v", err)
	}
}

func TestQueryHonorsCanceledContext(t *testing.T) {
	s := newTestService(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := s.Query(ctx, ListQuery{
		Type: "article", Scope: BypassPolicy(), Page: gquery.Page{Size: 10},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

// 写路径和图原语同样必须响应请求取消: 不能出现“部分 API 带 Context”的双轨。
func TestWriteAndGraphHonorCanceledContext(t *testing.T) {
	s := newTraverseService(t)
	root, _ := s.CreateNode(t.Context(), &Node{Type: "category", Display: "root", Fields: Fields{"name": "root"}})
	child, _ := s.CreateNode(t.Context(), &Node{Type: "category", Display: "child", Fields: Fields{"name": "child", "parent": root}})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := s.CreateNode(ctx, &Node{Type: "category", Display: "x", Fields: Fields{"name": "x"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateNode error = %v, want context.Canceled", err)
	}
	display := "patched"
	current, _ := s.GetNodeById(t.Context(), child)
	if err := s.PatchNode(ctx, child, &NodePatch{Revision: &current.Revision, Display: &display}); !errors.Is(err, context.Canceled) {
		t.Fatalf("PatchNode error = %v, want context.Canceled", err)
	}
	if err := s.DeleteNode(ctx, child); !errors.Is(err, context.Canceled) {
		t.Fatalf("DeleteNode error = %v, want context.Canceled", err)
	}
	if _, err := s.LoadTree(ctx, "category", BypassPolicy()); !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadTree error = %v, want context.Canceled", err)
	}
	if _, err := s.Traverse(ctx, "category", child, "parent", 5); !errors.Is(err, context.Canceled) {
		t.Fatalf("Traverse error = %v, want context.Canceled", err)
	}
	if _, err := s.GetNodeById(ctx, child); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetNodeById error = %v, want context.Canceled", err)
	}
	if err := s.RebuildSearch(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("RebuildSearch error = %v, want context.Canceled", err)
	}
}
