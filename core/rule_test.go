package core

import "testing"

func TestEvalRule(t *testing.T) {
	ctx := map[string]any{
		"auth": map[string]any{"id": float64(42), "role": "editor"},
	}
	cases := []struct {
		expr string
		want bool
	}{
		{`true`, true},
		{`false`, false},
		{`nil`, false},
		{`auth != nil`, true},
		{`(= auth.role "editor")`, true},
		{`(= auth.role "member")`, false},
		{`(and auth (= auth.role "editor"))`, true},
		{`(or (= auth.role "admin") (= auth.role "editor"))`, true},
		{`(or (= auth.role "admin") (= auth.role "member"))`, false},
		{`(not (= auth.role "admin"))`, true},
		{`(in auth.role ["member" "editor"])`, true},
		{`(in auth.role ["member" "admin"])`, false},
		{`(= auth.id 42)`, true},
		{`(= auth.id 43)`, false},
		{`auth.missing = nil`, true},
	}
	for _, c := range cases {
		got, err := EvalRule(c.expr, ctx)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got != c.want {
			t.Fatalf("%s = %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestEvalRuleAnonymous(t *testing.T) {
	// 未登录: auth = nil
	ctx := map[string]any{"auth": nil}
	got, err := EvalRule(`auth != nil`, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("anonymous should fail auth != nil")
	}
	got, _ = EvalRule(`(= auth.role "editor")`, ctx)
	if got {
		t.Fatal("nil auth path should be nil")
	}
}

func TestEvalRuleErrors(t *testing.T) {
	if _, err := EvalRule(`(unknown-op 1)`, nil); err == nil {
		t.Fatal("unknown op should error")
	}
	if _, err := EvalRule(`"unterminated`, nil); err == nil {
		t.Fatal("unterminated string should error")
	}
	if _, err := EvalRule(`(`, nil); err == nil {
		t.Fatal("unterminated list should error")
	}
}
