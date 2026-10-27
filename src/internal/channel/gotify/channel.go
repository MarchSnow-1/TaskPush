package gotify

/*
Gotify 渠道的渠道实现: Channel 与 Prepared 的类型, Prepare, Send 与 resolvePriority
渲染与 /message 拼接在 render.go, 打码与 Problem 构造在 mask.go, 配置结构在 settings.go
Prepare 在这里把渲染结果装配成 JSON 请求体, 并把 token 写进 X-Gotify-Key 头

The channel implementation of the Gotify channel: the Channel and Prepared types, Prepare, Send and
resolvePriority
Rendering and the /message join sit in render.go, masking and Problem construction in mask.go, and
the configuration shapes in settings.go
Prepare is where the rendered results become a JSON request body and the token goes into the
X-Gotify-Key header
*/

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// gotifyChannel 是一个已配置的 gotify 渠道
//
// gotifyChannel is one configured gotify channel
type gotifyChannel struct {
	id string
	s  gotifySettings
}

// ID 返回渠道实例标识
//
// ID returns the channel instance identifier
func (c *gotifyChannel) ID() string { return c.id }

// Type 返回渠道类型名
//
// Type returns the channel type name
func (c *gotifyChannel) Type() string { return "gotify" }

// sendPayload 是 /message 的请求体
// title 用 omitempty, 因为标题为空时不发该字段; priority 用指针, 让显式配置的 0 与"未给出"区分开
//
// sendPayload is the /message request body
// title uses omitempty, since an empty title sends no such field, and priority is a pointer so a
// configured 0 stays apart from "not supplied"
type sendPayload struct {
	Message  string `json:"message"`
	Title    string `json:"title,omitempty"`
	Priority *int   `json:"priority,omitempty"`
}

// preparedChannel 是一次 Prepare 的产物, 可被调度层重复 Send
// req 不带正文, body 每次发送时重新挂上, 因此重试同一 Prepared 仍可读到请求体
// endpoint 是渲染与拼接后的消息端点, 只用于发送; host 是预览用的主机名
// token 用于遮蔽服务端回显文本, 不写进任何 Data
//
// preparedChannel is the outcome of one Prepare and can be sent repeatedly by the scheduling layer
// The stored request carries no body, and the bytes are attached again for each send, so a retry on
// the same Prepared still has a readable body
// endpoint is the rendered endpoint after the /message join and is used for sending only, while host
// is the host name the preview carries
// token masks a token echoed by the server and is never written into Data
type preparedChannel struct {
	req      *http.Request
	endpoint string
	host     string
	token    string
	body     []byte
}

