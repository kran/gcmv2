# Changelog

## Unreleased — v0.9.1

### Breaking changes

- `web.ReadRule` takes a fourth parameter, `*core.List[string]`: a read rule now declares both the row range and the fields the actor must not see. Existing rules add one ignored parameter.

### Added

- Field-level visibility. A read rule appends field names to `hide` (`hide.Append("phone")`) and the field disappears from the output — API responses, template helpers, trees and expanded nodes alike. An empty `hide` means every field is visible, so unregistered rules and sites that never touch it behave exactly as before; a name that is not a declared field is an error instead of a silent leak. `web.MaskNode` / `MaskNodes` / `MaskTree` apply it where a site builds its own JSON; the framework's own output paths already do.
- `CmsCtx.ReadRule` resolves a rule once per request per `(action, type)` and caches the result on the request context — the same lifetime as the actor it depends on, invalidated by `SetActor`, never shared across requests.

### Behaviour changes

- `ListQuery.CountLimit` and `SearchQuery.CountLimit` bound what `total` costs. `0` uses the default (10000 for lists, 1000 for search) and saturates `total` at that limit, `core.CountExact` keeps the exact count, and a positive value sets an explicit cap. Callers that page treat `total` as "at least this many"; callers that need an exact total on a large table must ask for it.
- Search ranks matches with bm25 over the whole match set, so a term matching most of a large table still costs proportionally to the match count. The count limit does not bound that.

### Fixed

- `GetNodeByAddress` resolves addresses through a per-type index instead of scanning every node of every addressable type. The JSON path was bound as a parameter, which no index expression can match, so the global address index was unusable as a lookup. At 100k nodes: 1.7s → 129µs.
- Relation membership (`in` on a ref/ref[] path) compiles to an uncorrelated subquery instead of a correlated `EXISTS`, so the edge set is built once instead of once per candidate row. At 100k nodes: 1.25s → 35ms.

### Changed

- The full-text index is contentless (`content=''`, `contentless_delete=1`): it no longer stores a second copy of the bigram-expanded text, which for Chinese is several times the original. At 100k nodes: 15.5KB → 6.8KB per node.
- `core.Open` rebuilds the search index whenever migrations were applied, so an index-affecting migration can never leave search stale.

### Migration

- Core migration `00012_contentless_fts.sql` recreates `nodes_fts` without its content table; `core.Open` rebuilds it once (2.7s at 10k nodes, 29s at 100k, 13m37s at 1M).
- Changing `constraints.indexes` / `constraints.unique` / `addressable` only needs a restart: `core.Open` drops and recreates every `gcm_schema_%` index from the current Schema (a test pins this). The cost is the startup cost: 2.2s for four indexes at 100k nodes.

See `docs/scaling.md` for measured throughput, latency and storage from 1k to 1M nodes.
- The migration frees pages inside the existing file; reclaiming them needs `VACUUM` (`VACUUM INTO` — what the backup plugin uses — produces a compact copy).

## Unreleased — v0.9.0

### Breaking changes

