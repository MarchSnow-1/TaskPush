package discord

/*
discord 渠道的脱敏层: 服务端回显文本的清洗, webhook 路径凭据的提取与地址打码,
以及各类 Problem 的构造
渲染在 render.go, 发送在 channel.go, 配置结构在 settings.go

The masking layer of the discord channel: cleaning server-echoed text, reading the credential
segments out of a webhook path, masking an address, and building the Problem values
Rendering sits in render.go, sending in channel.go, and the configuration shapes in settings.go
*/

import (
	"net/url"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
)

// maskServerText 清洗服务端回显文本: URL 打码后再遮蔽 webhook token
// 服务端文案不属于本地构造, 这里只做兜底遮蔽; 本地构造的错误 Data 不放入完整地址
//
// maskServerText cleans text echoed by the server: URLs are masked and then the webhook token is
// hidden
// Server copy is not built locally, so this is a defensive pass; a locally built error Data does
// not hold the full address
func (p *preparedChannel) maskServerText(text string) string {
	out := common.SanitizeString(text)
	for _, secret := range webhookSecrets(p.endpoint) {
		out = strings.ReplaceAll(out, secret, maskValue)
	}
	return out
}

// webhookSecrets 从 webhook 地址的路径里取出 id 与 token 两段
// 分段规则由 common.PathSecrets 提供, 让「哪些段算凭据」只在一处维护
//
// webhookSecrets reads the id and token path segments of a webhook address
// The shared PathSecrets helper supplies the rule, which keeps the "which segments are credentials"
// decision in one place
func webhookSecrets(endpoint string) []string {
	return common.PathSecrets(endpoint, 2)
}

// maskURLSecrets 在 MaskURL 的结果上再遮蔽 Discord webhook 路径里的 id 与 token
// MaskURL 保留 path 与普通 query, 而 Discord 把凭据放在 /webhooks/<id>/<token> 的末两段
// 路径里没有 webhooks 段时只返回 MaskURL 的结果, 其它形态的地址因此不受影响
// 替换与排序交给 common.MaskURLWithSecrets, 本函数只判断哪些段是凭据
//
// maskURLSecrets masks the id and the token of a Discord webhook path on top of the MaskURL result
// MaskURL keeps the path and an ordinary query, while Discord puts its credential in the last two
// segments of /webhooks/<id>/<token>
// A path without a webhooks segment returns the MaskURL result alone, which leaves an address of
// another shape unaffected
// common.MaskURLWithSecrets owns the replacement and its ordering, and this function only decides
// which segments are credentials
func maskURLSecrets(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.Contains("/"+strings.Trim(u.Path, "/")+"/", "/webhooks/") {
		return common.MaskURL(raw)
	}
	return common.MaskURLWithSecrets(raw, webhookSecrets(raw)...)
}

// problem 构造只带消息键与可选 Data 的 Problem
//
// problem builds a Problem holding a message key and optional Data
func problem(id string, data map[string]any) *apperr.Problem {
	return &apperr.Problem{ID: id, Data: data}
}

// invalidURLProblem 构造 url 校验错误, 地址先经 MaskURL
// Cause 保留底层判定细节, 但先过 SanitizeError, 免得带凭据的地址被原样写进日志
//
// invalidURLProblem builds the url validation error with the address masked first
// The Cause keeps the underlying verdict detail and passes through SanitizeError, so an address
// holding credentials is not written into the log as it came in
func invalidURLProblem(raw string, cause error) *apperr.Problem {
	return &apperr.Problem{
		ID:    keyErrorInvalidURL,
		Data:  map[string]any{"URL": maskURLSecrets(raw)},
		Cause: common.SanitizeError(cause),
	}
}

// invalidAvatarProblem 构造 avatarUrl 校验错误, 地址先经 MaskURL
// avatarUrl 不带 webhook 的路径凭据, 因此这里只用公共层打码, 不再遮蔽路径末两段
//
// invalidAvatarProblem builds the avatarUrl validation error with the address masked by MaskURL
// An avatarUrl carries no webhook path credential, so the shared masking alone applies and the last
// two path segments are left alone
func invalidAvatarProblem(raw string, cause error) *apperr.Problem {
	return &apperr.Problem{
		ID:    keyErrorInvalidAvatar,
		Data:  map[string]any{"URL": common.MaskURL(raw)},
		Cause: common.SanitizeError(cause),
	}
}
