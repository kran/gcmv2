package core

import (
	"strings"
	"testing"

	"github.com/kran/dba"
)

// TestSchemaIndexesFollowTypes 改索引配置（换字段、加、删）后，重启即生效:
// Open 每次都会先删光 gcm_schema_% 再按当前 Schema 建, 不存在旧索引残留。
func TestSchemaIndexesFollowTypes(t *testing.T) {
	db := testDB(t)
	v1 := `
types:
  article:
    constraints:
      indexes: [[stage, amount]]
    fields:
      - { name: stage, kind: select, options: [a, b] }
      - { name: amount, kind: number }
      - { name: views, kind: number }
`
	v2 := `
types:
  article:
    constraints:
      indexes: [[stage, views]]
    fields:
      - { name: stage, kind: select, options: [a, b] }
      - { name: amount, kind: number }
      - { name: views, kind: number }
`
	_, err := Open(db, newTypes(t, v1))
	if err != nil {
		t.Fatal(err)
	}
	if got := indexSQL(t, db, "gcm_schema_i_article_0"); !strings.Contains(got, "$.amount") {
		t.Fatalf("v1 索引 = %q, 应包含 $.amount", got)
	}

	_, err = Open(db, newTypes(t, v2))
	if err != nil {
		t.Fatal(err)
	}
	got := indexSQL(t, db, "gcm_schema_i_article_0")
	if !strings.Contains(got, "$.views") {
		t.Fatalf("换字段后索引 = %q, 应重建为 $.views", got)
	}
	if strings.Contains(got, "$.amount") {
		t.Fatalf("换字段后仍含旧字段: %q", got)
	}

	// 删掉索引配置 → 索引消失
	v3 := strings.Replace(v2, "    constraints:\n      indexes: [[stage, views]]\n", "", 1)
	_, err = Open(db, newTypes(t, v3))
	if err != nil {
		t.Fatal(err)
	}
	if got := indexSQL(t, db, "gcm_schema_i_article_0"); got != "" {
		t.Fatalf("索引配置删掉后仍有索引: %q", got)
	}
}

// indexSQL 读索引定义（不存在返回 "")。
func indexSQL(t *testing.T, db *dba.SQL, name string) string {
	t.Helper()
	found, err := db.Add(`SELECT COALESCE(sql, '') FROM sqlite_master
		WHERE type = 'index' AND name = #{1}`, name).FetchOne[string]()
	if err != nil {
		t.Fatal(err)
	}
	if found == nil {
		return ""
	}
	return *found
}
