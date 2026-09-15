package types

// asAnySlice 把"多值列表"的两种形态统一成 []any：读投影给的是 []int64（读出来的值要
// 能原样写回），HTTP JSON 解码给的是 []any。其它类型一律 fail-loud。
func asAnySlice(v any) ([]any, bool) {
	switch list := v.(type) {
	case []any:
		return list, true
	case []int64:
		out := make([]any, len(list))
		for i := range list {
			out[i] = list[i]
		}
		return out, true
	}
	return nil, false
}
