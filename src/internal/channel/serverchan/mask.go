package serverchan

/*
serverchan 渠道的脱敏层: 服务端回显文本的清洗, SendKey 掩码, 以及 Problem 的构造
渲染与列表拼接在 render.go, 发送在 channel.go, 配置结构与路由判定在 settings.go

The masking layer of the serverchan channel: cleaning server-echoed text, masking a SendKey, and
building a Problem
Rendering with the list joining sits in render.go, sending in channel.go, and the configuration
shapes with the route decision in settings.go
*/

import (
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
)

// maskServerText 清洗服务端回显文本: URL 打码后再遮蔽 token
// 服务端文案不属于本地构造, 这里做兜底遮蔽, 渠道自身也不把 token 写进 Data
//
// maskServerText cleans server-echoed text: URLs are masked and then the token is hidden
// Server copy is not built locally, so this is a defensive pass, and the channel itself writes no
// token into Data either
func (p *preparedChannel) maskServerText(text string) string {
	out := common.SanitizeString(text)
	if p.token == "" {
		return out
	}
	return strings.ReplaceAll(out, p.token, maskValue)
}

// tokenMask 返回错误数据里可用的 SendKey 掩码
// 保留前 4 个 rune 让用户定位是哪把 key, 其余内容替换为 ***; 过短的 key 整体遮蔽
//
// tokenMask returns the SendKey mask used in error data
// Up to the first 4 runes stay so a user can tell which key is wrong, the rest becomes ***, and a
// shorter key is masked as a whole
func tokenMask(token string) string {
	runes := []rune(token)
	if len(runes) <= tokenMaskPrefixRunes {
		return maskValue
	}
	return string(runes[:tokenMaskPrefixRunes]) + maskValue
}

// problem 构造只带消息键与可选 Data 的 Problem
//
// problem builds a Problem holding a message key and optional Data
func problem(id string, data map[string]any) *apperr.Problem {
	return &apperr.Problem{ID: id, Data: data}
}
