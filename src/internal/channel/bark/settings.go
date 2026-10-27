package bark

/*
bark 渠道的配置结构, 消息键与工厂: settings 的解码形态与运行期形态, badge/ttl/autoCopy 的静态校验都在这里
Prepare 与 Send 在 channel.go, 请求体构造与模板渲染在 render.go, 打码与 Problem 构造在 mask.go

The configuration of the bark channel together with its message keys and factory: the decoded and
the run-time settings shapes, plus the static validation of badge, ttl and autoCopy all live here
Prepare and Send sit in channel.go, request-body construction and template rendering in render.go,
and masking and Problem building in mask.go
*/

import (
	"encoding/json"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
	"github.com/MarchSnow-1/TaskPush/internal/settings"
)

// 消息键与协议常量, 每个消息键都以 channel.bark. 开头并在 en 与 zh-CN 两份目录里成对存在
//
// The message keys and protocol constants, where every key starts with channel.bark. and exists in
// both the en and the zh-CN catalog
const (
	keyDesc                 = "channel.bark.desc"
	keyFieldToken           = "channel.bark.field.token.desc"
	keyFieldTokens          = "channel.bark.field.tokens.desc"
	keyFieldServer          = "channel.bark.field.server.desc"
	keyFieldSubtitle        = "channel.bark.field.subtitle.desc"
	keyFieldGroup           = "channel.bark.field.group.desc"
	keyFieldLevel           = "channel.bark.field.level.desc"
	keyFieldSound           = "channel.bark.field.sound.desc"
	keyFieldIcon            = "channel.bark.field.icon.desc"
	keyFieldImage           = "channel.bark.field.image.desc"
	keyFieldClickURL        = "channel.bark.field.clickUrl.desc"
	keyFieldCopy            = "channel.bark.field.copy.desc"
	keyFieldBadge           = "channel.bark.field.badge.desc"
	keyFieldTTL             = "channel.bark.field.ttl.desc"
	keyFieldAutoCopy        = "channel.bark.field.autoCopy.desc"
	keyErrorTokenOrTokens   = "channel.bark.error.token_or_tokens"
	keyErrorEmptyContent    = "channel.bark.error.empty_content"
	keyErrorInvalidServer   = "channel.bark.error.invalid_server"
	keyErrorInvalidBadge    = "channel.bark.error.invalid_badge"
	keyErrorInvalidTTL      = "channel.bark.error.invalid_ttl"
	keyErrorInvalidAutoCopy = "channel.bark.error.invalid_auto_copy"
	keyErrorInvalidURL      = "channel.bark.error.invalid_url"
	keyErrorAPICode         = "channel.bark.error.api_code"
	keyErrorBatchFailed     = "channel.bark.error.batch_failed"
	keyWarnBatchUnverified  = "channel.bark.warn.batch_unverified"
	keyWarnNoDeviceKey      = "channel.bark.warn.batch_no_device_key"
	// keyWarnTruncated 按 bark spec 第 7 节保留在 catalog 里, 但本渠道不产生它:
	// 标题与正文只受模板层 64KB 上限约束, 严格模式超限由模板层报错
	//
	// keyWarnTruncated stays in the catalog under section 7 of the bark spec, while the channel
	// raises it nowhere: the title and the body are bounded by the template-layer 64KB cap alone, and
	// strict mode reports an oversized render through the template layer
	keyWarnTruncated = "channel.bark.warn.truncated"
	keyPreviewSingle = "channel.bark.preview.single"
	keyPreviewBatch  = "channel.bark.preview.batch"
)

const (
	// defaultServer 是 server 渲染后为空时使用的官方地址
	// defaultServer is the official address used when server renders empty
	defaultServer = "https://api.day.app"
	// contentTypeJSON 是请求体的媒体类型
	// contentTypeJSON is the media type of the request body
	contentTypeJSON = "application/json; charset=utf-8"
)

