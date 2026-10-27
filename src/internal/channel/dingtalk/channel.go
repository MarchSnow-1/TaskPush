package dingtalk

/*
dingtalk 渠道的渠道实现: Channel 与 Prepared 的类型, Prepare 与 Send, 以及把校验错误改写为渠道键
内容构造与字段路径改写在 render.go, 加签与遮蔽在 mask.go, 配置结构在 settings.go

The channel implementation of dingtalk: the Channel and Prepared types, Prepare and Send, and the
rewriting of a verification error under the channel keys
Content construction and the field-path rewriting sit in render.go, signing and masking in mask.go,
and the configuration shapes in settings.go
*/

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
)

// dingtalkChannel 是一个已配置的 dingtalk 渠道
//
// dingtalkChannel is one configured dingtalk channel
type dingtalkChannel struct {
	id string
	s  dingtalkSettings
}

// ID 返回渠道实例标识
//
// ID returns the channel instance identifier
func (c *dingtalkChannel) ID() string { return c.id }

// Type 返回渠道类型名
//
// Type returns the channel type name
func (c *dingtalkChannel) Type() string { return "dingtalk" }

// sendPayload 是发送请求体
// msgtype 恒出现; text/markdown 二选一, at 只在配置了 atAll 时出现
//
// sendPayload is the request body
// msgtype is written on every message, exactly one of text and markdown is set, and at appears only
// when atAll is configured
type sendPayload struct {
	MsgType  string           `json:"msgtype"`
	Text     *textPayload     `json:"text,omitempty"`
	Markdown *markdownPayload `json:"markdown,omitempty"`
	At       *atPayload       `json:"at,omitempty"`
}

// preparedChannel 是一次 Prepare 的产物, 可被调度层重复 Send
// accessToken 保存规范化端点里 access_token 的取值, 供 maskServerText 遮蔽服务端的回显
//
// preparedChannel is the outcome of one Prepare and can be sent repeatedly by the scheduling layer
// accessToken keeps the value of the access_token query parameter from the normalised endpoint, which
// maskServerText hides when a server echoes it back
type preparedChannel struct {
	endpoint    string
	payload     []byte
	msgType     string
	secret      string
	accessToken string
}

// Prepare 渲染 settings, 校验 URL 与内容并构造请求体, 不做网络 I/O
// url/secret/msgType 都支持模板; URL 经 NormalizeURL 规范化并要求携带非空 access_token;
// text 用 JoinTitleBody 拼接, markdown 正文必填且标题缺省走 i18n 键;
// 内容按 4000 字节截断并发警告, atAll 只对 text 生效
//
// Prepare renders the settings, validates the URL and the content and builds the request body
// without network I/O
// url, secret and msgType all take templates; the URL is normalised by NormalizeURL and has to
// carry a non-empty access_token; text is joined by JoinTitleBody, a markdown body is required with
// the default title going through the i18n key, and the content is cut at 4000 bytes with a warning,
// while atAll applies to text only
func (c *dingtalkChannel) Prepare(_ context.Context, msg channel.Message) (channel.Prepared, []apperr.Problem, error) {
	mode := common.TemplateMode(msg.Mode)
	var warnings []apperr.Problem

	rawURL, warns, err := renderField(c.s.URL, "settings.url", msg.Vars, mode)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}
	warnings = append(warnings, warns...)
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, warnings, apperr.Permanent(problem(keyErrorMissingURL, nil))
	}
	endpoint, err := common.NormalizeURL(rawURL)
	if err != nil {
		return nil, warnings, apperr.Permanent(invalidURLProblem(rawURL, err))
	}
	if err := checkAccessToken(endpoint); err != nil {
		return nil, warnings, apperr.Permanent(err)
	}

	secret, warns, err := renderField(c.s.Secret, "settings.secret", msg.Vars, mode)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}
	warnings = append(warnings, warns...)
	secret = strings.TrimSpace(secret)

	// 加签时钟用 Message.Now: 固定时间向量因此可复现, Prepare 也不读 wall clock
	// The signing clock is Message.Now, so a fixed vector stays reproducible and this function reads
	// no wall clock
	if secret != "" {
		endpoint, err = signEndpoint(endpoint, secret, msg.Now)
		if err != nil {
			return nil, warnings, apperr.Permanent(invalidURLProblem(rawURL, err))
		}
	}

	renderedType, warns, err := renderField(c.s.MsgType, "settings.msgType", msg.Vars, mode)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}
	warnings = append(warnings, warns...)
	msgType, err := normalizeMsgType(renderedType)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}

	// atAll 是静态布尔设置, 不参与模板渲染; markdown 下设置它报错, 因为平台只在 text 里 @所有人
	//
	// atAll is a static boolean and takes no template rendering; setting it under markdown is an
	// error, since the platform mentions everyone in a text message alone
	atAll := c.s.atAllValue()
	if atAll && msgType == msgTypeMarkdown {
		return nil, warnings, apperr.Permanent(problem(keyErrorAtAllMarkdown, nil))
	}

	payload, warnings, err := buildPayload(msg, msgType, atAll, warnings)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}

	encoded, err := common.MarshalNoHTMLEscape(payload)
	if err != nil {
		return nil, warnings, err
	}
	return &preparedChannel{
		endpoint:    endpoint,
		payload:     encoded,
		msgType:     msgType,
		secret:      secret,
		accessToken: accessTokenOf(endpoint),
	}, warnings, nil
}

