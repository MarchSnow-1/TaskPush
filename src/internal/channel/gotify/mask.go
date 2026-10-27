package gotify

/*
Gotify 渠道的脱敏层: token 的遮蔽, 服务端回显文本的清洗, 状态码错误的重包,
以及各类 Problem 的构造
渲染在 render.go, 出网发送在 channel.go, 配置结构在 settings.go
token 只在这里被替换成 ***, 本地构造的错误 Data 从不放入完整地址或 token

The masking layer of the Gotify channel: hiding the token, cleaning the text a server echoes back,
re-wrapping a status-code error, and building the Problem values
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

// httpStatusError 把 common 的状态码错误重包成 gotify 的键
// Status 与 Body 取自原 Problem 的 Data, Body 再过一次脱敏与 token 遮蔽, 因为服务端可能在其中回显凭据;
// 原 Problem 的永久性判定保持不动
//
// httpStatusError re-wraps the status-code error of common under the gotify key
// Status and Body come from the Data of the original Problem, and Body passes through redaction and
// token masking again because the server may echo a credential there; the permanence verdict of the
// original Problem stays as it was
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
// 服务端文案不属于本地构造, 这里只做兜底遮蔽; 本地构造的错误 Data 从不放入完整地址或 token
//
// maskServerText cleans text echoed by the server: an address is masked and then the token is hidden
// Server copy is not built locally, so this is a defensive pass, and a locally built error Data never
// holds the full address or the token
func (p *preparedChannel) maskServerText(text string) string {
	out := common.SanitizeString(text)
	if p.token == "" {
		return out
	}
	return strings.ReplaceAll(out, p.token, maskValue)
}

// invalidURLProblem 构造 url 校验错误, 地址先经 common.MaskURL 打码
// Cause 保留底层判定细节, 但先过 SanitizeError, 免得带凭据的地址被原样写进日志
//
// invalidURLProblem builds the url validation error with the address masked by common.MaskURL first
// The Cause keeps the underlying verdict detail and passes through SanitizeError, so an address
// holding credentials is not written into the log as it came in
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
