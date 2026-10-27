package wecom

/*
wecom 渠道的渠道实现: Channel 与 Prepared 的类型, Prepare 与 Send, 以及把校验错误改写为渠道键
内容拼接与截断在 render.go, key 提取与遮蔽在 mask.go, 配置结构在 settings.go

The channel implementation of wecom: the Channel and Prepared types, Prepare and Send, and the
rewriting of a verification error under the channel keys
Content joining and truncation sit in render.go, key extraction and masking in mask.go, and the
configuration shapes in settings.go
*/

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
)

// wecomChannel 是一个已配置的 wecom 渠道
//
// wecomChannel is one configured wecom channel
type wecomChannel struct {
	id string
	s  wecomSettings
}

// ID 返回渠道实例标识
//
// ID returns the channel instance identifier
func (c *wecomChannel) ID() string { return c.id }

// Type 返回渠道类型名
//
// Type returns the channel type name
func (c *wecomChannel) Type() string { return "wecom" }

// preparedChannel 是一次 Prepare 的产物, 可被调度层重复 Send
//
// preparedChannel is the outcome of one Prepare and can be sent repeatedly by the scheduling layer
type preparedChannel struct {
	endpoint string
	msgType  string
	key      string
	payload  sendPayload
}

// Prepare 渲染 settings, 校验 URL 与内容并构造请求体, 不做网络 I/O
// 返回的 warnings 覆盖模板未解析变量与正文截断
//
// Prepare renders the settings, validates the URL and the content and builds the request body
// without network I/O
// The returned warnings cover an unresolved template variable and a truncated body
func (c *wecomChannel) Prepare(_ context.Context, msg channel.Message) (channel.Prepared, []apperr.Problem, error) {
	mode := common.TemplateMode(msg.Mode)
	var warnings []apperr.Problem

	endpoint, warns, err := renderField(c.s.URL, "settings.url", msg.Vars, mode)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}
	warnings = append(warnings, warns...)
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, warnings, apperr.Permanent(problem(keyErrorMissingURL, nil))
	}

	normalized, err := common.NormalizeURL(endpoint)
	if err != nil {
		return nil, warnings, apperr.Permanent(invalidURLProblem(endpoint, err))
	}
	key := queryKey(normalized)
	if key == "" {
		return nil, warnings, apperr.Permanent(problem(keyErrorMissingKey, nil))
	}

	rawType, warns, err := renderField(c.s.MsgType, "settings.msgType", msg.Vars, mode)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}
	warnings = append(warnings, warns...)
	msgType, err := normalizeMsgType(rawType)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}

	// 标题与正文已由调度层渲染, 这里只按企业微信的内容规则拼接与判空
	// 规格不 TrimSpace: 只含空白的正文按原样发送
	//
	// The scheduling layer has already rendered the title and the body, so this step only joins
	// and emptiness-checks them under the WeCom content rule
	// The specification does not trim: a whitespace-only body goes out as written
	content, contentWarns, err := buildContent(msg, msgType)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}
	warnings = append(warnings, contentWarns...)

	payload := sendPayload{MsgType: msgType}
	body := &contentPayload{Content: content}
	if msgType == msgTypeMarkdown {
		payload.Markdown = body
	} else {
		payload.Text = body
	}

	return &preparedChannel{
		endpoint: normalized,
		msgType:  msgType,
		key:      key,
		payload:  payload,
	}, warnings, nil
}

// Preview 返回脱敏后的预览, 只带消息类型与目标数
// URL 与 key 都不进入 Data
//
// Preview returns the redacted preview carrying the message type and the target count only
// Neither the URL nor the key reaches Data
func (p *preparedChannel) Preview() apperr.Problem {
	return apperr.Problem{
		ID: keyPreviewMessage,
		Data: map[string]any{
			"MsgType": p.msgType,
			"Targets": targetCount,
		},
	}
}

