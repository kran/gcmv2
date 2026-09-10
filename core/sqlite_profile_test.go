package core

import (
	"path/filepath"
	"testing"

	"github.com/kran/dba"
)

// 连接档位不满足内核不变量时拒绝启动（并发下会静默退化成 SQLITE_BUSY / 外键失效）。
func TestSQLiteProfileIsRequired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.db")

	plain, err := dba.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Pool().Close()
	if err := verifySQLiteProfile(plain); err == nil {
		t.Fatal("sqlite defaults accepted: journal_mode=delete / busy_timeout=0")
	}

	foreignOnly, err := dba.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer foreignOnly.Pool().Close()
	if err := verifySQLiteProfile(foreignOnly); err == nil {
		t.Fatal("non-WAL profile accepted")
	}

	configured, err := dba.Open("sqlite",
		path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer configured.Pool().Close()
	if err := verifySQLiteProfile(configured); err != nil {
		t.Fatalf("configured profile rejected: %v", err)
	}
}
