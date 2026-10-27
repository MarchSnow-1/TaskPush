package telegram

/*
telegram 渠道的渠道实现: Channel 与 Prepared 的类型, 每个 chat id 一次 POST 的发送循环,
多目标失败聚合与响应校验
渲染与字段路径改写在 render.go, 打码在 mask.go, 配置结构在 settings.go

The channel implementation of telegram: the Channel and Prepared types, the send loop of one POST
per chat id, the multi-target failure aggregation and the response verification
Rendering and field path rewriting sit in render.go, masking in mask.go, and the configuration
shapes in settings.go
*/

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
)

// telegramChannel 是一个已配置的 telegram 渠道
//
// telegramChannel is one configured telegram channel
type telegramChannel struct {
	id string
	s  telegramSettings
}

// ID 返回渠道实例标识
//
// ID returns the channel instance identifier
func (c *telegramChannel) ID() string { return c.id }

// Type 返回渠道类型名
//
// Type returns the channel type name
func (c *telegramChannel) Type() string { return "telegram" }

// target 是一个待发送的 chat: chatID 用于记录成功状态, body 是序列化后的请求体
//
// target is one chat to send to: chatID records the success state and body holds the serialised
// request body
type target struct {
	chatID string
	body   []byte
}

// sendPayload 是 sendMessage 的请求体
// disable_notification 只在 silent 为真时出现, 因此用 omitempty
//
// sendPayload is the sendMessage request body
// disable_notification shows up only when silent is true, which is what omitempty expresses
type sendPayload struct {
	ChatID              string `json:"chat_id"`
	Text                string `json:"text"`
	DisableNotification bool   `json:"disable_notification,omitempty"`
}

// preparedChannel 是一次 Prepare 的产物, 可被调度层重复 Send
// sent 记录已成功的 chat, 重试只处理剩余目标
//
// preparedChannel is the outcome of one Prepare and can be sent repeatedly by the scheduling layer
// sent records the chats that already succeeded, so a retry reaches only the remaining targets
type preparedChannel struct {
	endpoint string
	token    string
	targets  []target
	sent     map[string]bool
}

