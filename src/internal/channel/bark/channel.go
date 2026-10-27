package bark

/*
bark 渠道的渠道实现: Channel 与 Prepared 的类型, Prepare 与 Send, 单设备与批量两条发送路径
单设备走 {server}/{token}, 多设备走 {server}/push, 请求体为 JSON
Prepare 完成渲染, 校验与请求体构造, 不发网络; token 与 device key 只进入请求, 不进入错误与预览
批量推送逐设备校验 data[].code, 已成功设备记进 Prepared, 重试只补失败设备; 响应没有 device_key 时无法收窄, 下一轮按配置全量重发并告警
渲染与请求体字段写入在 render.go, 打码与 Problem 构造在 mask.go, 配置结构在 settings.go

The channel implementation of bark: the Channel and Prepared types, Prepare and Send, and the two
send paths for a single device and a batch
One device goes through {server}/{token} and several devices through {server}/push, with a JSON body
Prepare renders, validates and builds the request body without touching the network, and the token
and the device keys stay inside the request rather than inside an error or a preview
A batch push verifies data[].code device by device and records the succeeded devices inside
Prepared, so a retry reaches only the failures; when the response carries no device_key the range
cannot be narrowed, and the next round resends to the configured set with a warning
Rendering and request-body field writing sit in render.go, masking and Problem building in mask.go,
and the configuration shapes in settings.go
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

// barkChannel 是一个已配置的 bark 渠道
//
// barkChannel is one configured bark channel
type barkChannel struct {
	id string
	s  barkSettings
}

// ID 返回渠道实例标识
//
// ID returns the channel instance identifier
func (c *barkChannel) ID() string { return c.id }

// Type 返回渠道类型名
//
// Type returns the channel type name
func (c *barkChannel) Type() string { return "bark" }

// barkPayload 是发送请求体
// 可选字段全部用 omitempty, 因为渲染后为空的字段不发送; badge/ttl/autoCopy 用指针,
// 让 0/false 这些合法取值与"未给出"区分开
//
// barkPayload is the request body
// Every optional field uses omitempty, since a field that renders empty is not sent, while badge,
// ttl and autoCopy are pointers so a legal 0 or false stays apart from "not supplied"
type barkPayload struct {
	DeviceKeys []string `json:"device_keys,omitempty"`
	Title      string   `json:"title,omitempty"`
	Body       string   `json:"body,omitempty"`
	Subtitle   string   `json:"subtitle,omitempty"`
	Group      string   `json:"group,omitempty"`
	Level      string   `json:"level,omitempty"`
	Sound      string   `json:"sound,omitempty"`
	Icon       string   `json:"icon,omitempty"`
	Image      string   `json:"image,omitempty"`
	ClickURL   string   `json:"url,omitempty"`
	Copy       string   `json:"copy,omitempty"`
	Badge      *int     `json:"badge,omitempty"`
	TTL        *int     `json:"ttl,omitempty"`
	AutoCopy   *int     `json:"autoCopy,omitempty"`
}

// preparedChannel 是一次 Prepare 的产物, 可被调度层重复 Send
// pending 保存本轮仍待发送的设备, 每轮只对它发送, 因此已成功的设备不会被重复轰炸
// warns 收集 Send 阶段发现的警告, 因为 Prepared.Send 只能返回 error, 没有别的通道向上传递
//
// preparedChannel is the outcome of one Prepare and can be sent repeatedly by the scheduling layer
// pending holds the devices still outstanding this round, and each round reaches only them, so a
// device that already succeeded is not pushed a second time
// warns collects the warnings discovered while sending, since Prepared.Send can only return an
// error and offers no other way up
type preparedChannel struct {
	endpoint string
	body     []byte
	batch    bool
	devices  []string
	pending  []string
	secrets  []string
	done     bool
	warns    []apperr.Problem

	// 批量重试时重建请求体所需的字段: Send 不重新读取 settings, 设备列表之外的字段原样复用
	//
	// The fields needed to rebuild the body for a batch retry: Send never re-reads the settings,
	// and every field other than the device list is reused as it stands
	title    string
	bodyText string
	subtitle string
	group    string
	level    string
	sound    string
	icon     string
	image    string
	clickURL string
	copy     string
	badge    *int
	ttl      *int
	autoCopy *int
}

// Prepare 渲染 settings, 校验内容并构造请求体, 不做网络 I/O
// 标题与正文至少一个非空; token/tokens 渲染后不能为空; icon/image/clickUrl 按 http/https 校验
// 标题与正文不做渠道级截断, 长度只由模板层的 64KB 上限约束; 严格模式下超限在调度层渲染报错,
// check 模式由模板层截断并告警
// 返回的 warnings 覆盖 settings 字段的未解析模板片段
//
// Prepare renders the settings, validates the content and builds the request body without network
// I/O
// At least one of the title and the body is required, token or tokens must not render empty, and
// icon, image and clickUrl are checked as http or https
// The title and the body are not truncated by the channel; their length is bounded by the
// template-layer 64KB cap alone, which reports an oversized strict render through the scheduling
// layer and cuts with a warning in check mode
// The returned warnings cover an unresolved template fragment in a settings field
func (c *barkChannel) Prepare(_ context.Context, msg channel.Message) (channel.Prepared, []apperr.Problem, error) {
	mode := common.TemplateMode(msg.Mode)
	var warnings []apperr.Problem

	server, warns, err := renderField(c.s.Server, "settings.server", msg.Vars, mode)
	if err != nil {
		return nil, nil, apperr.Permanent(err)
	}
	warnings = append(warnings, warns...)
	server = strings.TrimSpace(server)
	if server == "" {
		server = defaultServer
	}
	normalizedServer, err := common.ValidateHTTPURL(server, "server")
	if err != nil {
		return nil, warnings, apperr.Permanent(invalidServerProblem(server, err))
	}
	normalizedServer = strings.TrimRight(normalizedServer, "/")

	batch := len(c.s.Tokens) > 0
	var token string
	var devices []string
	if batch {
		// tokens 逐项渲染并去空: 只含空白或只渲染出空白的项不参与批量推送
		//
		// tokens are rendered one by one and stripped of empties: an item that is whitespace only
		// or renders to whitespace takes no part in the batch push
		devices = make([]string, 0, len(c.s.Tokens))
		for i, configured := range c.s.Tokens {
			rendered, itemWarns, err := renderField(configured, fmt.Sprintf("settings.tokens.%d", i), msg.Vars, mode)
			if err != nil {
				return nil, warnings, apperr.Permanent(err)
			}
			warnings = append(warnings, itemWarns...)
			if rendered = strings.TrimSpace(rendered); rendered == "" {
				continue
			}
			devices = append(devices, rendered)
		}
		if len(devices) == 0 {
			return nil, warnings, apperr.Permanent(problem(keyErrorTokenOrTokens, nil))
		}
	} else {
		rendered, tokenWarns, err := renderField(c.s.Token, "settings.token", msg.Vars, mode)
		if err != nil {
			return nil, warnings, apperr.Permanent(err)
		}
		warnings = append(warnings, tokenWarns...)
		token = strings.TrimSpace(rendered)
		if token == "" {
			return nil, warnings, apperr.Permanent(problem(keyErrorTokenOrTokens, nil))
		}
	}

	title := msg.Title
	body := msg.Body
	if strings.TrimSpace(title) == "" && strings.TrimSpace(body) == "" {
		return nil, warnings, apperr.Permanent(problem(keyErrorEmptyContent, nil))
	}

	payload := &barkPayload{Title: title, Body: body}
	if batch {
		payload.DeviceKeys = devices
	}

	optional := []struct {
		key   string
		field string
		value string
	}{
		{"subtitle", "settings.subtitle", c.s.Subtitle},
		{"group", "settings.group", c.s.Group},
		{"level", "settings.level", c.s.Level},
		{"sound", "settings.sound", c.s.Sound},
		{"copy", "settings.copy", c.s.Copy},
	}
	for _, field := range optional {
		rendered, fieldWarns, err := renderField(field.value, field.field, msg.Vars, mode)
		if err != nil {
			return nil, warnings, apperr.Permanent(err)
		}
		warnings = append(warnings, fieldWarns...)
		if rendered = strings.TrimSpace(rendered); rendered != "" {
			setOptional(payload, field.key, rendered)
		}
	}

	urlFields := []struct {
		field   string
		payload string
		value   string
	}{
		{"settings.icon", "icon", c.s.Icon},
		{"settings.image", "image", c.s.Image},
		{"settings.clickUrl", "clickUrl", c.s.ClickURL},
	}
	for _, field := range urlFields {
		rendered, fieldWarns, err := renderField(field.value, field.field, msg.Vars, mode)
		if err != nil {
			return nil, warnings, apperr.Permanent(err)
		}
		warnings = append(warnings, fieldWarns...)
		rendered = strings.TrimSpace(rendered)
		if rendered == "" {
			continue
		}
		normalized, err := common.ValidateHTTPURL(rendered, field.field)
		if err != nil {
			return nil, warnings, apperr.Permanent(invalidURLProblem(field.field, rendered, err))
		}
		setOptional(payload, field.payload, normalized)
	}

	if c.s.HasBadge {
		badge := c.s.Badge
		payload.Badge = &badge
	}
	if c.s.HasTTL {
		ttl := c.s.TTL
		payload.TTL = &ttl
	}
	if c.s.HasAutoCopy {
		autoCopy := 0
		if c.s.AutoCopy {
			autoCopy = 1
		}
		payload.AutoCopy = &autoCopy
	}

	// 请求体在这里一次构造完成: Send 只发送已构造的字节, 不再读取 settings
	//
	// The request body is built here once, so Send only sends the prepared bytes and never reads
	// the settings again
	encoded, err := common.MarshalNoHTMLEscape(payload)
	if err != nil {
		return nil, warnings, err
	}

	endpoint := normalizedServer + "/push"
	if !batch {
		// token 里的 "/" 属于群组 key 的写法, 转义时保留; 其余危险字符按路径规则转义
		//
		// A "/" inside the token is part of how a group key is written and stays, while the other
		// dangerous characters are escaped by the path rule
		endpoint = normalizedServer + "/" + strings.ReplaceAll(url.PathEscape(token), "%2F", "/")
	}

	secrets := append([]string(nil), devices...)
	if token != "" {
		secrets = append(secrets, token)
	}

	prepared := &preparedChannel{
		endpoint: endpoint,
		body:     encoded,
		batch:    batch,
		devices:  append([]string(nil), devices...),
		secrets:  secrets,
		title:    payload.Title,
		bodyText: payload.Body,
		subtitle: payload.Subtitle,
		group:    payload.Group,
		level:    payload.Level,
		sound:    payload.Sound,
		icon:     payload.Icon,
		image:    payload.Image,
		clickURL: payload.ClickURL,
		copy:     payload.Copy,
		badge:    payload.Badge,
		ttl:      payload.TTL,
		autoCopy: payload.AutoCopy,
	}
	if batch {
		prepared.pending = append([]string(nil), devices...)
	}
	return prepared, warnings, nil
}

// Preview 返回脱敏后的预览, 只带目标数量
// token 与 device key 都不进入 Data
//
// Preview returns the redacted preview carrying the target count only
// Neither the token nor a device key reaches Data
func (p *preparedChannel) Preview() apperr.Problem {
	if !p.batch {
		return apperr.Problem{ID: keyPreviewSingle, Data: map[string]any{"Targets": 1}}
	}
	return apperr.Problem{ID: keyPreviewBatch, Data: map[string]any{"Targets": len(p.devices)}}
}

// Warnings 返回 Send 阶段发现的警告副本
// channel.Prepared 只有 Send 与 Preview 两个方法, 这些警告没有别的上送通道, 调度层需要时可用
// 类型断言读取
//
// Warnings returns a copy of the warnings discovered while sending
// channel.Prepared has only Send and Preview, so these warnings have no other way up and the
// scheduling layer may read them through a type assertion
func (p *preparedChannel) Warnings() []apperr.Problem {
	out := make([]apperr.Problem, len(p.warns))
	copy(out, p.warns)
	return out
}

// Send 执行一次发送
// 单设备发送校验顶层 code; 批量发送逐设备校验并只重发仍待处理的设备
// 这里不 sleep 也不做重试计数, 等待与计数由调度层负责
//
// Send performs one send
// A single-device send checks the top-level code, while a batch send verifies device by device and
// reaches only the devices still outstanding
// Nothing sleeps or counts retries here, since waiting and counting belong to the scheduling layer
func (p *preparedChannel) Send(ctx context.Context, client *http.Client) error {
	if p.batch {
		return p.sendBatch(ctx, client)
	}
	return p.sendSingle(ctx, client)
}

// sendSingle 发送单设备请求并校验 {"code":200}
//
// sendSingle sends the single-device request and checks {"code":200}
func (p *preparedChannel) sendSingle(ctx context.Context, client *http.Client) error {
	if p.done {
		return nil
	}
	resp, err := p.post(ctx, client, p.body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := common.Check2xx(resp); err != nil {
		return p.maskStatusError(err)
	}
	data, err := common.ReadBody(resp)
	if err != nil {
		// 响应体超过 1MB 时无法逐字段校验, 按已送达处理而不是反复重试同一个大响应
		//
		// A body beyond 1MB cannot be verified field by field, so it counts as delivered instead of
		// retrying the same large response over and over
		if isBodyTooLarge(err) {
			p.done = true
			return nil
		}
		return err
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return badResponseProblem("empty response body")
	}
	var env struct {
		Code    *int   `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(trimmed, &env); err != nil {
		return badJSONProblem(err)
	}
	if env.Code == nil {
		return badResponseProblem("missing code field in response body")
	}
	if *env.Code != 200 {
		return p.apiCodeError(*env.Code, env.Message)
	}
	p.done = true
	return nil
}

// deviceResult 是批量响应里一台设备的结果
// Code 用指针区分"字段缺失"与 0: 缺字段属于协议异常, 与平台明确拒绝走不同分支
//
// deviceResult is one device result inside a batch response
// Code is a pointer so a missing field stays apart from 0: a missing field is a protocol anomaly,
// while an explicit value is a platform rejection on another branch
type deviceResult struct {
	Code      *int   `json:"code"`
	DeviceKey string `json:"device_key"`
	Message   string `json:"message"`
}

// parseBatchResponse 解析批量响应, 兼容 {"code":200,"data":[...]} 与裸数组两种形态
// bare 表示响应本身就是一个数组, 这种部署没有顶层 code, 成败只由逐设备结果决定
// present 表示响应是否真的带来逐设备结果, 空数组算作没有
//
// parseBatchResponse parses a batch response and accepts both the {"code":200,"data":[...]} shape
// and a bare array
// bare says the response is an array itself, which is the deployment shape that has no top-level
// code and leaves the verdict to the per-device results
// present says whether the response really carried per-device results, where an empty array counts
// as none
func parseBatchResponse(data []byte) (topCode *int, topMessage string, results []deviceResult, bare, present bool, err error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, "", nil, false, false, badResponseProblem("empty response body")
	}
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &results); err != nil {
			return nil, "", nil, false, false, badJSONProblem(err)
		}
		return nil, "", results, true, len(results) > 0, nil
	}
	var env struct {
		Code    *int           `json:"code"`
		Message string         `json:"message"`
		Data    []deviceResult `json:"data"`
	}
	if err := json.Unmarshal(trimmed, &env); err != nil {
		return nil, "", nil, false, false, badJSONProblem(err)
	}
	return env.Code, env.Message, env.Data, false, len(env.Data) > 0, nil
}

// sendBatch 发送批量请求并逐设备校验
// 顶层 code 非 200 时整体失败, 待发送设备保持不变; data 缺失时按成功处理但记下未校验告警;
// 有 device_key 的失败设备留进 pending, 响应完全不带 device_key 时下一轮按配置全量重发
//
// sendBatch sends the batch request and verifies it device by device
// A top-level code other than 200 fails the whole batch and leaves the outstanding devices as they
// were; a missing data field counts as success but records the unverified warning; a failure with a
// device_key stays in pending, and a response carrying no device_key at all means the next round
// resends to the configured set
func (p *preparedChannel) sendBatch(ctx context.Context, client *http.Client) error {
	if len(p.pending) == 0 {
		return nil
	}
	attempted := append([]string(nil), p.pending...)

	// 只补发待处理设备: 请求体在这里重建, 已成功设备不再出现在 device_keys 里
	//
	// Only the outstanding devices are retried: the body is rebuilt here, so a succeeded device
	// no longer appears inside device_keys
	body, err := common.MarshalNoHTMLEscape(&barkPayload{
		DeviceKeys: attempted,
		Title:      p.title,
		Body:       p.bodyText,
		Subtitle:   p.subtitle,
		Group:      p.group,
		Level:      p.level,
		Sound:      p.sound,
		Icon:       p.icon,
		Image:      p.image,
		ClickURL:   p.clickURL,
		Copy:       p.copy,
		Badge:      p.badge,
		TTL:        p.ttl,
		AutoCopy:   p.autoCopy,
	})
	if err != nil {
		return err
	}

	resp, err := p.post(ctx, client, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := common.Check2xx(resp); err != nil {
		return p.maskStatusError(err)
	}
	data, err := common.ReadBody(resp)
	if err != nil {
		if isBodyTooLarge(err) {
			// 大响应无法逐设备校验, 与"没有 data"同处理: 按成功放行并记下未校验告警
			//
			// A large response cannot be verified device by device, so it follows the "no data"
			// branch: pass as success and record the unverified warning
			p.pending = nil
			p.addWarning(keyWarnBatchUnverified, nil)
			return nil
		}
		return err
	}

	topCode, topMessage, results, bare, present, err := parseBatchResponse(data)
	if err != nil {
		return err
	}
	// 裸数组响应没有顶层 code, 留到逐设备结果里判定
	// A bare-array response has no top-level code, so the verdict comes from the per-device results
	if !bare {
		if topCode == nil {
			return badResponseProblem("missing code field in response body")
		}
		if *topCode != 200 {
			return p.apiCodeError(*topCode, topMessage)
		}
	}
	if !present {
		// 顶层 200 但没有逐设备结果: 无法确认各设备是否收到, 按成功处理但必须留下告警
		//
		// A top-level 200 without per-device results leaves delivery unconfirmed, so it counts as
		// success but has to leave the warning behind
		p.pending = nil
		p.addWarning(keyWarnBatchUnverified, nil)
		return nil
	}

	succeeded := map[string]bool{}
	var failures []deviceResult
	keyedResults := 0
	unmappable := false
	for _, result := range results {
		if result.DeviceKey == "" {
			// 没有 device_key 的结果映射不回任何设备: 成功的结果无法记为已送达, 失败的结果
			// 也无法定位, 两种都让本轮收窄不了范围
			//
			// A result without a device_key maps back to no device: a successful one cannot be
			// recorded as delivered and a failing one cannot be located, so either way this round
			// cannot narrow the range
			unmappable = true
			if result.Code == nil || *result.Code != 200 {
				failures = append(failures, result)
			}
			continue
		}
		keyedResults++
		// code 缺失读作 0, 于是它落进失败分支: 缺失不等于成功
		//
		// A missing code reads as 0, which lands in the failure branch: an absent code is not a
		// success
		code := 0
		if result.Code != nil {
			code = *result.Code
		}
		if code == 200 {
			succeeded[result.DeviceKey] = true
			continue
		}
		failures = append(failures, result)
	}

	// 每个结果都带 device_key 才能把成败映射回设备; 否则收窄不了范围, 下一轮只能按配置全量重发
	//
	// Only when every result carries a device_key can an outcome be mapped back to a device;
	// otherwise the range cannot be narrowed and the next round has to resend to the configured set
	verified := !unmappable && keyedResults == len(results)
	if !verified {
		p.pending = append([]string(nil), p.devices...)
		p.addWarning(keyWarnNoDeviceKey, map[string]any{"Count": len(p.devices)})
	} else {
		p.pending = p.pending[:0]
		for _, device := range attempted {
			if !succeeded[device] {
				p.pending = append(p.pending, device)
			}
		}
	}

	if len(failures) == 0 {
		return nil
	}

	// 聚合规则: 只有每个失败设备都拿到永久码, 且设备成败都能映射回具体设备时, 整体才标记永久;
	// 结果映射不回设备时无法确定哪些设备失败, 因此保持可重试
	//
	// The aggregation rule: the round counts as permanent only when every failing device carries a
	// permanent code and every outcome maps back to a device; when an outcome maps back to no
	// device, which devices failed is unknown, so the round stays retryable
	allPermanent := verified
	summaries := make([]string, 0, len(failures))
	for i, failure := range failures {
		code := 0
		if failure.Code != nil {
			code = *failure.Code
		}
		if !permanentBusinessCode(code) {
			allPermanent = false
		}
		// 摘要只用序号与业务码: device key 属于凭据, 不进入错误 Data
		//
		// The summary carries an index and a business code only: a device key is a credential and
		// never reaches the error Data
		summary := fmt.Sprintf("device #%d code %d", i+1, code)
		if message := strings.TrimSpace(failure.Message); message != "" {
			summary += ": " + p.sanitize(message)
		}
		summaries = append(summaries, summary)
	}

	aggregate := &apperr.Problem{
		ID: keyErrorBatchFailed,
		Data: map[string]any{
			"Failed": len(failures),
			"Total":  len(attempted),
			"Detail": strings.Join(summaries, "; "),
		},
	}
	if allPermanent {
		return apperr.Permanent(aggregate)
	}
	return aggregate
}

// post 发送已经构造好的请求体, 附带 JSON 媒体类型
// 批量重试会传入只含待处理设备的请求体, 单设备路径传入 Prepare 阶段构造的原始字节
//
// post sends the request body built earlier with the JSON media type
// A batch retry passes the body holding only the outstanding devices, while the single path passes
// the bytes built during Prepare
func (p *preparedChannel) post(ctx context.Context, client *http.Client, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, common.SanitizeError(err)
	}
	req.Header.Set("Content-Type", contentTypeJSON)
	return common.DoRequest(ctx, client, req)
}

// addWarning 追加一条 Send 阶段警告
//
// addWarning appends one send-time warning
func (p *preparedChannel) addWarning(id string, data map[string]any) {
	p.warns = append(p.warns, apperr.Problem{ID: id, Data: data})
}

// apiCodeError 构造顶层业务码错误, 并按业务码分类永久性
//
// apiCodeError builds the top-level business-code error and classifies its permanence
func (p *preparedChannel) apiCodeError(code int, message string) error {
	outcome := &apperr.Problem{
		ID: keyErrorAPICode,
		Data: map[string]any{
			"Code":    code,
			"Message": p.sanitize(strings.TrimSpace(message)),
		},
	}
	if permanentBusinessCode(code) {
		return apperr.Permanent(outcome)
	}
	return outcome
}

// sanitize 清洗服务端回显文本: 先按公共规则给地址打码, 再遮蔽本渠道的 token 与 device key
//
// sanitize cleans text echoed by the server: addresses are masked by the shared rule first, and
// then the token and the device keys of this channel are hidden
func (p *preparedChannel) sanitize(text string) string {
	return common.MaskInText(text, p.secrets...)
}

// maskStatusError 把 common.Check2xx 的状态码错误里的响应体摘要再遮蔽一次
// 单设备发送时 token 在 path 里, 批量发送时 device key 在请求体里, 服务端都可能把它们
// 回显在错误响应体里, 而 common 的摘要只按 scheme://... 形态打码, 裸凭据会原样留下
// 重包时 ID 沿用 common.error.http_status, Data 保留 Status 与处理后的 Body, Cause 置 nil:
// 内层 Problem 放进 Cause 会让渲染层把 "common.error.http_status" 这个内部键追加到用户文案尾部
// 永久性沿用原判定, 因此 4xx 与 3xx 仍然不可重试
// 不是 Problem 的错误走这个兜底分支, 经 SanitizeError 返回, 保持原有的脱敏
//
// maskStatusError masks the response-body summary inside the status-code error of common.Check2xx a
// second time
// A single-device send keeps the token in the path while a batch send keeps the device keys in the
// request body, and the server may echo either inside an error body, while the common summary masks
// the scheme://... form alone and leaves a bare credential as it is
// The re-wrap keeps the ID common.error.http_status, keeps Status and the processed Body in Data, and
// sets Cause to nil: putting the inner Problem into Cause would make the rendering layer append the
// internal key "common.error.http_status" to the user copy
// Permanence follows the original verdict, so a 4xx and a 3xx stay non-retryable
// An error that is not a Problem takes this fallback branch and returns through SanitizeError, which
// keeps its existing redaction
func (p *preparedChannel) maskStatusError(err error) error {
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		return common.SanitizeError(err)
	}
	body, _ := prob.Data["Body"].(string)
	wrapped := &apperr.Problem{
		ID: "common.error.http_status",
		Data: map[string]any{
			"Status": prob.Data["Status"],
			"Body":   p.sanitize(body),
		},
	}
	if apperr.IsPermanent(err) {
		return apperr.Permanent(wrapped)
	}
	return wrapped
}

// permanentBusinessCode 判断平台业务码是否属于重试不改变判定的区间
// 3xx 与 4xx (408/425/429 除外) 返回 true; 501/505/511 返回 true; 其余 5xx 与未知区间返回 false
//
// permanentBusinessCode reports whether a platform business code sits in the range where a retry
// cannot change the verdict
// A 3xx and a 4xx other than 408, 425 and 429 return true, 501, 505 and 511 return true, and the
// remaining 5xx and unknown ranges return false
func permanentBusinessCode(code int) bool {
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
	case code >= 500 && code < 600:
		switch code {
		case http.StatusNotImplemented, http.StatusHTTPVersionNotSupported, http.StatusNetworkAuthenticationRequired:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

// isBodyTooLarge 判断错误是否为公共层的响应体超限
//
// isBodyTooLarge reports whether the error is the shared response-body cap
func isBodyTooLarge(err error) bool {
	var prob *apperr.Problem
	return errors.As(err, &prob) && prob.ID == "common.error.body_too_large"
}

// 接口实现检查: 编译期确认渠道与工厂满足契约
//
// Interface assertions: the compile-time checks confirm the channel and the factory satisfy the
// contract
var (
	_ channel.Channel  = (*barkChannel)(nil)
	_ channel.Prepared = (*preparedChannel)(nil)
	_ channel.Factory  = factory{}
)
