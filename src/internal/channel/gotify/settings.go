package gotify

/*
Gotify 渠道的配置结构, 消息键与工厂: settings 的两种形态, priority 的三条解析分支, 严格解码与静态校验
Prepare 与 Send 在 channel.go, 渲染与 /message 拼接在 render.go, 打码与 Problem 构造在 mask.go
token 与完整地址都只进入请求, 因此这里的静态校验只判形态, 不打印取值

The configuration of the Gotify channel together with its message keys and factory: the two settings
shapes, the three parsing branches of priority, strict decoding and static validation
Prepare and Send sit in channel.go, rendering and the /message join in render.go, and masking with
Problem construction in mask.go
Both the token and the full address enter the request alone, so the static checks here judge the shape
and print no value
*/

import (
	"encoding/json"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
	"github.com/MarchSnow-1/TaskPush/internal/settings"
)

// 消息键与协议常量, 每个消息键都以 channel.gotify. 开头并在 en 与 zh-CN 两份目录里成对存在
//
// The message keys and protocol constants, where every key starts with channel.gotify. and exists
// in both the en and the zh-CN catalog
const (
	keyDesc              = "channel.gotify.desc"
	keyFieldURL          = "channel.gotify.field.url.desc"
	keyFieldToken        = "channel.gotify.field.token.desc"
	keyFieldPriority     = "channel.gotify.field.priority.desc"
	keyErrorMissingURL   = "channel.gotify.error.missing_url"
	keyErrorMissingToken = "channel.gotify.error.missing_token"
	keyErrorInvalidURL   = "channel.gotify.error.invalid_url"
	keyErrorInvalidToken = "channel.gotify.error.invalid_token"
	keyErrorEmptyContent = "channel.gotify.error.empty_content"
	keyErrorInvalidPrior = "channel.gotify.error.invalid_priority"
	keyErrorHTTPStatus   = "channel.gotify.error.http_status"
	keyWarnTruncated     = "channel.gotify.warn.truncated"
	keyPreviewMessage    = "channel.gotify.preview.message"
)

const (
	// messagePath 是 Gotify 的消息端点路径
	// messagePath is the path of the Gotify message endpoint
	messagePath = "/message"

	// headerKey 是应用 token 所在的请求头名
	// headerKey is the header name carrying the application token
	headerKey = "X-Gotify-Key"

	// messageLimitRunes 是 message 的 rune 上限, 超出即截断并告警
	// messageLimitRunes is the rune cap of a message, past which the text is cut and a warning raised
	messageLimitRunes = 32767

	// priorityMin 与 priorityMax 是 Gotify 接受的优先级区间
	// priorityMin and priorityMax bound the priority range Gotify accepts
	priorityMin = 0
	priorityMax = 10

	// contentTypeJSON 是请求体的媒体类型
	// contentTypeJSON is the media type of the request body
	contentTypeJSON = "application/json; charset=utf-8"

	// settingsPathURL, settingsPathToken 与 settingsPathPriority 是模板错误与告警里的字段路径
	// settingsPathURL, settingsPathToken and settingsPathPriority are the field paths used by
	// template errors and warnings
	settingsPathURL      = "settings.url"
	settingsPathToken    = "settings.token"
	settingsPathPriority = "settings.priority"

	// maskValue 替换服务端文本里回显的 token
	// maskValue replaces a token echoed inside server-supplied text
	maskValue = "***"
)

// gotifyRawSettings 是 settings 的解码形态
// priority 先留成 json.RawMessage, 让数字, 十进制字符串与模板字符串各走自己的判定,
// 也让非法取值落到 channel.gotify.error.invalid_priority 而不是笼统的解码错误
//
// gotifyRawSettings is the decoded shape of the settings
// priority stays a json.RawMessage first, so a number, a decimal string and a template string each
// get their own verdict and an invalid value reaches channel.gotify.error.invalid_priority rather
// than a generic decoding error
type gotifyRawSettings struct {
	URL      string          `json:"url"`
	Token    string          `json:"token"`
	Priority json.RawMessage `json:"priority"`
}

// gotifySettings 是渠道运行期使用的 settings
// priority 与 0 都是合法取值, 所以"是否给出"单独记录; PriorityTemplate 非空表示数字要等 Prepare 渲染
//
// gotifySettings is what the channel uses at run time
// 0 is a legal priority, so whether the field was supplied is recorded apart, and a non-empty
// PriorityTemplate means the number only exists after Prepare renders it
type gotifySettings struct {
	URL              string
	Token            string
	PriorityTemplate string
	HasTemplate      bool
	Priority         int
	HasPriority      bool
}

