package common

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// MaskInText 测试: 裸 token、URL 形态、空 secret、包含关系与正则元字符都在覆盖范围内
//
// MaskInText tests: a bare token, a URL, an empty secret, containment and regex metacharacters are
// all covered
// ---------------------------------------------------------------------------

// TestMaskInTextMasksBareToken 验证裸 token 被替换, SanitizeString 不认识它
//
// TestMaskInTextMasksBareToken checks that a bare token is replaced, which SanitizeString misses
func TestMaskInTextMasksBareToken(t *testing.T) {
	const token = "example-secret-token"
	in := "the server rejected token " + token + " twice"

	got := MaskInText(in, token)
	want := "the server rejected token *** twice"
	if got != want {
		t.Errorf("MaskInText = %q, want %q", got, want)
	}
}

// TestMaskInTextMasksURLForm 验证 URL 形态仍由 SanitizeString 打码为 <redacted-url>
//
// TestMaskInTextMasksURLForm checks that a URL still gets the <redacted-url> form from SanitizeString
func TestMaskInTextMasksURLForm(t *testing.T) {
	in := "post to https://example.com/hook?token=example-secret-token failed"

	got := MaskInText(in, "example-secret-token")
	want := "post to <redacted-url> failed"
	if got != want {
		t.Errorf("MaskInText = %q, want %q", got, want)
	}
}

// TestMaskInTextWithoutSecrets 验证空 secret 列表时行为与 SanitizeString 相同
//
// TestMaskInTextWithoutSecrets checks that an empty secret list behaves like SanitizeString
func TestMaskInTextWithoutSecrets(t *testing.T) {
	in := "plain text and https://example.com/hook"

	got := MaskInText(in)
	want := SanitizeString(in)
	if got != want {
		t.Errorf("MaskInText = %q, want SanitizeString = %q", got, want)
	}
}

// TestMaskInTextSkipsEmptySecret 验证空字符串 secret 被跳过, 输出不被替换文本炸掉
//
// TestMaskInTextSkipsEmptySecret checks that an empty secret is skipped and the output survives
func TestMaskInTextSkipsEmptySecret(t *testing.T) {
	in := "unchanged text"

	got := MaskInText(in, "")
	want := in
	if got != want {
		t.Errorf("MaskInText = %q, want %q", got, want)
	}
}

// TestMaskInTextMasksLongerSecretFirst 验证一长一短有包含关系时, 长的先替换,
// 短的不会把长的截断成残留
//
// TestMaskInTextMasksLongerSecretFirst checks that with one secret inside another the longer one is
// replaced first, which keeps the shorter one from shredding the longer match
func TestMaskInTextMasksLongerSecretFirst(t *testing.T) {
	const long = "example-long-secret"
	const short = "example"

	got := MaskInText("key is "+long+" and also "+short, short, long)
	want := "key is *** and also ***"
	if got != want {
		t.Errorf("MaskInText = %q, want %q", got, want)
	}
}

// TestMaskInTextSecretWithRegexMetacharacters 验证替换按字面量进行, secret 里的
// 正则元字符不参与模式匹配
//
// TestMaskInTextSecretWithRegexMetacharacters checks that the replacement is literal and the regex
// metacharacters inside a secret take no part in pattern matching
func TestMaskInTextSecretWithRegexMetacharacters(t *testing.T) {
	const secret = "example.secret+(a)[b]"
	in := "echo " + secret + " end"

	got := MaskInText(in, secret)
	want := "echo *** end"
	if got != want {
		t.Errorf("MaskInText = %q, want %q", got, want)
	}
}

// TestMaskInTextMaskedValueIsLiteral 验证替换文本本身不含 secret 的任何片段
//
// TestMaskInTextMaskedValueIsLiteral checks that the replacement keeps no fragment of the secret
func TestMaskInTextMaskedValueIsLiteral(t *testing.T) {
	const token = "example-secret-token"
	in := "prefix " + token + " suffix"

	got := MaskInText(in, token)
	if strings.Contains(got, "example") {
		t.Errorf("MaskInText = %q, want no fragment of the secret left", got)
	}
	if !strings.Contains(got, maskValue) {
		t.Errorf("MaskInText = %q, want it to hold %q", got, maskValue)
	}
}
