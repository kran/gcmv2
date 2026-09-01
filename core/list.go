// 泛型集合 — hook 传递用（替代裸 slice 指针 — 支持 Append/Prepend）。
package core

// List 泛型集合（hook 参数 — 插件往集合追加/前插; 替代 *[]T 不友好）。
type List[T any] struct {
	items []T
}

// NewList 空集合。
func NewList[T any]() *List[T] { return &List[T]{} }

// Append 追加到末尾（钩子回填顺序 — 后注册的靠后）。
func (l *List[T]) Append(vals ...T) { l.items = append(l.items, vals...) }

// Prepend 前插到开头（高优先 — 如模板候选最优先）。
func (l *List[T]) Prepend(vals ...T) { l.items = append(append([]T{}, vals...), l.items...) }

// Items 底层切片（只读浏览 — 结果取用）。
func (l *List[T]) Items() []T { return l.items }

// Len 长度。
func (l *List[T]) Len() int { return len(l.items) }