// factory 创建 gotify 渠道实例
//
// factory creates gotify channel instances
type factory struct{}

// init 在包加载时注册工厂, 装配包以空白导入触发
//
// init registers the factory while the package loads, which the assembly package triggers with a
// blank import
func init() { channel.Register(factory{}) }

// Type 返回注册类型名
//
// Type returns the registered type name
func (factory) Type() string { return "gotify" }

// Describe 返回渠道描述的消息键
//
// Describe returns the message key describing the channel
func (factory) Describe() string { return keyDesc }

// Fields 返回全部 settings 字段的元数据, 顺序稳定
//
// Fields returns the metadata of every settings field in a stable order
func (factory) Fields() []channel.Field {
	return []channel.Field{
		{Name: "url", Type: "string", Required: true, DescriptionKey: keyFieldURL},
		{Name: "token", Type: "string", Required: true, DescriptionKey: keyFieldToken},
		{Name: "priority", Type: "int", Required: false, DescriptionKey: keyFieldPriority},
	}
}

// Create 严格解码 settings 并做静态校验
// url 与 token 必填; 不含模板的 url 在这里就按地址规则校验, 含 "{{" 的留给 Prepare, 因为那时变量才有值;
// priority 缺失或 null 表示未给出, 数字与十进制字符串当场解析, 模板字符串留到 Prepare, 其余取值报错
// 静态失败一律包成 Permanent, 因为重试不改变配置判定
//
// Create strictly decodes the settings and validates them statically
// url and token are required, an url without a template is address-checked here while one holding
// "{{" waits for Prepare, when the variables have values, and priority: an absent field or null means
// "not supplied", a number or decimal string is parsed on the spot, a template string waits for
// Prepare, and any other value is an error
// A static failure is wrapped with Permanent, since a retry cannot change a configuration verdict
func (factory) Create(id string, raw json.RawMessage) (channel.Channel, error) {
	var rawSettings gotifyRawSettings
	if err := channel.DecodeStrict(raw, &rawSettings); err != nil {
		return nil, apperr.Permanent(err)
	}

	s := gotifySettings{
		URL:   strings.TrimSpace(rawSettings.URL),
		Token: strings.TrimSpace(rawSettings.Token),
	}
	if s.URL == "" {
		return nil, apperr.Permanent(problem(keyErrorMissingURL, nil))
	}
	if s.Token == "" {
		return nil, apperr.Permanent(problem(keyErrorMissingToken, nil))
	}

	if !strings.Contains(s.URL, "{{") {
		if _, err := common.NormalizeURL(s.URL); err != nil {
			return nil, apperr.Permanent(invalidURLProblem(s.URL, err))
		}
	}

	priorityTemplate, err := parsePriority(rawSettings.Priority, &s)
	if err != nil {
		return nil, apperr.Permanent(err)
	}
	s.PriorityTemplate = priorityTemplate
	s.HasTemplate = priorityTemplate != ""

	return &gotifyChannel{id: id, s: s}, nil
}

// parsePriority 解析 priority 字段并写进 s, 返回需要留到 Prepare 的模板文本
// 缺失与 null 表示未给出; settings.Int 接受 JSON 数字与十进制字符串; 解析失败时, 含 "{{" 的字符串
// 交回模板分支, 其它取值报 channel.gotify.error.invalid_priority, Value 取原始写法供用户定位
//
// parsePriority reads the priority field into s and returns the template text left for Prepare
// An absent field or null means "not supplied", settings.Int takes a JSON number and a decimal
// string, and when parsing fails a string holding "{{" goes back as the template branch while any
// other value reports channel.gotify.error.invalid_priority with Value holding what was written so
// the user can find it
func parsePriority(raw json.RawMessage, s *gotifySettings) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", nil
	}

	var parsed settings.Int
	if err := json.Unmarshal(raw, &parsed); err == nil {
		s.Priority, s.HasPriority = parsed.Int(), true
		return "", nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil && strings.Contains(text, "{{") {
		return text, nil
	}
	return "", problem(keyErrorInvalidPrior, map[string]any{"Value": rawValueText(raw)})
}

// rawValueText 把 settings 原文压成可放进错误 Data 的文本
// JSON 字符串取引号内的内容, 其它取值取去掉首尾空白的原文
//
// rawValueText compresses the raw settings text into something an error Data can carry
// A JSON string keeps the text inside the quotes, and any other value keeps the trimmed text
func rawValueText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}