// Prepare 渲染 settings, 校验内容并构造请求体, 不做网络 I/O
// 返回的 warnings 覆盖模板未解析变量的告警与文本截断告警
// check 模式让未解析变量保留字面并继续: 保留下来的模板串随后仍要过 apiBase 与 token 的语义校验,
// 因此这两类字段在 check 下可能以 invalid_api_base 或 missing_token 结束; chatIds 与内容校验则照常推进
//
// Prepare renders the settings, validates the content and builds the request bodies without
// network I/O
// The returned warnings cover an unresolved template variable and a truncated text
// Check mode keeps an unresolved variable literal and continues, so the retained template text
// still faces the apiBase and token semantic checks and those fields can end in invalid_api_base or
// missing_token under check, while chatIds and the content checks proceed as usual
func (c *telegramChannel) Prepare(_ context.Context, msg channel.Message) (channel.Prepared, []apperr.Problem, error) {
	mode := common.TemplateMode(msg.Mode)

	apiBase, warns, err := renderField(c.s.APIBase, "settings.apiBase", msg.Vars, mode)
	if err != nil {
		return nil, nil, apperr.Permanent(err)
	}
	warnings := warns
	apiBase = strings.TrimSpace(apiBase)
	if apiBase == "" {
		apiBase = defaultAPIBase
	}
	normalizedBase, err := common.ValidateHTTPURL(apiBase, "apiBase")
	if err != nil {
		return nil, warnings, apperr.Permanent(invalidAPIBaseProblem(apiBase, err))
	}

	token, warns, err := renderField(c.s.Token, "settings.token", msg.Vars, mode)
	if err != nil {
		return nil, nil, apperr.Permanent(err)
	}
	warnings = append(warnings, warns...)
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, warnings, apperr.Permanent(problem(keyErrorMissingToken, nil))
	}
	escapedToken := url.PathEscape(token)

	chatIDs := make([]string, 0, len(c.s.ChatIDs))
	for i, configured := range c.s.ChatIDs {
		rendered, warns, err := renderField(configured, fmt.Sprintf("settings.chatIds.%d", i), msg.Vars, mode)
		if err != nil {
			return nil, warnings, apperr.Permanent(err)
		}
		warnings = append(warnings, warns...)
		if rendered = strings.TrimSpace(rendered); rendered == "" {
			continue
		}
		chatIDs = append(chatIDs, rendered)
	}
	if len(chatIDs) == 0 {
		return nil, warnings, apperr.Permanent(problem(keyErrorMissingChats, nil))
	}

	// 标题正文已由调度层渲染完成, 这里只按 Telegram 的内容规则拼接与判空
	//
	// The scheduling layer has already rendered the title and the body, so this step only joins
	// and emptiness-checks them under the content rule of Telegram
	text := common.JoinTitleBody(msg.Title, msg.Body)
	if text == "" {
		return nil, warnings, apperr.Permanent(problem(keyErrorEmptyContent, nil))
	}
	if clipped, truncated := common.TruncateUTF16(text, textLimitUTF16); truncated {
		text = clipped
		warnings = append(warnings, apperr.Problem{
			ID:   keyWarnTruncated,
			Data: map[string]any{"Limit": textLimitUTF16},
		})
	}

	endpoint := strings.TrimRight(normalizedBase, "/") + "/bot" + escapedToken + "/sendMessage"
	targets := make([]target, 0, len(chatIDs))
	for _, chatID := range chatIDs {
		// 请求体在这里一次构造完成: Send 只发送已构造的字节, 不再解析 settings
		//
		// The request body is built here once, so Send only sends the prepared bytes and never
		// re-reads the settings
		body, err := json.Marshal(sendPayload{
			ChatID:              chatID,
			Text:                text,
			DisableNotification: bool(c.s.Silent),
		})
		if err != nil {
			return nil, warnings, err
		}
		targets = append(targets, target{chatID: chatID, body: body})
	}

	return &preparedChannel{
		endpoint: endpoint,
		token:    token,
		targets:  targets,
		sent:     map[string]bool{},
	}, warnings, nil
}

// Preview 返回脱敏后的预览, 只带目标数量
// token, chat id 与正文都不进入 Data
//
// Preview returns the redacted preview carrying the target count only
// Neither the token, a chat id nor the text reaches Data
func (p *preparedChannel) Preview() apperr.Problem {
	return apperr.Problem{
		ID:   keyPreviewMessage,
		Data: map[string]any{"Targets": len(p.targets)},
	}
}

// Send 向尚未成功的 chat 逐个 POST, 成功的 chat 记入 sent
// 多目标聚合规则: 只有本轮全部失败目标都是永久错误时才返回 Permanent, 其它情况保持可重试
// 这里不 sleep 也不做重试计数, 等待与计数由调度层负责
//
// Send posts to every chat that has not succeeded yet, recording each success in sent
// The multi-target aggregation rule: Permanent comes back only when every failing target of this
// round is permanent, and the other cases stay retryable
// Nothing sleeps or counts retries here, since waiting and counting belong to the scheduling layer
func (p *preparedChannel) Send(ctx context.Context, client *http.Client) error {
	var failures []error
	attempted := 0
	for _, t := range p.targets {
		if p.sent[t.chatID] {
			continue
		}
		attempted++
		if err := p.sendOne(ctx, client, t); err != nil {
			failures = append(failures, err)
			continue
		}
		p.sent[t.chatID] = true
	}
	if len(failures) == 0 {
		return nil
	}
	if len(failures) == 1 {
		// 只有一个 chat 失败时直接返回它自己的错误: 键与永久性判定都更准确, 不需要聚合外壳
		//
		// With one failing chat its own error comes back as it is, since its key and its
		// permanence verdict are both more accurate than an aggregate shell
		return failures[0]
	}
	return aggregateFailures(failures, attempted)
}

