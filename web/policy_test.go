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
	publishedID, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "article", Display: "published",
		Fields: core.Fields{"publication_state": "published", "body": "visible"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = site.Engine().CreateNode(t.Context(), &core.Node{
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
	_, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "article", Display: "published",
		Fields: core.Fields{"publication_state": "published", "body": "visible"},
	})
	if err != nil {
		t.Fatal(err)
	}
	draftID, err := site.Engine().CreateNode(t.Context(), &core.Node{
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
	_, err := site.Engine().CreateNode(t.Context(), &core.Node{
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

// testSystemActions lists the framework actions explicitly: adding a system
// action must touch this list and policyDefaults in the same change.
var testSystemActions = []PolicyAction{PolicyList, PolicyView, PolicySearch, PolicyExport}

func TestPolicyDefaultsCoverSystemActions(t *testing.T) {
	if len(policyDefaults) != len(testSystemActions) {
		t.Fatalf("policyDefaults has %d entries, system actions are %d", len(policyDefaults), len(testSystemActions))
	}
	for _, action := range testSystemActions {
		def, ok := policyDefaults[action]
		if !ok {
			t.Fatalf("system action %q has no default", action)
		}
		if def != PolicyDefaultPublishedOnly {
			t.Fatalf("system action %q default = %d, want PolicyDefaultPublishedOnly", action, def)
		}
	}
}

func TestUnknownPolicyActionIsRejected(t *testing.T) {
	site := testSite(t)
	ctx := site.CmsCtxMaker(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	for _, action := range []PolicyAction{"", "viwe"} {
		if _, err := site.Policy().Scope(ctx, action, "article"); err == nil {
			t.Fatalf("Scope(%q) accepted an unknown action", action)
		}
	}
	assertPanics(t, func() {
		testSiteConfigured(t, func(site *Site) {
			site.Policy().Register("article", "viwe", func(*CmsCtx, PolicyRequest) (gquery.Expr, error) {
				return gquery.True(), nil
			})
		})
	})
}

func TestSiteActionMustBeNamespaced(t *testing.T) {
	assertPanics(t, func() {
		testSiteConfigured(t, func(site *Site) {
			site.Policy().Register("article", "my_content", func(*CmsCtx, PolicyRequest) (gquery.Expr, error) {
				return gquery.True(), nil
			})
		})
	})
}

func TestSiteActionResolvesOnlyWhereRegistered(t *testing.T) {
	site := testSiteConfigured(t, func(site *Site) {
		site.Policy().Register("guestbook", "site.mine", func(*CmsCtx, PolicyRequest) (gquery.Expr, error) {
			return gquery.True(), nil
		})
	})
	if _, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "guestbook", Display: "note", Fields: core.Fields{"title": "note"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "article", Display: "published",
		Fields: core.Fields{"publication_state": "published", "body": "visible"},
	}); err != nil {
		t.Fatal(err)
	}
	ctx := site.CmsCtxMaker(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	scope, err := site.Policy().Scope(ctx, "site.mine", "guestbook")
	if err != nil {
		t.Fatal(err)
	}
	_, total, err := site.Engine().QueryPage(t.Context(), core.ListQuery{
		Type: "guestbook", Where: gquery.True(), Scope: scope,
		Page: gquery.Page{Number: 1, Size: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("registered site action exposed %d rows, want 1", total)
	}

	if _, err := site.Policy().Scope(ctx, "site.mine", "article"); err == nil {
		t.Fatal("site action resolved for a Type it was not registered on")
	}

	scope, err = site.Policy().Scope(ctx, PolicyList, "guestbook")
	if err != nil {
		t.Fatal(err)
	}
	_, total, err = site.Engine().QueryPage(t.Context(), core.ListQuery{
		Type: "guestbook", Where: gquery.True(), Scope: scope,
		Page: gquery.Page{Number: 1, Size: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatalf("system action default changed: %d rows", total)
	}
}

func TestDefaultScopeForEveryReadAction(t *testing.T) {
	site := testSite(t)
	publishedID, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "article", Display: "published",
		Fields: core.Fields{"publication_state": "published", "body": "visible"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "article", Display: "draft",
		Fields: core.Fields{"publication_state": "draft", "body": "hidden"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "guestbook", Display: "note", Fields: core.Fields{"title": "note"},
	}); err != nil {
		t.Fatal(err)
	}
	ctx := site.CmsCtxMaker(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	for _, action := range testSystemActions {
		scope, err := site.Policy().Scope(ctx, action, "article")
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		items, total, err := site.Engine().QueryPage(t.Context(), core.ListQuery{
			Type: "article", Where: gquery.True(), Scope: scope,
			Page: gquery.Page{Number: 1, Size: 20},
		})
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if total != 1 || len(items) != 1 || items[0].ID != publishedID {
			t.Fatalf("%s default = %#v (total %d), want only the published row", action, items, total)
		}
		denied, err := site.Policy().Scope(ctx, action, "guestbook")
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		_, total, err = site.Engine().QueryPage(t.Context(), core.ListQuery{
			Type: "guestbook", Where: gquery.True(), Scope: denied,
			Page: gquery.Page{Number: 1, Size: 20},
		})
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if total != 0 {
			t.Fatalf("%s exposed %d guestbook rows without publication", action, total)
		}
	}
}
