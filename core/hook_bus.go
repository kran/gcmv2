// Package core 通用 hook 总线 (WP 字符串模型 × Go 类型安全的交点):
//
//	DefineHook 声明名字与签名 (proto 必须 func(...) error)
//	AddHook    注册 handler, 运行时校验签名可赋值 (注册即报错, 不留到触发)
//	Fire       按优先级+注册序调用, panic 中断剩余; 业务 err 继续（收集语义）, 返回第一个 err; 调用前校验实参防反射 panic
//
// 变换 (filter) 没有特殊机制 — handler 传指针就地修改, C 语言的地址语义。
// 每站点一个 HookBus 实例 (挂在 SiteCtx), 隔离是结构性的。
// 事件名不在此定义: 业务事件常量在各语义包 (如 node.HookNodeSave),
// 通用组件不知道 CMS 语义。
package core

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
)

var errorType = reflect.TypeFor[error]()

type entry struct {
	priority int
	seq      int
	fn       any
}

type Hook struct {
	name     string
	proto    reflect.Type
	handlers []entry
}

// HookBus 站点级 hook 总线。
type HookBus struct {
	mu    sync.RWMutex
	hooks map[string]*Hook
	seq   int
}

func NewHookBus() *HookBus {
	return &HookBus{hooks: map[string]*Hook{}}
}

// HasHook 事件是否注册了 handler（权限类 hook — 无 handler = 默认拒绝）。
func (b *HookBus) HasHook(name string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	h, ok := b.hooks[name]
	return ok && len(h.handlers) > 0
}

// Define 批量声明 hook (语义同 DefineHook): 一个 map, key=事件名, value=proto 函数。
// 单条失败即报错停止。
func (b *HookBus) Define(specs map[string]any) error {
	for name, proto := range specs {
		if err := b.DefineHook(name, proto); err != nil {
			return err
		}
	}
	return nil
}

// DefineHook 声明 hook 名与签名。proto 必须是一个函数且恰好返回一个
// error (参数不限 — 变换靠指针就地修改)。重复定义报错。
func (b *HookBus) DefineHook(name string, proto any) error {
	t := reflect.TypeOf(proto)
	if t == nil || t.Kind() != reflect.Func {
		return fmt.Errorf("hook: %q proto must be a function", name)
	}
	if t.NumOut() != 1 || !t.Out(0).AssignableTo(errorType) {
		return fmt.Errorf("hook: %q proto must return exactly error, got %v", name, t)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.hooks[name]; ok {
		return fmt.Errorf("hook: %q already defined", name)
	}
	b.hooks[name] = &Hook{name: name, proto: t}
	return nil
}

// AddHook 注册 handler。fn 的签名必须可赋值给 DefineHook 声明的 proto
// (注册即校验, 拼错名字/写错签名当场报错)。priority 缺省 0, 同优先级按
// 注册序稳定执行。
func (b *HookBus) AddHook(name string, fn any, priority ...int) error {
	b.mu.RLock()
	h, ok := b.hooks[name]
	b.mu.RUnlock()
	if !ok {
		return fmt.Errorf("hook: %q not defined (DefineHook first)", name)
	}
	ft := reflect.TypeOf(fn)
	if ft == nil || ft.Kind() != reflect.Func || !ft.AssignableTo(h.proto) {
		return fmt.Errorf("hook: %q expects %v, got %v", name, h.proto, ft)
	}
	p := 0
	if len(priority) > 0 {
		p = priority[0]
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	// 注册时有序插入（priority 升序; 同优先级保持注册序稳定）—
	// Fire 直接遍历, 不在触发路径排序（高频事件零排序开销）。
	e := entry{priority: p, seq: b.seq, fn: fn}
	i := sort.Search(len(h.handlers), func(i int) bool {
		return h.handlers[i].priority > p
	})
	h.handlers = append(h.handlers, entry{})
	copy(h.handlers[i+1:], h.handlers[i:])
	h.handlers[i] = e
	b.seq++
	return nil
}

// Fire 触发 hook: 实参与 proto 校验 (防反射 panic) → 按优先级+注册序调用
// → 首个 error 中止并返回。
func (b *HookBus) Fire(name string, args ...any) error {
	b.mu.RLock()
	h, ok := b.hooks[name]
	b.mu.RUnlock()
	if !ok {
		return fmt.Errorf("hook: %q not defined", name)
	}
	callArgs, err := checkHookArgs(h.proto, args)
	if err != nil {
		return fmt.Errorf("hook: %q: %w", name, err)
	}

	// 顺序在 AddHook 已排好（priority + 注册序稳定）, 触发路径零排序
	b.mu.RLock()
	handlers := append([]entry(nil), h.handlers...)
	b.mu.RUnlock()
	// panic 中断（崩溃级不可预期）; 业务 err 继续执行剩余 hook（收集语义 —
	// 一个失败不阻断其他）, Fire 返回第一个业务 err（fail-closed 调用方照拒）。
	var firstErr error
	for _, hd := range handlers {
		err, panicked := func() (err error, panicked bool) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("hook %q panicked: %v", name, r)
					panicked = true
				}
			}()
			rets := reflect.ValueOf(hd.fn).Call(callArgs)
			if e, ok := rets[0].Interface().(error); ok && e != nil {
				return e, false
			}
			return nil, false
		}()
		if panicked {
			return err
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// checkHookArgs 实参与 proto 参数逐项可赋值校验; nil 实参按对应参数类型的
// 零值处理 (指针/接口/map/切片/chan/func 可为 nil)。
func checkHookArgs(proto reflect.Type, args []any) ([]reflect.Value, error) {
	if len(args) != proto.NumIn() {
		return nil, fmt.Errorf("expects %d args, got %d", proto.NumIn(), len(args))
	}
	out := make([]reflect.Value, len(args))
	for i, arg := range args {
		pt := proto.In(i)
		v := reflect.ValueOf(arg)
		if !v.IsValid() { // nil
			switch pt.Kind() {
			case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
				v = reflect.Zero(pt)
			default:
				return nil, fmt.Errorf("arg %d: nil not assignable to %v", i, pt)
			}
		} else if !v.Type().AssignableTo(pt) {
			return nil, fmt.Errorf("arg %d: %v not assignable to %v", i, v.Type(), pt)
		}
		out[i] = v
	}
	return out, nil
}