// sendOne 发送一个 chat 的请求并校验响应
//
// sendOne sends the request of one chat and verifies the response
func (p *preparedChannel) sendOne(ctx context.Context, client *http.Client, t target) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(t.body))
	if err != nil {
		return common.SanitizeError(err)
	}
	req.Header.Set("Content-Type", contentTypeJSON)

	resp, err := common.DoRequest(ctx, client, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return p.verifyResponse(resp)
}

// responseEnvelope 是 sendMessage 的业务响应
// OK 用指针区分"字段缺失"与 ok=false: 缺字段属于协议错误, 与平台明确拒绝走不同的分支
//
// responseEnvelope is the business response of sendMessage
// OK is a pointer so a missing field stays apart from ok=false: a missing field is a protocol
// error, while an explicit false is a platform rejection on another branch
type responseEnvelope struct {
	OK          *bool  `json:"ok"`
	ErrorCode   *int   `json:"error_code"`
	Description string `json:"description"`
}

// verifyResponse 先按 HTTP 状态分类, 再解析业务字段
// 2xx 且 ok=true 视为成功; ok=false 按 error_code 分类; 坏 JSON, 空体与缺 ok 字段返回可重试的
// bad_response, 因为中间层可能返回一个错误页而端点本身仍然可用
//
// verifyResponse classifies the HTTP status first and then reads the business fields
// A 2xx with ok=true counts as success, while ok=false is classified through error_code; invalid
// JSON, an empty body and a missing ok field return the retryable bad_response, since a middle
// layer may answer with an error page while the real endpoint stays usable
func (p *preparedChannel) verifyResponse(resp *http.Response) error {
	if err := common.Check2xx(resp); err != nil {
		// 非 2xx 响应体由 common 读成摘要, 服务端可能在其中回显 token, 这里再遮蔽一次
		//
		// For a non-2xx status common reads the body into a summary, where the server may echo the
		// token, so the credential is masked once more
		return p.maskProblem(err)
	}
	body, err := common.ReadBody(resp)
	if err != nil {
		return p.maskProblem(err)
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return &apperr.Problem{
			ID:   keyErrorBadResponse,
			Data: map[string]any{"Detail": "empty response body"},
		}
	}

	var env responseEnvelope
	if err := json.Unmarshal(trimmed, &env); err != nil {
		return &apperr.Problem{
			ID:    keyErrorBadResponse,
			Data:  map[string]any{"Detail": p.maskServerText(err.Error())},
			Cause: common.SanitizeError(err),
		}
	}
	if env.OK == nil {
		return &apperr.Problem{
			ID:   keyErrorBadResponse,
			Data: map[string]any{"Detail": "missing ok field in response body"},
		}
	}
	if *env.OK {
		return nil
	}

	code := 0
	if env.ErrorCode != nil {
		code = *env.ErrorCode
	}
	description := strings.TrimSpace(env.Description)
	if description == "" {
		description = "ok=false"
	}
	rejection := &apperr.Problem{
		ID: keyErrorAPIError,
		Data: map[string]any{
			"Code":        code,
			"Description": p.maskServerText(description),
		},
	}
	if permanentErrorCode(code) {
		return apperr.Permanent(rejection)
	}
	return rejection
}

// permanentErrorCode 判断平台 error_code 是否属于重试不改变判定的区间
// 400/401/403 一类确定性拒绝返回 true; 429 与常规 5xx 返回 false, 留出限流窗口与恢复时间
// 缺少 error_code (0) 时按可重试处理, 因为响应没有给出判定依据
//
// permanentErrorCode reports whether a platform error_code sits in the range where a retry cannot
// change the verdict
// A deterministic rejection such as 400, 401 or 403 returns true, while 429 and an ordinary 5xx
// return false, which keeps the rate-limit window and the recovery time open
// A missing error_code, read as 0, counts as retryable because the response gives no verdict to
// stand on
func permanentErrorCode(code int) bool {
	switch {
	case code >= 300 && code < 400:
		return true
	case code >= 400 && code < 500:
		switch code {
		case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
			return false
		default:
			return true
		}
	default:
		return false
	}
}