- `core.Node` no longer contains `Slug`, `Status`, or `Sort`; these are explicit Schema fields selected by capabilities.
- `core.Node` adds `Revision` and `ArchivedAt` system metadata.
- `core.NodePatch` now contains `Revision`, `Display`, and `Fields`; effective updates require the current revision.
- `GetNodeBySlug` is replaced by `GetNodeByAddress`.
- `LoadTree(typeName, field)` is replaced by `LoadTree(typeName)` and reads the declared tree capability.
- Flat TypeDef properties `search`, `view`, `icon`, and `auth` are removed. Use `capabilities` and `admin`.
- Public node routes only expose types with a publication capability.
- Node create/update HTTP bodies reject unknown top-level properties.
- `ListQuery.Filter string` and variadic parameter maps are removed; use `Type` plus typed `query.Expr`.
- `Query` and `QueryPage` now require `context.Context`.
- `core.SortField` is replaced by `query.SortField` with a typed `query.Path`.
- `ListQuery.Expand string` is replaced by typed `[]query.ExpandPath`.
- `ExpandPath/ExpandPathMany` are replaced by context-aware `Expand/ExpandMany`; incoming paths require an explicit source Type.
- Raw-SQL Lisp extension registration is removed.
- Authentication request DTOs no longer accept `type`; credential routes are now `/api/auth/{realm}/...`.
- `password.Mount` now requires `password.Options{Realm: ...}`.
- `CmsCtx.User` and `RequireRole` are removed; use `Actor` and `Principal`.
- Session APIs now require a Realm and resolve a `core.Session` instead of a bare Node ID.
- `core.VerifyPassword` is removed; credential verification belongs to the password plugin.
- `ListQuery` requires an explicit `QueryScope`; trusted administrative callers must use `core.BypassPolicy()`.
- `Search` now accepts context-aware `SearchQuery` with Type-specific targets and scopes.
- `FullFields` is replaced by context-aware `FullNode`, `FullNodes`, `RefID`, `RefIDs`, and `HasRef` APIs.
- The unsafe mutation-only `Merge` operation is removed; use `PreviewMerge` until audited merge execution is available.
- Public Node DELETE now archives; administrator DELETE remains the explicit permanent-delete path.
- `InEdges` now filters the requested field and returns logical two-way results for symmetric/equivalence relations.
- Composite fields reject any nested ref/ref[] Kind: previously such a declaration loaded successfully and stored raw IDs in `fields` JSON, bypassing edges, cardinality and delete policies.
- `Kind.Validate` now receives the `FieldDef`, so per-field value constraints such as select options are enforced by the Kind instead of a container switch.
- `LoadTree(typeName)` becomes `LoadTree(ctx, typeName, scope)`; Core no longer requires publication or filters published Nodes itself.
- `TypeDef.TemplateCandidates` is removed; template candidates belong to the web layer.
- The generic `/api/nodes/mine` endpoint is removed. Owner-scoped content listing is site business API (association: `GET /api/me/content`).
- Every database or external I/O entry point now takes a `context.Context`: `CreateNode`, `PatchNode`, `DeleteNode`, `AddEdge`, `RemoveEdge`, `GetNodeById`, `GetNodeByAddress`, `Traverse`, `Subtree`, `Ancestors`, `EquivalenceClass`, `OutEdges`, `InEdges`, `RegisterAuth`, `FindAuth`, `AddAuthMethod`, `RemoveAuthMethod`, `CreateSession`, `ValidSession`, `DeleteSession`, `DeleteNodeSessions`, `GetSetting`, `SetSetting`, `ListSettings`, `DeleteSetting`, `RebuildSearch`, `Migrator.Up/UpDir` and `Render.Render`.
- `SearchIndex.Rebuild` takes a `context.Context`.
- `web.New`/`core.New` keep the panic convenience path; `web.Open`/`core.Open` return the initialization error instead.
- Error responses now carry a stable `code` next to `error`; clients must branch on `code` instead of matching message text.
- Unknown Node types now answer 404 (`not_found`) instead of 400, and rejected uploads answer 413/422 (`upload_invalid`) instead of 400.
- `core.ErrInvalidFields` marks Schema validation failures so the Web edge can return 422 `invalid_value`.
- `InEdges` now filters the requested field and, like `OutEdges`, returns logical two-way results for symmetric/equivalence relations.
- Authorization is now a set of per-Type events instead of a separate `PolicyRegistry`: `web.read.list|view|search|export.<type>` narrow the row scope, `web.write.create|update|delete.<type>` decide identity, client-writable fields, and server-owned values. Register them with `Site.ReadRule` / `Site.WriteRule`; `Site.ReadScope` and `Site.Exposes` replace `Policy.Scope` and `Policy.Exposes`.
- `PolicyRegistry`, `PolicyAction`, `PolicyRequest`, `PolicyRule`, `PolicyDefault`, `Policy.Write`, `Policy.RegisterWrite`, `Policy.Writable`, `Policy.Exposes`, `Policy.Scope` and `HookBeforeCreate` / `HookBeforeUpdate` / `HookBeforeDelete` are removed.
- `HookBus` regains `Has` (does the event have handlers) and adds `Defined` (is the event declared).
- Public node writes no longer fall back to "any registered Hook means every Type is writable". A Type without a `web.write.<action>.<type>` handler rejects with 401 for anonymous and 403 for authenticated callers; payload fields the rule did not allow answer 422 `invalid_value` with per-field details.

### Added

