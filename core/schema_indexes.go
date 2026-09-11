package core

import (
	"fmt"
	"strings"

	"github.com/kran/dba"
)

// syncSchemaIndexes 让声明式标量索引与当前 TypeDef 一致。索引全部带统一前缀，
// 每次启动先删除再创建，Schema 是唯一来源，不留下已经移除的约束。
func (s *Service) syncSchemaIndexes() error {
	return s.db.Transaction(func(tx *dba.SQL) error {
		names, err := tx.Add(
			`SELECT name FROM sqlite_master WHERE type = 'index' AND name LIKE 'gcm_schema_%'`).FetchList[string]()
		if err != nil {
			return fmt.Errorf("core: schema indexes: list: %w", err)
		}
		for _, name := range names {
			_, err := tx.Add(`DROP INDEX ` + quoteIdentifier(name)).Exec()
			if err != nil {
				return fmt.Errorf("core: schema indexes: drop %s: %w", name, err)
			}
		}

		for _, typeName := range s.types.Names() {
			td, _ := s.types.Type(typeName)
			for i, fields := range td.Constraints.Unique {
				name := fmt.Sprintf("gcm_schema_u_%s_%d", typeName, i)
				err := createSchemaIndex(tx, name, typeName, fields, true)
				if err != nil {
					return err
				}
			}
			for i, fields := range td.Constraints.Indexes {
				name := fmt.Sprintf("gcm_schema_i_%s_%d", typeName, i)
				err := createSchemaIndex(tx, name, typeName, fields, false)
				if err != nil {
					return err
				}
			}
		}

		return s.createAddressIndex(tx)
	})
}

func createSchemaIndex(tx *dba.SQL, name, typeName string, fields []string, unique bool) error {
	expressions := make([]string, len(fields))
	for i, field := range fields {
		expressions[i] = `json_extract(fields, '$.` + field + `')`
	}
	kind := "INDEX"
	if unique {
		kind = "UNIQUE INDEX"
	}
	statement := `CREATE ` + kind + ` ` + quoteIdentifier(name) + ` ON nodes (` +
		strings.Join(expressions, ", ") + `) WHERE type = ` + quoteLiteral(typeName) + ` AND archived_at IS NULL`
	_, err := tx.Add(statement).Exec()
	if err != nil {
		return fmt.Errorf("core: schema index %s: %w", name, err)
	}
	return nil
}

func (s *Service) createAddressIndex(tx *dba.SQL) error {
	branches := make([]string, 0)
	types := make([]string, 0)
	for _, typeName := range s.types.Names() {
		capability, ok := s.types.Addressable(typeName)
		if !ok {
			continue
		}
		// 单类型地址索引: 全局唯一索引是 CASE 表达式（跨类型唯一用）, 查询很难命中;
		// 单类型 (type, address) 索引才是点查路径（GetNodeByAddress 按类型展开成 OR）。
		name := "gcm_schema_address_" + typeName
		err := createSchemaIndex(tx, name, typeName, []string{capability.Field}, false)
		if err != nil {
			return err
		}
		branches = append(branches, `WHEN `+quoteLiteral(typeName)+` THEN NULLIF(json_extract(fields, '$.`+capability.Field+`'), '')`)
		types = append(types, quoteLiteral(typeName))
	}
	if len(branches) == 0 {
		return nil
	}
	expression := `CASE type ` + strings.Join(branches, " ") + ` ELSE NULL END`
	statement := `CREATE UNIQUE INDEX gcm_schema_address_global ON nodes (` + expression +
		`) WHERE archived_at IS NULL AND type IN (` + strings.Join(types, ", ") + `)`
	_, err := tx.Add(statement).Exec()
	if err != nil {
		return fmt.Errorf("core: global address index: %w", err)
	}
	return nil
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quoteLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}