// aggregateFailures 汇总本轮失败的 chat
// 全部失败目标都是永久错误时整体包成 Permanent; 混合或瞬时失败保持普通错误
// 聚合外壳固定用 channel.telegram.error.api_error: Code 取第一个失败的业务码, Description 放各
// 失败的脱敏摘要, 本轮尝试数放 Cause
// 摘要只含键名与业务码文本, 不含 chat id, token 与完整 URL, 内部永久标记也不留在可重试批次上
//
// aggregateFailures summarises the chats that failed in this round
// When every failing target is permanent the whole error is wrapped with Permanent, while a mixed
// or transient round stays a plain error
// The aggregate shell always uses channel.telegram.error.api_error: Code holds the first platform
// code, Description the redacted summary of each failure, and Cause the number of attempts
// A summary holds a message key and business code only, never a chat id, a token or a full URL, and
// no inner permanent marker stays on a retryable batch
func aggregateFailures(failures []error, attempted int) error {
	allPermanent := true
	summaries := make([]string, 0, len(failures))
	code := 0
	for _, err := range failures {
		if !apperr.IsPermanent(err) {
			allPermanent = false
		}
		if code == 0 {
			code = failureCode(err)
		}
		summaries = append(summaries, failureSummary(err))
	}

	aggregate := &apperr.Problem{
		ID: keyErrorAPIError,
		Data: map[string]any{
			"Code":        code,
			"Description": strings.Join(summaries, "; "),
		},
		Cause: fmt.Errorf("%d of %d chats failed this round", len(failures), attempted),
	}
	if allPermanent {
		return apperr.Permanent(aggregate)
	}
	return aggregate
}

// failureCode 从单个失败里取出平台业务码, 取不到时返回 0
// 失败来自 HTTP 状态或网络层时没有业务码, 聚合文案因此显示 0 并保留 Cause 里的判定
//
// failureCode reads the platform code out of one failure and returns 0 when there is none
// A failure from an HTTP status or the network layer carries no business code, so the aggregate
// copy shows 0 while Cause keeps the verdict
func failureCode(err error) int {
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		return 0
	}
	if code, ok := prob.Data["Code"].(int); ok {
		return code
	}
	return 0
}

// summaryLimitRunes 是聚合摘要里单个失败保留的最多字符数
//
// summaryLimitRunes is the most characters one failure keeps inside the aggregate summary
const summaryLimitRunes = 120

// failureSummary 把单个失败压成一行脱敏文本, 供聚合错误的 Data 使用
//
// failureSummary compresses one failure into a single redacted line for the Data of the aggregate
func failureSummary(err error) string {
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		return clipSummary(common.SanitizeString(err.Error()))
	}
	if code, ok := prob.Data["Code"].(int); ok {
		return clipSummary(fmt.Sprintf("%s code %d", prob.ID, code))
	}
	if status, ok := prob.Data["Status"]; ok {
		return clipSummary(fmt.Sprintf("%s status %v", prob.ID, status))
	}
	return clipSummary(common.SanitizeString(prob.ID))
}

// clipSummary 按 rune 把一行摘要截断, 避免聚合错误文本无上限增长
//
// clipSummary cuts one summary line by rune, which keeps the aggregate error text from growing
// without bound
func clipSummary(s string) string {
	if r := []rune(s); len(r) > summaryLimitRunes {
		return string(r[:summaryLimitRunes]) + "…"
	}
	return s
}

// 接口实现检查: 编译期确认渠道与工厂满足契约
//
// Interface assertions: the compile-time checks confirm the channel and the factory satisfy the
// contract
var (
	_ channel.Channel  = (*telegramChannel)(nil)
	_ channel.Prepared = (*preparedChannel)(nil)
	_ channel.Factory  = factory{}
)
