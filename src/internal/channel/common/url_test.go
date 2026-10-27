package common

import (
	"errors"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// ---------------------------------------------------------------------------
// URL 规范化与打码测试, 覆盖协议补全, 回环判定, 路径与 query 打码以及非法输入拒绝
// 所有输入输出都是字符串层面的, 不发网络
//
// The URL normalisation and masking tests, covering scheme defaulting, the loopback rule, path and
// query masking and the rejection of an illegal input
// Every case stays on the string level and touches no network
// ---------------------------------------------------------------------------

// mustProblem 断言 err 是 *apperr.Problem 且 ID 与 Data.Field 符合预期
//
// mustProblem asserts that err is an *apperr.Problem whose ID and Data.Field match the expectation
func mustProblem(t *testing.T, err error, id, field string) *apperr.Problem {
	t.Helper()
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("error type = %T (%v), want *apperr.Problem", err, err)
	}
	if prob.ID != id {
		t.Errorf("problem ID = %q, want %q", prob.ID, id)
	}
	if got := prob.Data["Field"]; got != field {
		t.Errorf("problem Data.Field = %v, want %q", got, field)
	}
	return prob
}

func TestNormalizeURLKeepsSchemeHostPathQueryFragment(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"http", "http://example.com", "http://example.com"},
		{"https", "https://example.com", "https://example.com"},
		{"trim space", "  https://example.com/push?token=example  \t", "https://example.com/push?token=example"},
		{"path query fragment", "https://example.com/a/b?x=1&y=2#frag", "https://example.com/a/b?x=1&y=2#frag"},
		{"port", "https://example.com:8443/push", "https://example.com:8443/push"},
		{"userinfo kept", "https://user@example.com/push", "https://user@example.com/push"},
		{"scheme case lowered", "HTTPS://example.com/Push", "https://example.com/Push"},
		{"missing slash typo", "https:/example.com/push", "https://example.com/push"},
		{"missing slashes typo", "https:example.com/push", "https://example.com/push"},
		{"explicit loopback", "http://127.0.0.1:8080/push", "http://127.0.0.1:8080/push"},
		{"bracketed ipv6", "http://[::1]:8080/push", "http://[::1]:8080/push"},
		{"scheme relative", "//example.com/push", "https://example.com/push"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NormalizeURL(c.in)
			if err != nil {
				t.Fatalf("NormalizeURL(%q) error: %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("NormalizeURL(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestNormalizeURLAddsScheme 覆盖缺 scheme 时的补全规则: 回环走 http, 其余走 https, 裸 IPv6 加括号
//
// TestNormalizeURLAddsScheme covers the completion rules for a missing scheme: loopback takes
// http, anything else takes https, and a bare IPv6 gets brackets
func TestNormalizeURLAddsScheme(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"example.com", "https://example.com"},
		{"example.com/push?x=1#f", "https://example.com/push?x=1#f"},
		{"example.com:8443", "https://example.com:8443"},
		{"sub.example.com", "https://sub.example.com"},
		{"localhost", "http://localhost"},
		{"LOCALHOST:8080", "http://LOCALHOST:8080"},
		{"localhost/push", "http://localhost/push"},
		{"127.0.0.1:8080", "http://127.0.0.1:8080"},
		{"127.5.5.5", "http://127.5.5.5"}, // 127.0.0.0/8 整段都算回环 / the whole 127.0.0.0/8 is loopback
		{"::1", "http://[::1]"},
		{"[::1]:8080", "http://[::1]:8080"},
		{"2001:db8::1", "https://[2001:db8::1]"},
		{"10.0.0.1:8080", "https://10.0.0.1:8080"},
	}
	for _, c := range cases {
		got, err := NormalizeURL(c.in)
		if err != nil {
			t.Fatalf("NormalizeURL(%q) error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeURLErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		id   string
	}{
		{"empty", "", keyErrorEmptyURL},
		{"whitespace only", "   ", keyErrorEmptyURL},
		{"unsupported scheme", "ftp://example.com/push", keyErrorInvalidURL},
		{"opaque scheme", "mailto:someone@example.com", keyErrorInvalidURL},
		{"empty host", "https://", keyErrorMissingHost},
		{"empty host with path", "http:///push", keyErrorMissingHost},
		{"bad port", "https://example.com:notaport", keyErrorInvalidURL},
		{"unclosed ipv6 bracket", "https://[::1", keyErrorInvalidURL},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NormalizeURL(c.in)
			if err == nil {
				t.Fatalf("NormalizeURL(%q) = %q, want an error", c.in, got)
			}
			mustProblem(t, err, c.id, defaultURLField)
		})
	}
}

func TestValidateHTTPURLUsesFieldInError(t *testing.T) {
	_, err := ValidateHTTPURL("", "avatarUrl")
	if err == nil {
		t.Fatal("ValidateHTTPURL with an empty value should fail")
	}
	prob := mustProblem(t, err, keyErrorEmptyURL, "avatarUrl")
	if prob.Cause != nil {
		t.Errorf("Cause = %v, want nil for an empty value", prob.Cause)
	}

	got, err := ValidateHTTPURL("example.com/push", "server")
	if err != nil {
		t.Fatalf("ValidateHTTPURL: %v", err)
	}
	if got != "https://example.com/push" {
		t.Errorf("ValidateHTTPURL = %q, want %q", got, "https://example.com/push")
	}

	// 空 field 回落到默认占位, 避免文案里出现空字段名
	// A blank field falls back to the default placeholder so the copy holds no empty name
	if _, err := ValidateHTTPURL("ftp://example.com", "  "); err == nil {
		t.Fatal("ValidateHTTPURL with an unsupported scheme should fail")
	} else {
		mustProblem(t, err, keyErrorInvalidURL, defaultURLField)
	}
}

// TestValidateHTTPErrorsCarryField 覆盖非法地址把 field 写进 Data 的每条分支
//
// TestValidateHTTPErrorsCarryField covers each failure branch carrying field into Data
func TestValidateHTTPErrorsCarryField(t *testing.T) {
	cases := []struct {
		in string
		id string
	}{
		{"ftp://example.com", keyErrorInvalidURL},
		{"https://", keyErrorMissingHost},
		{"https://example.com:oops", keyErrorInvalidURL},
	}
	for _, c := range cases {
		_, err := ValidateHTTPURL(c.in, "url")
		if err == nil {
			t.Fatalf("ValidateHTTPURL(%q) should fail", c.in)
		}
		mustProblem(t, err, c.id, "url")
	}
}

func TestMaskURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "userinfo and sensitive query",
			in:   "https://example:secret@example.com/push?token=example-token&title=hello",
			want: "https://***@example.com/push?token=***&title=hello",
		},
		{
			name: "all sensitive keys",
			in:   "https://example.com/cb?key=1&access_token=2&sign=3&secret=4&other=5",
			want: "https://example.com/cb?key=***&access_token=***&sign=***&secret=***&other=5",
		},
		{
			name: "key matched case insensitively",
			in:   "https://example.com/cb?Token=abc",
			want: "https://example.com/cb?Token=***",
		},
		{
			name: "bare sensitive name without value",
			in:   "https://example.com/cb?token",
			want: "https://example.com/cb?***",
		},
		{
			name: "path and fragment kept",
			in:   "https://example.com/a/b?x=1#frag",
			want: "https://example.com/a/b?x=1#frag",
		},
		{
			name: "encoding kept as written",
			in:   "https://example.com/a%20b?q=hello%2Bworld",
			want: "https://example.com/a%20b?q=hello%2Bworld",
		},
		{
			name: "no scheme",
			in:   "//example.com/push?token=abc",
			want: "//example.com/push?token=***",
		},
		{
			name: "empty",
			in:   "",
			want: "",
		},
		{
			name: "opaque",
			in:   "mailto:someone@example.com",
			want: redactedURL,
		},
		{
			name: "no host",
			in:   "not a url",
			want: redactedURL,
		},
		{
			name: "percent escape error",
			in:   "https://example.com/%zz",
			want: redactedURL,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MaskURL(c.in); got != c.want {
				t.Errorf("MaskURL(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestNormalizeURLCauseRedacted 确认 url.Parse 原文里带地址时, Cause 也是脱敏过的
//
// TestNormalizeURLCauseRedacted confirms that when the url.Parse text quotes an address, the
// Cause is redacted as well
func TestNormalizeURLCauseRedacted(t *testing.T) {
	cases := []string{
		"https://[::1",
		"https://ex ample.com/push",
	}
	for _, in := range cases {
		_, err := ValidateHTTPURL(in, "url")
		if err == nil {
			t.Fatalf("ValidateHTTPURL(%q) should fail", in)
		}
		var prob *apperr.Problem
		if !errors.As(err, &prob) {
			t.Fatalf("error type = %T, want *apperr.Problem", err)
		}
		if prob.Cause == nil {
			t.Fatalf("Cause of %q failure = nil, want the parse error", in)
		}
		// Cause 是 sanitizedError: 文本已打码, 但 Unwrap 仍指向原始解析错误
		// The Cause is a sanitizedError: its text is masked while Unwrap still reaches the
		// original parse error
		if text := prob.Cause.Error(); urlInTextRegexp.MatchString(text) {
			t.Errorf("Cause text for %q = %q, want the address masked", in, text)
		}
		if errors.Unwrap(prob.Cause) == nil {
			t.Errorf("Cause for %q lost the original error, so errors.Is could not pass through", in)
		}
	}
}
