# Changelog

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

### Migration

- Move old `slug`, `status`, and `sort` values into fields selected by each type's capabilities.
- Update callers to send `revision` with effective patches.
- Remove `legacy_node_columns` after the site migration has verified its data.
- No runtime fallback reads old columns and no source compatibility layer is provided.
- Register Auth Realms before mounting credential plugins and update clients to `/api/auth/{realm}/...`.
- Core migration `00010_auth_realms.sql` replaces plaintext Session tokens with hashes and invalidates existing frontend Sessions.

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
