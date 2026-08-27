-- +goose Up
-- settings 运营元数据: 分组 + 编辑形态（cmx piece 语义; value 自由 JSON 透传）。
ALTER TABLE settings ADD COLUMN group_name TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN type TEXT NOT NULL DEFAULT 'string';

-- +goose Down
ALTER TABLE settings DROP COLUMN group_name;
ALTER TABLE settings DROP COLUMN type;
