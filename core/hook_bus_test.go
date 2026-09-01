package core

import (
	"errors"
	"strings"
	"testing"
)

func TestDefineHook(t *testing.T) {
	b := NewHookBus()
	if err := b.DefineHook("a", 42); err == nil {
		t.Fatal("non-func proto must be rejected")
	}
	if err := b.DefineHook("a", func(x int) int { return x }); err == nil {
		t.Fatal("proto without error return must be rejected")
	}
	if err := b.DefineHook("a", func(x int) error { return nil }); err != nil {
		t.Fatalf("valid proto: %v", err)
	}
	if err := b.DefineHook("a", func(x int) error { return nil }); err == nil {
		t.Fatal("re-define must be rejected")
	}
}

// 顺序: priority 升序; 同优先级按注册序稳定（AddHook 时已排好）。
func TestOrdering(t *testing.T) {
	b := NewHookBus()
	b.DefineHook("e", func() error { return nil })
	var order []string
	b.AddHook("e", func() error { order = append(order, "p10"); return nil }, 10)
	b.AddHook("e", func() error { order = append(order, "p1a"); return nil }, 1)
	b.AddHook("e", func() error { order = append(order, "p0"); return nil }, 0)
	b.AddHook("e", func() error { order = append(order, "p1b"); return nil }, 1)
	if err := b.Fire("e"); err != nil {
		t.Fatal(err)
	}
	want := "p0,p1a,p1b,p10"
	if got := strings.Join(order, ","); got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
}

// 首个 error 中止。
func TestAbort(t *testing.T) {
	b := NewHookBus()
	b.DefineHook("e", func() error { return nil })
	var called []string
	b.AddHook("e", func() error { called = append(called, "a"); return nil })
	b.AddHook("e", func() error { called = append(called, "b"); return errors.New("stop") })
	b.AddHook("e", func() error { called = append(called, "c"); return nil })
	err := b.Fire("e")
	if err == nil || !strings.Contains(err.Error(), "stop") {
		t.Fatalf("must abort with error, got %v", err)
	}
	if len(called) != 2 {
		t.Fatalf("abort after 2, got %v", called)
	}
}

// 签名校验在注册时（拼错名字/写错签名当场报错）。
func TestAddValidation(t *testing.T) {
	b := NewHookBus()
	b.DefineHook("e", func(x int) error { return nil })
	if err := b.AddHook("ghost", func() error { return nil }); err == nil {
		t.Fatal("unknown hook must fail")
	}
	if err := b.AddHook("e", func(s string) error { return nil }); err == nil {
		t.Fatal("wrong signature must fail")
	}
	if err := b.AddHook("e", func(x int) error { return nil }); err != nil {
		t.Fatalf("valid add: %v", err)
	}
}

// hook panic 被 recover — 返回包装 err, 不继续剩余 handler。
func TestPanicRecover(t *testing.T) {
	b := NewHookBus()
	b.DefineHook("e", func() error { return nil })
	var called []string
	b.AddHook("e", func() error { called = append(called, "a"); return nil })
	b.AddHook("e", func() error { panic("boom") })
	b.AddHook("e", func() error { called = append(called, "c"); return nil })
	err := b.Fire("e")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("must recover panic, got %v", err)
	}
	if len(called) != 1 || called[0] != "a" {
		t.Fatalf("must stop after panic, got %v", called)
	}
}
