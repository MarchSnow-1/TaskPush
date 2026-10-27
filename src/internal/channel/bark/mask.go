package bark

/*
bark 渠道的脱敏层: 凭据的收集与放置, 服务端回显文本的遮蔽, 以及各类 Problem 的构造与错误文本的脱敏
渲染在 render.go, 出网发送在 channel.go, 配置结构在 settings.go

The masking layer of the bark channel: collecting and carrying the credentials, masking what the
server echoes back, and building the Problem values together with redacting error text
Rendering sits in render.go, sending in channel.go, and the configuration shapes in settings.go
*/

import (
	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
)

// maskValue 替换服务端文本里回显的 token 与 device key
//
// maskValue replaces a token or device key echoed inside server-supplied text
const maskValue = "***"

// badJSONProblem 构造可重试的响应解析错误, 复用公共层的 common.error.bad_json 消息键
//
// badJSONProblem builds the retryable response-parsing error under the common.error.bad_json message
// key of the shared layer
func badJSONProblem(cause error) *apperr.Problem {
	return &apperr.Problem{
		ID:    "common.error.bad_json",
		Data:  map[string]any{"Field": "body", "Detail": common.SanitizeString(cause.Error())},
		Cause: common.SanitizeError(cause),
	}
}

// badResponseProblem 构造可重试的响应结构错误
//
// badResponseProblem builds a retryable response-shape error
func badResponseProblem(detail string) *apperr.Problem {
	return &apperr.Problem{
		ID:   "common.error.bad_response",
		Data: map[string]any{"Field": "body", "Detail": common.SanitizeString(detail)},
	}
}

// invalidServerProblem 构造 server 校验错误, URL 先经 MaskURL
//
// invalidServerProblem builds the server validation error with the URL masked first
func invalidServerProblem(raw string, cause error) *apperr.Problem {
	return &apperr.Problem{
		ID:    keyErrorInvalidServer,
		Data:  map[string]any{"URL": common.MaskURL(raw)},
		Cause: common.SanitizeError(cause),
	}
}

// invalidURLProblem 构造 icon/image/clickUrl 的校验错误, Field 与掩码后的 URL 进 Data
//
// invalidURLProblem builds the icon, image and clickUrl validation error, with the field and the
// masked URL in Data
func invalidURLProblem(field, raw string, cause error) *apperr.Problem {
	return &apperr.Problem{
		ID:    keyErrorInvalidURL,
		Data:  map[string]any{"Field": field, "URL": common.MaskURL(raw)},
		Cause: common.SanitizeError(cause),
	}
}

// problem 构造只带消息键与可选 Data 的 Problem
//
// problem builds a Problem holding a message key and optional Data
func problem(id string, data map[string]any) *apperr.Problem {
	return &apperr.Problem{ID: id, Data: data}
}
