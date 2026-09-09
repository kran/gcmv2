# Changelog

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
