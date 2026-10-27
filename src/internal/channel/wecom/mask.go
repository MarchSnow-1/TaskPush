package wecom

/*
wecom 渠道的遮蔽层: URL query 里 key 的提取, 服务端回显文本的遮蔽,
URL 错误 Problem 与通用 Problem 的构造
渲染与截断在 render.go, 出网发送在 channel.go, 配置结构在 settings.go

The masking layer of the wecom channel: extracting the key from the URL query, masking
server-echoed text, and building the URL error Problem together with the general Problem
Rendering and truncation sit in render.go, sending in channel.go, and the configuration shapes in
settings.go
*/

import (
	"net/url"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
)

// queryKey 取 URL query 里的 key 值, 已 TrimSpace
// 地址无法解析时返回空串, 调用方按缺 key 处理
//
// queryKey reads the key value out of the URL query, trimmed
// An address that does not parse yields an empty string, which the caller treats as a missing key
func queryKey(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(u.Query().Get("key"))
}

// maskServerText 清洗服务端回显文本: URL 打码后再遮蔽 key
// 服务端文案不属于本地构造, 这里做兜底遮蔽, 渠道自身也不把 key 写进 Data
//
// maskServerText cleans server-echoed text: URLs are masked and then the key is hidden
// Server copy is not built locally, so this is a defensive pass, and the channel itself writes no
// key into Data either
func (p *preparedChannel) maskServerText(text string) string {
	out := common.SanitizeString(text)
	if p.key == "" {
		return out
	}
	return strings.ReplaceAll(out, p.key, maskValue)
}

// invalidURLProblem 构造 URL 校验错误, URL 先经 common.MaskURL 遮蔽 key
// Cause 保留底层判定细节, 但先过 SanitizeError, 免得非法地址被原样写进日志
//
// invalidURLProblem builds the URL validation error with the URL masked first by common.MaskURL,
// which hides the key
// The Cause keeps the underlying verdict detail and passes through SanitizeError, so an invalid
// address is not written into the log as it came in
func invalidURLProblem(raw string, cause error) *apperr.Problem {
	return &apperr.Problem{
		ID:    keyErrorInvalidURL,
		Data:  map[string]any{"URL": common.MaskURL(raw)},
		Cause: common.SanitizeError(cause),
	}
}

// problem 构造只带消息键与可选 Data 的 Problem
//
// problem builds a Problem holding a message key and optional Data
func problem(id string, data map[string]any) *apperr.Problem {
	return &apperr.Problem{ID: id, Data: data}
}
