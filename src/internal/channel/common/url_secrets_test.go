package common

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 路径级凭据遮蔽的用例: MaskURLWithSecrets 与 PathSecrets 是渠道声明 path 凭据的公共入口
// 覆盖长凭据被打码、无语义端点保持可读、空串与包含关系不破坏替换
//
// Cases for path-level credential masking: MaskURLWithSecrets and PathSecrets are the shared entry a
// channel uses to declare a path credential
// They cover a long credential masked, an endpoint without that meaning staying readable, and an
// empty or nested secret not breaking the replacement
// ---------------------------------------------------------------------------

// TestMaskURLWithSecrets 覆盖在 MaskURL 结果上再遮蔽调用方声明的凭据
//
// TestMaskURLWithSecrets covers masking the declared credentials on top of the MaskURL result
func TestMaskURLWithSecrets(t *testing.T) {
	raw := "https://example.com/services/T00000/example-secret-token"

	got := MaskURLWithSecrets(raw, "example-secret-token")
	if strings.Contains(got, "example-secret-token") {
		t.Fatalf("MaskURLWithSecrets = %q, want the declared secret masked", got)
	}
	if !strings.Contains(got, "services/T00000") {
		t.Fatalf("MaskURLWithSecrets = %q, want the harmless path kept", got)
	}

	if got := MaskURLWithSecrets(raw); got != MaskURL(raw) {
		t.Fatalf("MaskURLWithSecrets without secrets = %q, want the MaskURL result %q", got, MaskURL(raw))
	}
	if got := MaskURLWithSecrets(raw, ""); strings.Contains(got, "/***") || strings.Count(got, "***") > 1 {
		t.Fatalf("MaskURLWithSecrets with an empty secret = %q, want the text left alone", got)
	}
}

// TestMaskURLWithSecretsLongestFirst 覆盖包含关系的两个凭据按长度降序替换
// 短的先替换会把长凭据的后半截留在输出里
//
// TestMaskURLWithSecretsLongestFirst covers two nested credentials replaced in descending length
// A shorter one applied first would leave the tail of the longer one in the output
func TestMaskURLWithSecretsLongestFirst(t *testing.T) {
	got := MaskURLWithSecrets("https://example.com/hook", "example", "example-token-long")
	if strings.Contains(got, "token-long") {
		t.Fatalf("MaskURLWithSecrets = %q, want the longer secret fully masked", got)
	}
}

// TestMaskURLWithSecretsKeepsCallerSlice 覆盖替换不改动调用方传入的切片
//
// TestMaskURLWithSecretsKeepsCallerSlice covers the replacement leaving the caller's slice untouched
func TestMaskURLWithSecretsKeepsCallerSlice(t *testing.T) {
	secrets := []string{"short", "a-much-longer-secret"}
	MaskURLWithSecrets("https://example.com/hook", secrets...)
	if secrets[0] != "short" || secrets[1] != "a-much-longer-secret" {
		t.Fatalf("caller slice = %v, want the original order", secrets)
	}
}

// TestPathSecrets 覆盖 path 末段的提取, 含段数不足、根路径与不可解析地址
//
// TestPathSecrets covers reading the trailing path segments, including a short path, a root path and
// an unparsable address
func TestPathSecrets(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		count int
		want  string
	}{
		{name: "two of three", raw: "https://example.com/a/b/c", count: 2, want: "c,b"},
		{name: "one of three", raw: "https://example.com/a/b/c", count: 1, want: "c"},
		{name: "trailing slash", raw: "https://example.com/a/b/", count: 1, want: "b"},
		{name: "short path", raw: "https://example.com/token", count: 2, want: "token"},
		{name: "root path", raw: "https://example.com/", count: 1, want: ""},
		{name: "no path", raw: "https://example.com", count: 1, want: ""},
		{name: "empty", raw: "", count: 1, want: ""},
		{name: "unparsable", raw: "https://exam ple.com/hook/token", count: 1, want: ""},
		{name: "zero count", raw: "https://example.com/a/b", count: 0, want: "b,a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := strings.Join(PathSecrets(tc.raw, tc.count), ","); got != tc.want {
				t.Errorf("PathSecrets(%q, %d) = %q, want %q", tc.raw, tc.count, got, tc.want)
			}
		})
	}
}
