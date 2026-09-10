package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kran/gcmv2/core"
	gquery "github.com/kran/gcmv2/query"
)

func TestDefaultPolicyUsesPublicationCapability(t *testing.T) {
	site := testSite(t)
	publishedID, err := site.Engine().CreateNode(&core.Node{
		Type: "article", Display: "published",
		Fields: core.Fields{"publication_state": "published", "body": "visible"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = site.Engine().CreateNode(&core.Node{
		Type: "article", Display: "draft",
		Fields: core.Fields{"publication_state": "draft", "body": "hidden"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := site.CmsCtxMaker(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	scope, err := site.Policy().Scope(ctx, PolicyList, "article")
	if err != nil {
		t.Fatal(err)
	}
	items, total, err := site.Engine().QueryPage(t.Context(), core.ListQuery{
		Type: "article", Where: gquery.True(), Scope: scope,
		Page: gquery.Page{Number: 1, Size: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].ID != publishedID {
		t.Fatalf("default policy = %#v, total=%d", items, total)
	}
}

func TestCustomPolicyUsesActorAndCannotBeWeakened(t *testing.T) {
	site := testSiteConfigured(t, func(site *Site) {
		site.Policy().Register("article", PolicyList, func(_ *CmsCtx, request PolicyRequest) (gquery.Expr, error) {
			if request.Actor.Kind == ActorAPIKey {
				return gquery.EQ(gquery.Field("publication_state"), "draft"), nil
			}
			return gquery.False(), nil
		})
	})
	_, err := site.Engine().CreateNode(&core.Node{
		Type: "article", Display: "published",
		Fields: core.Fields{"publication_state": "published", "body": "visible"},
	})
	if err != nil {
		t.Fatal(err)
	}
	draftID, err := site.Engine().CreateNode(&core.Node{
		Type: "article", Display: "draft",
		Fields: core.Fields{"publication_state": "draft", "body": "hidden"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := site.CmsCtxMaker(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	ctx.SetActor(Actor{Kind: ActorAPIKey, Scopes: []string{"draft:read"}})
	scope, err := site.Policy().Scope(ctx, PolicyList, "article")
	if err != nil {
		t.Fatal(err)
	}
	items, err := site.Engine().Query(t.Context(), core.ListQuery{
		Type: "article", Where: gquery.True(), Scope: scope, Page: gquery.Page{Size: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != draftID {
		t.Fatalf("actor policy = %#v", items)
	}
}

func TestExplicitPolicyCanExposeNonPublicationType(t *testing.T) {
	site := testSiteConfigured(t, func(site *Site) {
		site.Policy().Register("guestbook", PolicyList, func(*CmsCtx, PolicyRequest) (gquery.Expr, error) {
			return gquery.True(), nil
		})
	})
	_, err := site.Engine().CreateNode(&core.Node{
		Type: "guestbook", Display: "visible", Fields: core.Fields{"title": "visible"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := do(site, http.MethodGet, "/api/nodes/guestbook", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("custom public list = %d: %s", response.Code, response.Body.String())
	}
}

func TestPolicyRegistrationAfterStartPanics(t *testing.T) {
	site := testSite(t)
	assertPanics(t, func() {
		site.Policy().Register("article", PolicyList, func(*CmsCtx, PolicyRequest) (gquery.Expr, error) {
			return gquery.True(), nil
		})
	})
}
