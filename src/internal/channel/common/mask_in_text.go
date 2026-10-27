package common

import (
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// 裸凭据遮蔽: SanitizeString 只认 scheme://... 形态的 URL, 裸 token 会原样透出
// 服务端常把收到的凭据回显在错误响应体里, 这种回显既不是 URL 也没有固定前缀,
// 只有拿渠道已知的凭据逐个替换才能挡住
// MaskInText 是给各渠道重包 common.error.http_status 时用的公共入口
//
// Bare-credential masking: SanitizeString only recognises the scheme://... form of a URL, so a
// bare token passes through untouched
// A server often echoes the credentials it received inside an error body, and such an echo is
// neither a URL nor a fixed prefix, so only the secrets this channel already knows can hide it by
// literal replacement
// MaskInText is the shared entry the channels use when they re-wrap common.error.http_status
// ---------------------------------------------------------------------------

// MaskInText 先按公共规则打码文本里的地址, 再把每个非空 secret 的出现处替换成 ***
// 替换按 secret 长度降序进行: 较短的先替换会破坏较长 secret 的匹配, "example" 排在
// "example-token-long" 之前会把后半截留在输出里
// 空串 secret 必须跳过, 否则 ReplaceAll 会在每个字符之间插入替换文本
// 替换是字面量比较, 不做正则解释, secret 含正则元字符时按原文匹配
// secrets 为空时等价于 SanitizeString
//
// MaskInText masks the addresses inside the text with the shared rule first and then replaces every
// occurrence of each non-empty secret with ***
// The replacements run in descending order of secret length, since a shorter secret applied first
// would break the match of a longer one: with "example" ahead of "example-token-long" the tail of
// the longer secret would stay in the output
// An empty secret must be skipped, otherwise ReplaceAll would insert the replacement between every
// pair of characters
// The replacement is a literal comparison, not a regular expression, so a secret holding regex
// metacharacters matches as written
// With no secrets it behaves like SanitizeString
func MaskInText(text string, secrets ...string) string {
	out := SanitizeString(text)
	if len(secrets) == 0 {
		return out
	}

	// 排序在副本上做, 调用方传入的切片顺序与内容都不被改动
	//
	// The sort runs on a copy, so neither the order nor the content of the caller's slice changes
	sorted := make([]string, len(secrets))
	copy(sorted, secrets)
	sort.SliceStable(sorted, func(i, j int) bool {
		return len(sorted[i]) > len(sorted[j])
	})

	for _, secret := range sorted {
		if secret == "" {
			continue
		}
		out = strings.ReplaceAll(out, secret, maskValue)
	}
	return out
}