// Prepare 渲染 settings, 校验地址, token 与内容并构造请求, 不做网络 I/O
// 返回的 warnings 覆盖未解析模板片段与正文截断
// 地址先规范化再拼 /message: path 去掉尾部斜杠后已以 /message 结尾就保持, 否则追加, query 与 fragment 不动
// token 渲染后必须非空且不含控制字符, 否则会写出非法请求头
// priority 的 0-10 判定在这里做, 因为模板要等到这里才有值
//
// Prepare renders the settings, validates the address, the token and the content, and builds the
// request without network I/O
// The returned warnings cover an unresolved template fragment and a truncated message
// The address is normalised first and then joined with /message: a path that ends in /message once
// its trailing slashes are dropped stays as it is, any other path gets /message, and the query and
// fragment are left alone
// A rendered token has to be non-empty and free of control characters, which would otherwise produce
// an illegal header
// The 0-10 priority check happens here, since a template only has a value at this point
func (c *gotifyChannel) Prepare(ctx context.Context, msg channel.Message) (channel.Prepared, []apperr.Problem, error) {
	mode := common.TemplateMode(msg.Mode)
	var warnings []apperr.Problem

	rawURL, warns, err := renderField(c.s.URL, settingsPathURL, msg.Vars, mode)
	if err != nil {
		return nil, nil, apperr.Permanent(err)
	}
	warnings = append(warnings, warns...)
	if strings.TrimSpace(rawURL) == "" {
		return nil, warnings, apperr.Permanent(problem(keyErrorMissingURL, nil))
	}
	normalized, err := common.NormalizeURL(rawURL)
	if err != nil {
		return nil, warnings, apperr.Permanent(invalidURLProblem(rawURL, err))
	}
	endpoint, host, err := messageEndpoint(normalized)
	if err != nil {
		return nil, warnings, apperr.Permanent(invalidURLProblem(rawURL, err))
	}

	token, warns, err := renderField(c.s.Token, settingsPathToken, msg.Vars, mode)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}
	warnings = append(warnings, warns...)
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, warnings, apperr.Permanent(problem(keyErrorMissingToken, nil))
	}
	// common.ValidHeaderValue 的拒绝集合与 net/http 写出请求头时的检查一致, 提前到这里能让
	// dry-run 与真实发送得出同一判定; CR/LF 一类字符也因此不会拖到 client.Do 才暴露
	//
	// The rejection set of common.ValidHeaderValue matches what net/http checks while writing a
	// header, and moving it here makes a dry-run and a real send reach the same verdict; a CR or LF
	// therefore surfaces here instead of waiting for client.Do
	if !common.ValidHeaderValue(token) {
		return nil, warnings, apperr.Permanent(problem(keyErrorInvalidToken, nil))
	}

	// 标题与正文已由调度层渲染完成, 这里只按 Gotify 的内容规则取值与判空:
	// 正文为空时用标题, 两者都空报错, 标题不拼进正文
	// 判空按空串判定, 只含空白的文本原样送出, 与模板层保持输入输出一致
	//
	// The scheduling layer has already rendered the title and the body, so this step only picks and
	// emptiness-checks them under the Gotify content rule: an empty body falls back to the title,
	// both empty is an error, and the title is never joined into the message
	// Emptiness means the empty string, so whitespace-only text goes out as it came in and the
	// template layer keeps its input and output alike
	message := msg.Body
	if message == "" {
		message = msg.Title
	}
	if message == "" {
		return nil, warnings, apperr.Permanent(problem(keyErrorEmptyContent, nil))
	}
	if clipped, truncated := common.TruncateRunes(message, messageLimitRunes); truncated {
		message = clipped
		warnings = append(warnings, apperr.Problem{
			ID:   keyWarnTruncated,
			Data: map[string]any{"Limit": messageLimitRunes},
		})
	}

	payload := sendPayload{Message: message}
	if msg.Title != "" {
		payload.Title = msg.Title
	}
	priority, hasPriority, warns, err := c.resolvePriority(msg.Vars, mode)
	warnings = append(warnings, warns...)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}
	if hasPriority {
		value := priority
		payload.Priority = &value
	}

	body, err := common.MarshalNoHTMLEscape(payload)
	if err != nil {
		return nil, warnings, common.SanitizeError(err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		// 到这里地址已通过校验, 失败只可能来自 method 或 URL 的非法 token, 仍按非法地址报
		//
		// The address has passed its checks by now, so a failure here comes from an illegal method or
		// URL token and is still reported as an invalid address
		return nil, warnings, apperr.Permanent(invalidURLProblem(rawURL, common.SanitizeError(err)))
	}
	req.Header.Set("Content-Type", contentTypeJSON)
	req.Header.Set(headerKey, token)

	return &preparedChannel{
		req:      req,
		endpoint: endpoint,
		host:     host,
		token:    token,
		body:     body,
	}, warnings, nil
}

