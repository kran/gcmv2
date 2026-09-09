package query

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseLisp(t *testing.T) {
	expression, err := ParseLisp(`(and (= $stage "qualified") (in ->owner {:owners}) (exists <-activity.contact))`, map[string]any{
		"owners": []int64{12, 18},
	})
	if err != nil {
		t.Fatal(err)
	}
	logic, ok := expression.(Logic)
	if !ok || logic.Op != OpAnd || len(logic.Args) != 3 {
		t.Fatalf("expression = %#v", expression)
	}
	membership, ok := logic.Args[1].(In)
	if !ok || membership.Path.Kind != PathOutRef || !reflect.DeepEqual(membership.Values, []any{int64(12), int64(18)}) {
		t.Fatalf("membership = %#v", logic.Args[1])
	}
}

func TestParseLispLimits(t *testing.T) {
	_, err := ParseLisp(strings.Repeat("x", MaxFilterBytes+1), nil)
	if err == nil {
		t.Fatal("oversized expression must fail")
	}
	values := make([]int, MaxSetValues+1)
	_, err = ParseLisp(`(in id {:values})`, map[string]any{"values": values})
	if err == nil {
		t.Fatal("oversized bound collection must fail")
	}
}

func TestParseExpand(t *testing.T) {
	paths, err := ParseExpand(`author, categories.parent, <-article.categories.author`)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 || len(paths[1]) != 2 {
		t.Fatalf("paths = %#v", paths)
	}
	incoming := paths[2][0]
	if incoming.Kind != PathInRef || incoming.SourceType != "article" || incoming.Field != "categories" {
		t.Fatalf("incoming = %#v", incoming)
	}
	if ExpandKey(incoming) != "<-article.categories" {
		t.Fatalf("key = %q", ExpandKey(incoming))
	}
}

func TestDecodeSpec(t *testing.T) {
	where, sortFields, page, err := DecodeSpec(strings.NewReader(`{
		"where":{"op":"and","args":[
			{"op":"eq","field":"stage","value":"qualified"},
			{"op":"in","ref":"owner","values":[12,18]}
		]},
		"sort":[{"column":"updated_at","desc":true}],
		"page":{"number":2,"size":20}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := where.(Logic); !ok || len(sortFields) != 1 || page.Number != 2 || page.Size != 20 {
		t.Fatalf("where=%#v sort=%#v page=%#v", where, sortFields, page)
	}
}

func TestDecodeSpecRejectsUnknown(t *testing.T) {
	_, _, _, err := DecodeSpec(strings.NewReader(`{"where":{"op":"eq","field":"stage","unknown":true}}`))
	if err == nil {
		t.Fatal("unknown property must fail")
	}
}
