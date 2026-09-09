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
LoadTree(type, field)      -> LoadTree(type)
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
