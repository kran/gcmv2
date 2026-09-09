package core

import (
	"fmt"
	"testing"
	"time"
)

// BenchmarkJSONAndEAVFilters 对比 gcm 的典型 CRM 列表条件。它不是跨数据库
// 结论，只用于防止在没有本项目数据证据时把主存储改成 EAV。
func BenchmarkJSONAndEAVFilters(b *testing.B) {
	ts := newTypes(b, `
types:
  opportunity:
    constraints:
      indexes: [[stage, amount]]
    fields:
      - { name: stage, kind: select, options: [new, qualified, won] }
      - { name: amount, kind: number }
`)
	s := New(testDB(b), ts)
	sqlDB := s.db.Pool().DB

	_, err := sqlDB.Exec(`CREATE TABLE eav_bench (
		node_id INTEGER NOT NULL,
		field TEXT NOT NULL,
		text_value TEXT,
		number_value INTEGER
	)`)
	if err != nil {
		b.Fatal(err)
	}
	_, err = sqlDB.Exec(`CREATE INDEX eav_bench_text ON eav_bench(field, text_value, node_id)`)
	if err != nil {
		b.Fatal(err)
	}
	_, err = sqlDB.Exec(`CREATE INDEX eav_bench_number ON eav_bench(field, number_value, node_id)`)
	if err != nil {
		b.Fatal(err)
	}

	tx, err := sqlDB.Begin()
	if err != nil {
		b.Fatal(err)
	}
	nodeStmt, err := tx.Prepare(`INSERT INTO nodes(type, display, fields, revision, created_at, updated_at) VALUES ('opportunity', ?, ?, 1, ?, ?)`)
	if err != nil {
		b.Fatal(err)
	}
	eavStmt, err := tx.Prepare(`INSERT INTO eav_bench(node_id, field, text_value, number_value) VALUES (?, ?, ?, ?)`)
	if err != nil {
		b.Fatal(err)
	}
	now := time.Now()
	for i := 1; i <= 100_000; i++ {
		stage := []string{"new", "qualified", "won"}[i%3]
		amount := i * 100
		fields := fmt.Sprintf(`{"stage":%q,"amount":%d}`, stage, amount)
		result, err := nodeStmt.Exec(fmt.Sprintf("opportunity-%d", i), fields, now, now)
		if err != nil {
			b.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			b.Fatal(err)
		}
		if _, err = eavStmt.Exec(id, "stage", stage, nil); err != nil {
			b.Fatal(err)
		}
		if _, err = eavStmt.Exec(id, "amount", nil, amount); err != nil {
			b.Fatal(err)
		}
	}
	if err := nodeStmt.Close(); err != nil {
		b.Fatal(err)
	}
	if err := eavStmt.Close(); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}

	b.Run("json-composite-index", func(b *testing.B) {
		for b.Loop() {
			row := sqlDB.QueryRow(`SELECT COUNT(*) FROM nodes
				WHERE type = 'opportunity' AND archived_at IS NULL
				AND json_extract(fields, '$.stage') = 'qualified'
				AND json_extract(fields, '$.amount') >= 5000000`)
			var count int
			if err := row.Scan(&count); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("eav-self-join", func(b *testing.B) {
		for b.Loop() {
			row := sqlDB.QueryRow(`SELECT COUNT(*) FROM nodes n
				JOIN eav_bench stage ON stage.node_id = n.id AND stage.field = 'stage'
				JOIN eav_bench amount ON amount.node_id = n.id AND amount.field = 'amount'
				WHERE n.type = 'opportunity' AND n.archived_at IS NULL
				AND stage.text_value = 'qualified' AND amount.number_value >= 5000000`)
			var count int
			if err := row.Scan(&count); err != nil {
				b.Fatal(err)
			}
		}
	})
}
