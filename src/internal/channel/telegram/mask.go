package telegram

/*
telegram 渠道的脱敏层: 服务端回显文本的清洗, common 助手返回 Problem 的再遮蔽,
以及 apiBase 校验错误的构造
发送循环与多目标聚合在 channel.go, 渲染在 render.go, 配置结构在 settings.go

The masking layer of the telegram channel: cleaning server-echoed text, masking a Problem returned
by a common helper again, and building the apiBase validation error
The send loop and the multi-target aggregation sit in channel.go, rendering in render.go, and the
configuration shapes in settings.go
*/

import (
	"errors"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
)

// maskServerText 清洗服务端回显文本: URL 打码后再遮蔽 token
// 服务端文案不属于本地构造, 这里只做兜底遮蔽, 渠道自身不把任何凭据写进 Data
//
// maskServerText cleans text echoed by the server: URLs are masked and then the token is hidden
// Server copy is not built locally, so this is a defensive pass; the channel itself writes no
// credential into Data
func (p *preparedChannel) maskServerText(text string) string {
	out := common.SanitizeString(text)
	if p.token == "" {
		return out
	}
	return strings.ReplaceAll(out, p.token, maskValue)
}

// maskProblem 遮蔽 common 助手返回的 Problem 里可能回显 token 的自由文本
// 只处理字符串 Data, 并保留 *apperr.Problem 的键与 Permanent 标记: 分类由 common 决定, 这里
// 只做凭据遮蔽
//
// maskProblem masks free text inside a Problem returned by a common helper, since the server may
// echo the token there
// Only string Data values are rewritten, while the message key and the Permanent marker are kept:
// common decides the classification and this step only hides credentials
func (p *preparedChannel) maskProblem(err error) error {
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		return err
	}
	data := make(map[string]any, len(prob.Data))
	for k, v := range prob.Data {
		if s, ok := v.(string); ok {
			data[k] = p.maskServerText(s)
			continue
		}
		data[k] = v
	}
	masked := &apperr.Problem{ID: prob.ID, Data: data, Cause: prob.Cause}
	if apperr.IsPermanent(err) {
		return apperr.Permanent(masked)
	}
	return masked
}

// problem 构造只带消息键与可选 Data 的 Problem
//
// problem builds a Problem holding a message key and optional Data
func problem(id string, data map[string]any) *apperr.Problem {
	return &apperr.Problem{ID: id, Data: data}
}

// invalidAPIBaseProblem 构造 apiBase 校验错误, URL 先经 MaskURL
// Cause 保留底层判定细节, 但先过 SanitizeError, 免得非法地址被原样写进日志
//
// invalidAPIBaseProblem builds the apiBase validation error with the URL masked first
// The Cause keeps the underlying verdict detail and passes through SanitizeError, so an invalid
// address is not written into the log as it came in
func invalidAPIBaseProblem(raw string, cause error) *apperr.Problem {
	return &apperr.Problem{
		ID:    keyErrorInvalidBase,
		Data:  map[string]any{"URL": common.MaskURL(raw)},
		Cause: common.SanitizeError(cause),
	}
}