// Send 发送一次 POST 请求并校验响应
// 请求错误先经 common.SanitizeError, HTTP 状态与业务码由 common.VerifyErrcode 判定, errcode 必须为 0
// 业务码与协议错误按渠道键改写, 让 CLI 渲染 wecom 自己的文案; 永久性沿用 common 的判定
// 这里不 sleep 也不做重试计数, 等待与计数由调度层负责
//
// Send performs one POST request and verifies the response
// A request error passes through common.SanitizeError first, the HTTP status and the business code
// are judged by common.VerifyErrcode, and errcode has to be 0
// A business-code or protocol error is rewritten under the channel keys so the CLI renders wecom
// copy, while permanence keeps the verdict reached by common
// Nothing sleeps or counts retries here, since waiting and counting belong to the scheduling layer
func (p *preparedChannel) Send(ctx context.Context, client *http.Client) error {
	// nil client 是装配错误: DoJSON 里的 client.Do 会解引用空指针, 因此在这里显式返回错误
	// A nil client is an assembly error: the client.Do inside DoJSON dereferences a nil pointer, so
	// an explicit error comes back here instead
	if client == nil {
		return errors.New("wecom: nil HTTP client")
	}
	resp, err := common.DoJSON(ctx, client, http.MethodPost, p.endpoint, p.payload, nil)
	if err != nil {
		return common.SanitizeError(err)
	}
	defer resp.Body.Close()

	if err := common.VerifyErrcode(resp, 0); err != nil {
		return p.wrapVerifyError(err)
	}
	return nil
}

// wrapVerifyError 把 common.VerifyErrcode 的错误改写为 wecom 的消息键
// 业务码错误携带 Code 与 Message, 协议错误携带 Detail; 其它错误 (HTTP 状态/响应过大) 保持原键
// 服务端回显的文本先过 SanitizeString 与 key 遮蔽, 避免响应里的凭据进入 Data
//
// wrapVerifyError rewrites the errors of common.VerifyErrcode under the wecom keys
// A business-code error carries Code and Message, a protocol error carries Detail, and any other
// error (an HTTP status or an oversized response) keeps its original key
// Server-echoed text passes through SanitizeString and key masking, which keeps a credential in
// the response out of Data
func (p *preparedChannel) wrapVerifyError(err error) error {
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		return common.SanitizeError(err)
	}

	switch prob.ID {
	case "common.error.business_code":
		code, _ := prob.Data["Code"].(int)
		message, _ := prob.Data["Message"].(string)
		wrapped := &apperr.Problem{
			ID: keyErrorAPICode,
			Data: map[string]any{
				"Code":    code,
				"Message": p.maskServerText(message),
			},
			Cause: err,
		}
		if apperr.IsPermanent(err) {
			return apperr.Permanent(wrapped)
		}
		return wrapped
	case "common.error.bad_json", "common.error.bad_response":
		detail, _ := prob.Data["Detail"].(string)
		if detail == "" {
			detail = "the response body is empty or malformed"
		}
		return &apperr.Problem{
			ID: keyErrorBadResponse,
			Data: map[string]any{
				"Detail": p.maskServerText(detail),
			},
			Cause: err,
		}
	default:
		// 其它键 (HTTP 状态/响应过大) 保持原键, 但 Data 里的字符串先遮蔽服务端可能的 key 回显
		//
		// Any other key (an HTTP status or an oversized response) keeps its ID, while the string
		// values in Data are masked first in case the server echoed the key
		data := make(map[string]any, len(prob.Data))
		for key, value := range prob.Data {
			if text, ok := value.(string); ok {
				data[key] = p.maskServerText(text)
				continue
			}
			data[key] = value
		}
		wrapped := &apperr.Problem{
			ID:    prob.ID,
			Data:  data,
			Cause: common.SanitizeError(err),
		}
		if apperr.IsPermanent(err) {
			return apperr.Permanent(wrapped)
		}
		return wrapped
	}
}

// 接口实现检查: 编译期确认渠道与工厂满足契约
//
// Interface assertions: the compile-time checks confirm the channel and the factory satisfy the
// contract
var (
	_ channel.Channel  = (*wecomChannel)(nil)
	_ channel.Prepared = (*preparedChannel)(nil)
	_ channel.Factory  = factory{}
)