// barkRawSettings 是 settings 的解码形态
// badge/ttl/autoCopy 先留成 json.RawMessage, 让非法取值落到各自的错误键而不是笼统的解码错误
//
// barkRawSettings is the decoded shape of the settings
// badge, ttl and autoCopy stay as json.RawMessage first, so an invalid value reaches its own error
// key instead of a generic decoding error
type barkRawSettings struct {
	Token    string              `json:"token"`
	Tokens   settings.StringList `json:"tokens"`
	Server   string              `json:"server"`
	Subtitle string              `json:"subtitle"`
	Group    string              `json:"group"`
	Level    string              `json:"level"`
	Sound    string              `json:"sound"`
	Icon     string              `json:"icon"`
	Image    string              `json:"image"`
	ClickURL string              `json:"clickUrl"`
	Copy     string              `json:"copy"`
	Badge    json.RawMessage     `json:"badge"`
	TTL      json.RawMessage     `json:"ttl"`
	AutoCopy json.RawMessage     `json:"autoCopy"`
}

// barkSettings 是渠道运行期使用的 settings: 数字与布尔已经解析, 并带上"是否给出"的标记
// 0 与 false 都是合法取值, 所以是否给出必须单独记录
//
// barkSettings is what the channel uses at run time: the number and the boolean are parsed
// already, together with a flag saying whether they were supplied
// Both 0 and false are legal values, so whether the field was supplied has to be recorded apart
type barkSettings struct {
	Token       string
	Tokens      settings.StringList
	Server      string
	Subtitle    string
	Group       string
	Level       string
	Sound       string
	Icon        string
	Image       string
	ClickURL    string
	Copy        string
	Badge       int
	HasBadge    bool
	TTL         int
	HasTTL      bool
	AutoCopy    bool
	HasAutoCopy bool
}

// factory 创建 bark 渠道实例
//
// factory creates bark channel instances
type factory struct{}

// init 在包加载时注册工厂, 装配包以空白导入触发
//
// init registers the factory while the package loads, which the assembly package triggers with a
// blank import
func init() { channel.Register(factory{}) }

// Type 返回注册类型名
//
// Type returns the registered type name
func (factory) Type() string { return "bark" }

// Describe 返回渠道描述的消息键
//
// Describe returns the message key describing the channel
func (factory) Describe() string { return keyDesc }

// Fields 返回全部 settings 字段的元数据, 顺序稳定
// token 与 tokens 二选一, 单个 Required 无法表达该约束, 因此两者都标为非必填, 由描述文案说明
//
// Fields returns the metadata of every settings field in a stable order
// token and tokens are mutually exclusive, and a single Required flag cannot express that, so both
// are marked optional and the field descriptions carry the either-or rule
func (factory) Fields() []channel.Field {
	return []channel.Field{
		{Name: "token", Type: "string", Required: false, DescriptionKey: keyFieldToken},
		{Name: "tokens", Type: "list<string>", Required: false, DescriptionKey: keyFieldTokens},
		{Name: "server", Type: "string", Required: false, Default: defaultServer, DescriptionKey: keyFieldServer},
		{Name: "subtitle", Type: "string", Required: false, DescriptionKey: keyFieldSubtitle},
		{Name: "group", Type: "string", Required: false, DescriptionKey: keyFieldGroup},
		{Name: "level", Type: "string", Required: false, DescriptionKey: keyFieldLevel},
		{Name: "sound", Type: "string", Required: false, DescriptionKey: keyFieldSound},
		{Name: "icon", Type: "string", Required: false, DescriptionKey: keyFieldIcon},
		{Name: "image", Type: "string", Required: false, DescriptionKey: keyFieldImage},
		{Name: "clickUrl", Type: "string", Required: false, DescriptionKey: keyFieldClickURL},
		{Name: "copy", Type: "string", Required: false, DescriptionKey: keyFieldCopy},
		{Name: "badge", Type: "int", Required: false, DescriptionKey: keyFieldBadge},
		{Name: "ttl", Type: "int", Required: false, DescriptionKey: keyFieldTTL},
		{Name: "autoCopy", Type: "bool", Required: false, DescriptionKey: keyFieldAutoCopy},
	}
}