// resolvePriority 得出最终优先级: 模板写法先渲染再按十进制解析, 数字写法直接用 Create 解析好的值
// 渲染后为空串表示该项没有取值, 按未给出处理, 因为模板变量为空的场景已由告警说明
// 非十进制写法与超出 0-10 都报 channel.gotify.error.invalid_priority, Value 取当时的文本
// 模板分支的渲染告警与错误一并返回, 调用方把它们合并进同一份 warnings
//
// resolvePriority settles the priority: a template spelling is rendered and then parsed as a decimal,
// while a number keeps the value Create already parsed
// An empty render means the item has no value and is treated as not supplied, since an empty template
// variable is already covered by the warning
// A non-decimal spelling and anything outside 0-10 report channel.gotify.error.invalid_priority with
// Value holding the text of that moment
// The template branch returns its render warnings and error as well, and the caller merges them into
// the same warnings slice
func (c *gotifyChannel) resolvePriority(vars *template.Vars, mode template.Mode) (int, bool, []apperr.Problem, error) {
	if !c.s.HasTemplate {
		if !c.s.HasPriority {
			return 0, false, nil, nil
		}
		if c.s.Priority < priorityMin || c.s.Priority > priorityMax {
			return 0, false, nil,
				problem(keyErrorInvalidPrior, map[string]any{"Value": strconv.Itoa(c.s.Priority)})
		}
		return c.s.Priority, true, nil, nil
	}

	rendered, warns, err := renderField(c.s.PriorityTemplate, settingsPathPriority, vars, mode)
	if err != nil {
		return 0, false, warns, err
	}
	trimmed := strings.TrimSpace(rendered)
	if trimmed == "" {
		return 0, false, warns, nil
	}
	value, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, false, warns, problem(keyErrorInvalidPrior, map[string]any{"Value": trimmed})
	}
	if value < priorityMin || value > priorityMax {
		return 0, false, warns, problem(keyErrorInvalidPrior, map[string]any{"Value": trimmed})
	}
	return value, true, warns, nil
}

// Preview 返回脱敏后的预览, 只带目标主机名与请求体字节数
// token, 请求头与完整地址都不进入 Data
//
// Preview returns the redacted preview carrying the target host name and the request body size
// Neither the token, a header nor the full address reaches Data
func (p *preparedChannel) Preview() apperr.Problem {
	return apperr.Problem{
		ID: keyPreviewMessage,
		Data: map[string]any{
			"Host":  p.host,
			"Bytes": len(p.body),
		},
	}
}

// Send 向 /message 端点 POST 一次请求体
// 状态码由 common.Check2xx 判定: 任意 2xx 成功 (含 204 与空 body), 4xx 走 Permanent, 429 与常规 5xx 保持可重试
// 非 2xx 的摘要取自 common.error.http_status 的 Body, 再按 gotify 的键重包一次并遮蔽 token
// 这里不 sleep 也不做重试计数, 等待与重试由调度层决定; 每次调用都重新挂上请求体
//
// Send posts the request body to the /message endpoint once
// common.Check2xx judges the status: any 2xx is a success (204 and an empty body included), a 4xx goes
// through Permanent, and 429 plus an ordinary 5xx stay retryable
// For a non-2xx status the summary of common.error.http_status is re-wrapped under the gotify key with
// the token masked
// Nothing sleeps or counts retries here, since waiting and counting belong to the scheduling layer,
// and every call attaches the body again
func (p *preparedChannel) Send(ctx context.Context, client *http.Client) error {
	if client == nil {
		return errors.New("gotify: nil HTTP client")
	}

	req := p.req.Clone(ctx)
	req.Body = io.NopCloser(bytes.NewReader(p.body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(p.body)), nil
	}
	req.ContentLength = int64(len(p.body))

	resp, err := common.DoRequest(ctx, client, req)
	if err != nil {
		// DoRequest 已经过 SanitizeError, Unwrap 链保留, 原始网络错误仍可被 errors.Is 取到
		//
		// DoRequest already ran SanitizeError and keeps the Unwrap chain, so errors.Is still reaches
		// the original network error
		return err
	}
	defer resp.Body.Close()

	if err := common.Check2xx(resp); err != nil {
		return p.httpStatusError(err)
	}
	return nil
}

// 接口实现检查: 编译期确认渠道与工厂满足契约
//
// Interface assertions: the compile-time checks confirm the channel and the factory satisfy the
// contract
var (
	_ channel.Channel  = (*gotifyChannel)(nil)
	_ channel.Prepared = (*preparedChannel)(nil)
	_ channel.Factory  = factory{}
)
