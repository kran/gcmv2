# Migrating to gcmv2 v0.9

v0.9 intentionally removes the CMS-specific Node columns `slug`, `status`, and `sort`.
There is no source compatibility adapter.

## Source migration

```text
Node.Slug                  -> Node.Fields.Str(addressable.field)
Node.Status                -> Node.Fields[publication.field]
Node.Sort                  -> Node.Fields[tree.order] or another business field
NodePatch.Slug/Status/Sort -> NodePatch.Fields
GetNodeBySlug              -> GetNodeByAddress
LoadTree(type, field)      -> LoadTree(ctx, type, scope)
TypeDef.Search             -> Capabilities.Searchable
TypeDef.Auth               -> Capabilities.Authentication
TypeDef.View/Icon          -> Admin.View/Admin.Icon
```

Every effective `NodePatch` must include the revision read with the Node:

```go
patch := &core.NodePatch{
    Revision: &node.Revision,
    Fields: core.Fields{"publication_state": "published"},
}
err := engine.PatchNode(node.ID, patch)
if errors.Is(err, core.ErrRevisionConflict) {
    // Reload and ask the caller to resolve the conflict.
}
```

## Database migration

Core migration `00009_node_schema_capabilities.sql`:

1. Copies every old slug/status/sort value into `legacy_node_columns`.
2. Adds revision and archived_at.
3. Removes slug/status/sort from nodes.
4. Stops all runtime reads of the old values.

Each site must then add its own migration because only the site knows what old `status` meant.
For example:

```sql
UPDATE nodes
SET fields = json_set(
    fields,
    '$.slug', (SELECT slug FROM legacy_node_columns WHERE node_id = nodes.id),
    '$.position', (SELECT sort FROM legacy_node_columns WHERE node_id = nodes.id),
    '$.publication_state', CASE
      WHEN (SELECT status FROM legacy_node_columns WHERE node_id = nodes.id) = 1
      THEN 'published' ELSE 'draft' END
);
```

If an old status represented approval rather than publication, migrate it to a different field such as `approval_state`.

After validating counts, addresses, publication visibility, and `PRAGMA integrity_check`, the site migration must drop `legacy_node_columns`.

Rollback order is the reverse:

1. Roll back the site migration so it recreates `legacy_node_columns`.
2. Roll back core migration 00009.
3. Restore the pre-upgrade backup if either step fails.

Do not deploy the v0.9 runtime with a v0.8 `types.yaml`.

## Authentication migration

Register every authentication Realm before mounting credential plugins:

```go
site.Auth().Register(web.AuthRealm{
    Name:          "member",
    NodeType:      "member",
    AllowRegister: true,
    Default:       true,
})
password.Mount(site, password.Options{Realm: "member"})
```

Update source and clients as follows:

```text
RegisterInput.Type / LoginInput.Type     -> removed
password.Mount(site)                     -> password.Mount(site, Options{Realm: ...})
/api/auth/register                       -> /api/auth/{realm}/register
/api/auth/login                          -> /api/auth/{realm}/login
/api/auth/bind                           -> /api/auth/{realm}/bind
CmsCtx.User()                            -> CmsCtx.Actor() / CmsCtx.Principal()
AuthSession(ctx, engine, nodeID)         -> AuthSession(ctx, realm, nodeID)
CreateSession(nodeID)                    -> CreateSession(realm, nodeID)
ValidSession(token) node ID result       -> ValidSession(token) *Session result
core.VerifyPassword                     -> credential plugin verification
ListQuery{...}                           -> ListQuery{Scope: core.PolicyScope(...), ...}
trusted admin/background query           -> Scope: core.BypassPolicy()
Search(q, type, page, size)              -> Search(ctx, core.SearchQuery{Targets: ...})
GET /api/nodes/mine?type=                -> site-owned owner listing (association: GET /api/me/content)
Kind.Validate(v)                         -> Kind.Validate(fieldDef, v)
TypeDef.TemplateCandidates()             -> web template candidates (web.nodeCandidates)
web.New(dir)                             -> web.Open(dir) (*Site, error)
core.New(db, ts)                         -> core.Open(db, ts) (*Service, error)
site.DB().Pool().Close()                 -> site.Close()
CreateNode/PatchNode/DeleteNode          -> ...(ctx, ...)
GetNodeById/GetNodeByAddress             -> ...(ctx, ...)
Traverse/Subtree/Ancestors/EquivalenceClass -> ...(ctx, ...)
OutEdges/InEdges/AddEdge/RemoveEdge      -> ...(ctx, ...)
RegisterAuth/FindAuth/AddAuthMethod      -> ...(ctx, ...)
CreateSession/ValidSession/DeleteSession -> ...(ctx, ...)
Settings get/set/list/delete             -> ...(ctx, ...)
RebuildSearch()/Migrator.Up/UpDir        -> ...(ctx, ...)
Render.Render(w, candidates, data)       -> Render(ctx, w, candidates, data)
```

`New` remains as a panic-on-error convenience wrapper; process entry points should use
`Open` so startup failures are logged instead of crashing with a stack trace. `Site.Close`
is idempotent.

`LoadTree` accepts an explicit `core.QueryScope`; pass the Policy scope on public routes and `core.BypassPolicy()` for internal or administrative trees. Trees no longer require a publication capability.

Core migration `00010_auth_realms.sql` replaces plaintext Session tokens with SHA-256 hashes and adds the Realm column. Existing frontend Sessions are intentionally invalidated, so users must sign in again after upgrading. `auth_methods.type` remains the authenticated NodeType; Realm-to-NodeType mapping is server configuration and is not duplicated there.

## Relation migration

Core migration `00011_edge_integrity.sql` adds storage-only `single_ref` and `symmetric` metadata plus baseline symmetric-single triggers. At startup Core derives these flags from the current Schema, validates existing data, and creates the partial unique indexes and final triggers. Startup fails loudly if existing data violates single-ref or undirected uniqueness.

Reference fields support:

```yaml
- { name: account, kind: ref, to: account, required: true, on_delete: restrict }
- { name: owner, kind: ref, to: member, on_delete: set_null }
```

Defaults are `restrict` for required references and `set_null` for optional references. `cascade` is accepted only on endpoint fields of a Type declaring a relation capability.

Applications that insert edges through raw SQL migrations must run:

```go
err := site.Engine().SyncRelationSchema()
```

after those site migrations. The old `FullFields` API is replaced by `FullNode`/`FullNodes`, whose `EditableNode.Values` explicitly contains scalar values plus ref IDs. Existing direct `Merge` calls must be removed; `PreviewMerge` is read-only until conflict resolution and audit-backed merge execution are implemented.

Composite fields (`array` / `object`) must not contain `ref` or `ref[]` sub-fields. Such a declaration used to load successfully and store raw node IDs inside `fields` JSON, so it had no edge, no cardinality, no delete policy, and was invisible to `CheckRelations`. Types that used this shape must be remodelled as a relation Node; the Schema loader now rejects them with a `kind ref cannot be nested in array/object` error.

Public `DELETE /api/nodes/{type}/{id}` now archives instead of permanently deleting. Authenticated administrators can call `POST /admin/nodes/{id}/archive` or `/restore` with `{"revision": n}`, while `DELETE /admin/nodes/{id}` remains the explicit permanent-delete operation and can return HTTP 409 for restricted references.