// Create 严格解码 settings 并做静态校验
// token 与 tokens 必须且只能给出一个; server 可省略, 给出且不含模板时按 URL 规则校验;
// badge/ttl 必须是非负整数, autoCopy 必须是布尔或 0/1 写法
// 静态失败一律包成 Permanent, 因为重试不改变配置判定
//
// Create strictly decodes the settings and validates them statically
// Exactly one of token and tokens is required; server may be omitted and is URL-checked when it is
// present without a template; badge and ttl have to be non-negative integers, and autoCopy has to
// be a boolean or a 0/1 spelling
// A static failure is wrapped with Permanent, since a retry cannot change a configuration verdict
func (factory) Create(id string, raw json.RawMessage) (channel.Channel, error) {
	var rawSettings barkRawSettings
	if err := channel.DecodeStrict(raw, &rawSettings); err != nil {
		return nil, apperr.Permanent(err)
	}

	s := barkSettings{
		Token:    strings.TrimSpace(rawSettings.Token),
		Tokens:   rawSettings.Tokens,
		Server:   strings.TrimSpace(rawSettings.Server),
		Subtitle: rawSettings.Subtitle,
		Group:    rawSettings.Group,
		Level:    rawSettings.Level,
		Sound:    rawSettings.Sound,
		Icon:     rawSettings.Icon,
		Image:    rawSettings.Image,
		ClickURL: rawSettings.ClickURL,
		Copy:     rawSettings.Copy,
	}

	if (s.Token != "") == (len(s.Tokens) > 0) {
		return nil, apperr.Permanent(problem(keyErrorTokenOrTokens, nil))
	}

	// server 缺省表示使用官方地址, 只含空白则属于取值错误: 空串省略与"填了空白"是两回事
	// An omitted server means the official address, while a whitespace-only value is a bad value:
	// omitting the field and filling in blanks are two different things
	if rawSettings.Server != "" && s.Server == "" {
		return nil, apperr.Permanent(invalidServerProblem(rawSettings.Server,
			problem(keyErrorInvalidServer, nil)))
	}

	// server 含模板时把校验留到 Prepare, 那时变量才有值
	// A server holding a template waits for Prepare, where the variables have values
	if s.Server != "" && !strings.Contains(s.Server, "{{") {
		if _, err := common.ValidateHTTPURL(s.Server, "server"); err != nil {
			return nil, apperr.Permanent(invalidServerProblem(s.Server, err))
		}
	}

	badge, hasBadge, err := parseNonNegativeInt(rawSettings.Badge, keyErrorInvalidBadge)
	if err != nil {
		return nil, apperr.Permanent(err)
	}
	s.Badge, s.HasBadge = badge, hasBadge

	ttl, hasTTL, err := parseNonNegativeInt(rawSettings.TTL, keyErrorInvalidTTL)
	if err != nil {
		return nil, apperr.Permanent(err)
	}
	s.TTL, s.HasTTL = ttl, hasTTL

	autoCopy, hasAutoCopy, err := parseAutoCopy(rawSettings.AutoCopy)
	if err != nil {
		return nil, apperr.Permanent(err)
	}
	s.AutoCopy, s.HasAutoCopy = autoCopy, hasAutoCopy

	return &barkChannel{id: id, s: s}, nil
}

// parseNonNegativeInt 解析 badge/ttl: 字段缺失或 null 表示未给出, 其它取值走 settings.Int
// 解析失败与负数都返回对应字段的错误键, Value 取原始写法供用户定位
//
// parseNonNegativeInt parses badge and ttl: a missing field or null means "not supplied" and any
// other value goes through settings.Int
// A parse failure and a negative number both return the error key of that field, with Value holding
// what was written so the user can find it
func parseNonNegativeInt(raw json.RawMessage, key string) (int, bool, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return 0, false, nil
	}
	value := rawValueText(raw)
	var parsed settings.Int
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed.Int() < 0 {
		return 0, false, problem(key, map[string]any{"Value": value})
	}
	return parsed.Int(), true, nil
}

// parseAutoCopy 解析 autoCopy: 字段缺失或 null 表示未给出
// 布尔与 "true"/"false"/"1"/"0" 走 settings.Bool, 数字 0/1 单独接受, 其余取值报错
//
// parseAutoCopy parses autoCopy: a missing field or null means "not supplied"
// A boolean and the "true", "false", "1" and "0" spellings go through settings.Bool, the numbers
// 0 and 1 are accepted separately, and any other value reports an error
func parseAutoCopy(raw json.RawMessage) (bool, bool, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return false, false, nil
	}
	var parsed settings.Bool
	if err := json.Unmarshal(raw, &parsed); err == nil {
		return parsed.Bool(), true, nil
	}
	switch text {
	case "0":
		return false, true, nil
	case "1":
		return true, true, nil
	}
	return false, false, problem(keyErrorInvalidAutoCopy, map[string]any{"Value": rawValueText(raw)})
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