// Preview 返回脱敏后的预览, 只带消息类型
// URL, access_token 与 secret 都不进入 Data
//
// Preview returns the redacted preview carrying the message type only
// Data holds MsgType and Targets, so the URL, access_token and secret have no field to land in
func (p *preparedChannel) Preview() apperr.Problem {
	return apperr.Problem{
		ID:   keyPreviewMessage,
		Data: map[string]any{"MsgType": p.msgType, "Targets": 1},
	}
}

// Send 发送一次请求并校验 errcode==0
// 请求错误先经 common.SanitizeError; HTTP 状态与业务码由 common.VerifyErrcode 判定,
// errcode 非 0 的错误改写成渠道键, 服务端回显的文本再遮蔽一次 secret
// 这里不 sleep 也不做重试计数, 等待与计数由调度层负责
//
// Send performs one request and verifies errcode==0
// A request error passes through common.SanitizeError first, while the HTTP status and the business
// code are judged by common.VerifyErrcode, and an errcode other than 0 is rewritten under the
// channel keys with server-echoed text masked against the secret once more
// Nothing sleeps or counts retries here, since waiting and counting belong to the scheduling layer
func (p *preparedChannel) Send(ctx context.Context, client *http.Client) error {
	// nil client 是装配错误: client.Do 会解引用空指针, 因此在这里显式返回错误
	// A nil client is an assembly error: client.Do dereferences a nil pointer, so an explicit error
	// comes back here instead
	if client == nil {
		return errors.New("dingtalk: nil HTTP client")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(p.payload))
	if err != nil {
		return common.SanitizeError(err)
	}
	req.Header.Set("Content-Type", contentTypeJSON)
	resp, err := common.DoRequest(ctx, client, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return p.wrapVerifyError(common.VerifyErrcode(resp, 0))
}

// wrapVerifyError 把 common.VerifyErrcode 的错误改写为 dingtalk 的消息键
// 业务码错误携带 Code 与 Message, 协议错误携带 Detail; 其它错误 (HTTP 状态/响应过大) 保持原键
// 服务端回显的文本先过 SanitizeString 与 secret 遮蔽, 避免响应里的凭据进入 Data
//
// wrapVerifyError rewrites the errors of common.VerifyErrcode under the dingtalk keys
// A business-code error carries Code and Message, a protocol error carries Detail, and any other
// error (an HTTP status or an oversized response) keeps its original key
// Server-echoed text passes through SanitizeString and secret masking, which keeps a credential in
// the response out of Data
func (p *preparedChannel) wrapVerifyError(err error) error {
	if err == nil {
		return nil
	}
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
		// 其它键 (HTTP 状态/响应过大) 保持原键, 但 Data 里的字符串先遮蔽服务端可能的回显
		// Cause 置 nil: 内层 Problem 放进 Cause 会让渲染层把 "common.error.http_status"
		// 这类内部键追加到用户文案尾部
		//
		// Any other key (an HTTP status or an oversized response) keeps its ID, while the string
		// values in Data are masked first in case the server echoed a secret
		// Cause is nil: putting the inner Problem there would make the rendering layer append an
		// internal key such as "common.error.http_status" to the user copy
		data := make(map[string]any, len(prob.Data))
		for key, value := range prob.Data {
			if text, ok := value.(string); ok {
				data[key] = p.maskServerText(text)
				continue
			}
			data[key] = value
		}
		wrapped := &apperr.Problem{
			ID:   prob.ID,
			Data: data,
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
	_ channel.Channel  = (*dingtalkChannel)(nil)
	_ channel.Prepared = (*preparedChannel)(nil)
	_ channel.Factory  = factory{}
)
