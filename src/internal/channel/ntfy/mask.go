package ntfy

/*
ntfy 渠道的脱敏层: token 的遮蔽, 服务端回显文本的清洗, 状态码错误的重包,
以及各类 Problem 的构造与配置取值的压缩
渲染在 render.go, 出网发送在 channel.go, 配置结构在 settings.go
token 只在这里被替换成 ***, 本地构造的错误 Data 从不放入完整地址与 token

The masking layer of ntfy: hiding the token, cleaning the text a server echoes back, re-wrapping a
status-code error, and building the Problem values together with compressing a configured value
Rendering sits in render.go, sending in channel.go, and the configuration shapes in settings.go
The token becomes *** here alone, and a locally built error Data never holds the full address or the
token
*/

import (
	"errors"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
)

// httpStatusError 把 common 的状态码错误重包成 ntfy 的键
// Status 与 Body 取自原 Problem 的 Data, Body 再过一次脱敏与 token 遮蔽, 因为服务端或中间层可能在
// 其中回显地址与凭据; 原 Problem 的永久性判定保持不动, 非 Problem 的错误经 SanitizeError 返回
//
// httpStatusError re-wraps the status-code error of common under the ntfy key
// Status and Body come from the Data of the original Problem, and Body passes redaction and token
// masking again since the server or a middle layer may echo an address and a credential there
// The permanence verdict of the original Problem stays as it is, and an error that is not a Problem
// returns through SanitizeError
func (p *preparedChannel) httpStatusError(err error) error {
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		return common.SanitizeError(err)
	}
	body, _ := prob.Data["Body"].(string)
	wrapped := &apperr.Problem{
		ID: keyErrorHTTPStatus,
		Data: map[string]any{
			"Status": prob.Data["Status"],
			"Body":   p.maskServerText(body),
		},
		Cause: common.SanitizeError(err),
	}
	if apperr.IsPermanent(err) {
		return apperr.Permanent(wrapped)
	}
	return wrapped
}

// maskServerText 清洗服务端回显文本: 地址打码后再遮蔽 token
// 服务端文案不属于本地构造, 这里只做兜底遮蔽; 本地构造的错误 Data 从不放入完整地址与 token
//
// maskServerText cleans text echoed by the server: addresses are masked and then the token is
// hidden
// Server copy is not built locally, so this is a defensive pass; a locally built error Data never
// holds the full address or the token
func (p *preparedChannel) maskServerText(text string) string {
	out := common.SanitizeString(text)
	if p.token == "" {
		return out
	}
	return strings.ReplaceAll(out, p.token, maskValue)
}

// problem 构造只带消息键与可选 Data 的 Problem
//
// problem builds a Problem holding a message key and optional Data
func problem(id string, data map[string]any) *apperr.Problem {
	return &apperr.Problem{ID: id, Data: data}
}

// invalidURLProblem 构造地址校验错误, Data.URL 取 MaskURL 的打码结果
// Cause 保留底层判定细节, 但先过 SanitizeError, 免得带凭据的地址被原样写进日志
//
// invalidURLProblem builds the address validation error with Data.URL taken from MaskURL
// The Cause keeps the underlying verdict detail and passes through SanitizeError first, so an
// address holding credentials is not written into the log as it came in
func invalidURLProblem(raw string, cause error) *apperr.Problem {
	return &apperr.Problem{
		ID:    keyErrorInvalidURL,
		Data:  map[string]any{"URL": common.MaskURL(raw)},
		Cause: common.SanitizeError(cause),
	}
}

// displayValue 把配置原文压成可放进错误 Data 的短文本: 先按 rune 截断再整体打码
//
// displayValue compresses the configured text into a short value an error Data can carry: it is
// cut by rune count first and redacted as a whole
func displayValue(s string) string {
	if r := []rune(s); len(r) > valueDisplayLimit {
		s = string(r[:valueDisplayLimit]) + "…"
	}
	return common.SanitizeString(s)
}