- Schema field defaults and immutable fields.
- Type-level scalar unique constraints and indexes backed by SQLite expression/partial indexes.
- Explicit searchable, addressable, publication, authentication, and tree capabilities.
- Admin view metadata separated from Schema and runtime capabilities.
- Optimistic locking through `Node.Revision`.
- `slug` field kind and global address uniqueness.
- Core migration `00009_node_schema_capabilities.sql` preserves old fixed-column values in `legacy_node_columns` for one-time site migration.
- Closed Filter AST and Go query builder in the `query` package.
- Lisp-to-AST parser; Lisp no longer compiles directly to SQL.
- Strict JSON QuerySpec decoder and authenticated `POST /admin/query/{type}` endpoint.
- Schema-aware validation for fields, Kind-declared query operations, values, relations, sorts, complexity, and relation depth.
- Stable ID tie-breaking for every explicit sort.
- Per-hop Schema validation for Expand, including mixed root Types, relation cardinality, and target Type integrity.
- Server-registered Auth Realms mapping stable public names to authentication-enabled Node types.
- Unified Anonymous, Node, Admin, and API Key Actor model with lazy Node Principal loading.
- Realm-isolated password register/login/bind endpoints and cross-Realm bind rejection.
- SHA-256 Session token storage and bulk Node session revocation.
- Site-defined read actions, so a site can scope a read surface no system action describes.
- Template helpers (`list`, `filterList`, `search`, `get`) resolve the same read rules as the JSON API instead of a private publication rule, and a non-publication Type no longer panics the helper.
- Render failures now answer 500 and log, instead of a 200 page with an HTML comment that hid broken templates from monitoring.
- Every request body is capped (8MB hard cap in `CmsCtxMaker`, 1MB for JSON decoding in `BindStrictJSON`, which answers 413 `invalid_request`), so a single request cannot allocate unbounded memory.
- SQLite runs with `journal_mode=WAL`, `busy_timeout=5000`, and `foreign_keys=1`; `core.Open` verifies those and refuses to start otherwise.
- Per-Type authorization events for every read and write action, so one `(action, Type)` can resolve per request: a member and an editor can be allowed different fields, and several handlers compose (reads narrow by AND, write grants union).
- A write rule that allows no field at all denies the request (403), and an unregistered site read action or a read rule that produced no scope is an error instead of a silent empty result.
- Mandatory AST-level Policy merging for list, count, view, search, and sitemap export paths.
- Type-specific Search targets, allowing one cross-Type FTS query without weakening per-Type Policy.
- `on_delete: restrict|set_null|cascade` with safe defaults and transactional permanent deletion.
- Database-backed single-ref and symmetric-single cardinality constraints.
- Canonical symmetric/equivalence storage, cycle rejection, and stricter traversal validation.
- `relation` capability for attributed relation Nodes.
- Composite field definitions are now fully validated at load time: nested Kinds must exist, their field constraints run, duplicate object sub-fields are rejected, and `array`/`object` no longer silently ignore `item`, `fields`, `to` or algebra declarations.
- Context-aware `RefID`, `RefIDs`, `HasRef`, `FullNode`, and `FullNodes` APIs.
- Read-only relation integrity report, protected admin inspection, and non-mutating merge previews.
- Archive/restore operations that preserve Edges and revoke sessions for archived authentication Nodes.
- `Site.Close() error` releases the database pool idempotently, and `GET /healthz` / `GET /readyz` provide liveness and readiness probes.
- Context cancellation is honored on write, graph, session, settings and search-rebuild paths, covered by a cancellation test.
- `web.Error` with a stable `Code`, `Details` for field-level errors, and the constructor helpers `BadRequest`/`InvalidValue`/`InvalidFields`/`Unauthorized`/`Forbidden`/`NotFound`/`Conflict`/`Unavailable`/`Internal`.
- `CmsCtx.Fail` (API exit) and `CmsCtx.Reject` (hook rejection) map `*web.Error` and core sentinel errors onto the HTTP contract; unknown errors are logged and returned as a generic 500.
- `CmsCtx.Error(status, message)` keeps the cho call shape but always emits a derived `code`.

### Migration

- Move old `slug`, `status`, and `sort` values into fields selected by each type's capabilities.
- Update callers to send `revision` with effective patches.
- Remove `legacy_node_columns` after the site migration has verified its data.
- No runtime fallback reads old columns and no source compatibility layer is provided.
- Register Auth Realms before mounting credential plugins and update clients to `/api/auth/{realm}/...`.
- Core migration `00010_auth_realms.sql` replaces plaintext Session tokens with hashes and invalidates existing frontend Sessions.
- Rebuild the FTS index after upgrading: drafts and other non-public searchable Nodes now remain indexed, while visibility is enforced by Policy at query time.
- Core migration `00011_edge_integrity.sql` adds Edge cardinality/algebra metadata; startup Schema synchronization validates existing data and creates the final database constraints.
- Run `SyncRelationSchema` after any raw-SQL site migration that inserts Edge rows.

## v0.8.4

Security and correctness hardening. This release intentionally changes unstable v0 APIs instead of retaining compatibility adapters.

### Breaking changes

- `core.ListQuery.Sort` is now `[]core.SortField`; raw SQL sort strings are no longer accepted.
- Public `GET /api/nodes/{type}` no longer accepts `filter` or `expand` and only returns published nodes.
- Unknown node types now return HTTP 400.
- Node view/update/delete require the URL type to match the stored node type.
- Create and Patch reject unknown fields instead of silently dropping them.
- `types.yaml` rejects unknown properties; removed properties such as `title` and `inverse` must be deleted.
- `/api/upload` requires a frontend session and `/admin/upload` requires an administrator session.

### Fixed

- Validate Patch scalar/ref values and prevent deletion or emptying of required fields.
- Preserve requested ID order in `ExpandPathMany` and map expanded values by node ID.
- Bound Lisp filter and expand expression complexity.
- Validate upload extension against detected content type, sanitize filenames, and prevent overwrites.
- Force PDF and ZIP uploads to download with `nosniff` responses.
- Upgrade `golang.org/x/image` to v0.43.0 to remove known image decoder vulnerabilities.
- Validate node type and auth capability in `AddAuthMethod`.
- Bind additional auth methods to the current session node type rather than a client-provided type.
- Add server-side administrator session expiry and invalidate sessions on logout/password changes.
- Add configurable Secure cookies for frontend and administrator sessions.
- Refresh the admin tree page when switching between route parameter types.
- Match admin page titles using route parameters.

### Migration

- Core migration `00008_admin_session_expiry.sql` adds `accounts.session_expires_at`.
- Existing administrator sessions become invalid and require a new login after migration.
