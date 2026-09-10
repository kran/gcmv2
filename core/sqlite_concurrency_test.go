package core

import (
	"fmt"
	"sync"
	"testing"

	gquery "github.com/kran/gcmv2/query"
)

// WAL + busy_timeout 的意义就是并发读写不再撞锁失败：这个测试把
// “8 个并发写 + 4 个并发读”跑一遍，任何 SQLITE_BUSY / 锁错误都会让测试失败。
// （没有 WAL 或 busy_timeout=0 时，这个测试会零星失败 —— 见 TestSQLiteProfileIsRequired。）
func TestSQLiteConcurrentReadWrite(t *testing.T) {
	svc := newTestService(t)
	const (
		writers        = 8
		perWriter      = 6
		readers        = 4
		readsPerReader = 20
	)
	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter+readers*readsPerReader)

	for writer := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perWriter {
				node := &Node{
					Type: "article", Display: fmt.Sprintf("w%d-%d", writer, i),
					Fields: Fields{
						"title":             fmt.Sprintf("w%d-%d", writer, i),
						"body":              "并发写入",
						"publication_state": "published",
					},
				}
				if _, err := svc.CreateNode(t.Context(), node); err != nil {
					errs <- fmt.Errorf("writer %d: %w", writer, err)
					return
				}
			}
		}()
	}
	for reader := range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range readsPerReader {
				_, _, err := svc.QueryPage(t.Context(), ListQuery{
					Type: "article", Where: gquery.True(),
					Scope: BypassPolicy(), Page: gquery.Page{Size: 10},
				})
				if err != nil {
					errs <- fmt.Errorf("reader %d: %w", reader, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent access failed: %v", err)
	}

	_, total, err := svc.QueryPage(t.Context(), ListQuery{
		Type: "article", Where: gquery.True(), Scope: BypassPolicy(),
		Page: gquery.Page{Size: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(writers * perWriter); total != want {
		t.Fatalf("total = %d, want %d", total, want)
	}
}
