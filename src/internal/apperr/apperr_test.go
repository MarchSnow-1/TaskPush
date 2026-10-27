package apperr

import (
	"errors"
	"fmt"
	"testing"
)

// TestProblem_Error 覆盖 Error() 的调试文本: 无 Cause 只输出 ID, 有 Cause 追加技术细节
//
// TestProblem_Error covers the debugging text of Error(): without a Cause it prints the bare
// ID, and with one it appends the technical detail
func TestProblem_Error(t *testing.T) {
	cases := []struct {
		name string
		p    *Problem
		want string
	}{
		{name: "id only", p: &Problem{ID: "example.error.sample"}, want: "example.error.sample"},
		{
			name: "id with cause",
			p:    &Problem{ID: "example.error.sample", Cause: errors.New("example detail")},
			want: "example.error.sample: example detail",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.Error(); got != tc.want {
				t.Fatalf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestProblem_ErrorNil 覆盖 nil 接收者: Error() 返回空串而不是 panic
//
// TestProblem_ErrorNil covers the nil receiver, where Error() returns an empty string rather
// than panicking
func TestProblem_ErrorNil(t *testing.T) {
	var p *Problem
	if got := p.Error(); got != "" {
		t.Fatalf("nil Problem Error() = %q, want empty", got)
	}
	if err := p.Unwrap(); err != nil {
		t.Fatalf("nil Problem Unwrap() = %v, want nil", err)
	}
}

// TestProblem_Unwrap 覆盖 Unwrap 对 Cause 的透出
//
// TestProblem_Unwrap covers how Unwrap surfaces the Cause
func TestProblem_Unwrap(t *testing.T) {
	cause := errors.New("example detail")
	p := &Problem{ID: "example.error.sample", Cause: cause}
	if got := p.Unwrap(); !errors.Is(got, cause) {
		t.Fatalf("Unwrap() = %v, want the configured cause", got)
	}
	if got := (&Problem{ID: "example.error.sample"}).Unwrap(); got != nil {
		t.Fatalf("Unwrap() without a cause = %v, want nil", got)
	}
}

// TestProblem_ErrorsIsAs 覆盖 errors.Is / errors.As 穿过 fmt.Errorf 与 Permanent 包装
//
// TestProblem_ErrorsIsAs covers errors.Is and errors.As reaching through both a fmt.Errorf
// wrap and a Permanent wrap
func TestProblem_ErrorsIsAs(t *testing.T) {
	sentinel := errors.New("example sentinel")
	p := &Problem{ID: "example.error.sample", Data: map[string]any{"Field": "example"}, Cause: sentinel}
	wrapped := fmt.Errorf("example context: %w", Permanent(p))

	if !errors.Is(wrapped, sentinel) {
		t.Fatal("errors.Is did not reach the underlying sentinel")
	}
	if !IsPermanent(wrapped) {
		t.Fatal("IsPermanent did not reach through fmt.Errorf wrapping")
	}
	var got *Problem
	if !errors.As(wrapped, &got) {
		t.Fatal("errors.As did not reach the *Problem")
	}
	if got != p {
		t.Fatalf("errors.As reached %p, want %p", got, p)
	}
	if got.ID != "example.error.sample" {
		t.Fatalf("Problem.ID = %q, want the original ID", got.ID)
	}
	if got.Data["Field"] != "example" {
		t.Fatalf("Problem.Data = %v, want the original map", got.Data)
	}
}

// TestPermanent 覆盖 nil 进 nil 出、标记判定与包装不改写 Problem 的 ID/Data
//
// TestPermanent covers nil in and nil out, the marker check, and that wrapping leaves the ID
// and Data of a Problem untouched
func TestPermanent(t *testing.T) {
	if got := Permanent(nil); got != nil {
		t.Fatalf("Permanent(nil) = %v, want nil", got)
	}

	plain := errors.New("example detail")
	if IsPermanent(plain) {
		t.Fatal("a plain error reported as permanent")
	}
	if !IsPermanent(Permanent(plain)) {
		t.Fatal("Permanent(plain) did not report as permanent")
	}

	p := &Problem{ID: "channel.example.error.missing_url", Data: map[string]any{"Field": "url"}}
	wrapped := Permanent(p)
	if !IsPermanent(wrapped) {
		t.Fatal("Permanent(Problem) did not report as permanent")
	}
	if got := wrapped.Error(); got != p.Error() {
		t.Fatalf("wrapped Error() = %q, want the original %q", got, p.Error())
	}
	var got *Problem
	if !errors.As(wrapped, &got) {
		t.Fatal("errors.As did not reach the wrapped *Problem")
	}
	if got.ID != p.ID || got.Data["Field"] != "url" {
		t.Fatalf("wrapping changed the Problem: ID=%q Data=%v", got.ID, got.Data)
	}
}

// TestPermanent_Nested 覆盖多层 Permanent 与再包装后的判定, 以及 nil 链的判定
//
// TestPermanent_Nested covers the verdict after nested Permanent calls and further wrapping,
// along with the verdict on a nil chain
func TestPermanent_Nested(t *testing.T) {
	base := &Problem{ID: "example.error.sample"}
	if !IsPermanent(fmt.Errorf("example: %w", Permanent(Permanent(base)))) {
		t.Fatal("nested Permanent did not report as permanent")
	}
	if IsPermanent(nil) {
		t.Fatal("a nil error reported as permanent")
	}
}
