package ntfy

/*
ntfy 渠道的配置结构, 消息键与工厂: settings 的两种形态, priority 的归一写法, 严格解码与静态校验
Prepare 与 Send 在 channel.go, 渲染与内容判定在 render.go, 打码与 Problem 构造在 mask.go
完整地址与 token 只用于发送, 因此这里的静态校验只判形态, 不打印取值

The configuration of the ntfy channel together with its message keys and factory: the two settings
shapes, the normalised priority spelling, strict decoding and static validation
Prepare and Send sit in channel.go, rendering and content decisions in render.go, and masking with
Problem construction in mask.go
The full address and the token serve sending only, so the static checks here judge the shape alone and
print no value
*/

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/settings"
)

// 消息键与协议常量, 每个消息键都以 channel.ntfy. 开头并在 en 与 zh-CN 两份目录里成对存在
//
// The message keys and protocol constants, where every key starts with channel.ntfy. and exists in
// both the en and the zh-CN catalog
const (
	keyDesc                 = "channel.ntfy.desc"
	keyFieldURL             = "channel.ntfy.field.url.desc"
	keyFieldToken           = "channel.ntfy.field.token.desc"
	keyFieldPriority        = "channel.ntfy.field.priority.desc"
	keyFieldTags            = "channel.ntfy.field.tags.desc"
	keyErrorMissingURL      = "channel.ntfy.error.missing_url"
	keyErrorInvalidURL      = "channel.ntfy.error.invalid_url"
	keyErrorMissingTopic    = "channel.ntfy.error.missing_topic"
	keyErrorEmptyContent    = "channel.ntfy.error.empty_content"
	keyErrorInvalidPriority = "channel.ntfy.error.invalid_priority"
	keyErrorInvalidHeader   = "channel.ntfy.error.invalid_header"
	keyErrorHTTPStatus      = "channel.ntfy.error.http_status"
	keyWarnTruncated        = "channel.ntfy.warn.truncated"
	keyPreviewMessage       = "channel.ntfy.preview.message"
)

const (
	// typeName 是注册用的渠道类型名
	// typeName is the registered channel type name
	typeName = "ntfy"

	// bodyLimitBytes 是正文的 UTF-8 字节上限, 超过由 common.TruncateBytes 截断并发警告
	// bodyLimitBytes is the UTF-8 byte cap of the body, which TruncateBytes cuts at and a warning
	// accompanies
	bodyLimitBytes = 4096

	// contentTypeText 是请求体的媒体类型: ntfy 把正文当纯文本读
	// contentTypeText is the media type of the request body, since ntfy reads it as plain text
	contentTypeText = "text/plain"

	// 四个自定义 header 名, 与 spec 第 5 节的报文样例一致
	// The four custom header names, matching the request sample of spec section 5
	headerTitle         = "Title"
	headerPriority      = "Priority"
	headerTags          = "Tags"
	headerAuthorization = "Authorization"

	// bearerPrefix 是 token 进入 Authorization 时使用的方案前缀, 末尾空格属于前缀本身
	// bearerPrefix is the scheme written in front of a token in Authorization, with its trailing
	// space being part of the prefix
	bearerPrefix = "Bearer "

	// priorityMin 与 priorityMax 是数字 priority 的合法区间
	// priorityMin and priorityMax bound the legal range of a numeric priority
	priorityMin = 1
	priorityMax = 5

	// settingsPath* 是模板告警与错误里的字段路径
	// The settingsPath* constants are the field paths used by template warnings and errors
	settingsPathURL      = "settings.url"
	settingsPathToken    = "settings.token"
	settingsPathPriority = "settings.priority"
	settingsPathTags     = "settings.tags"

	// maskValue 遮蔽服务端回显文本里可能出现的 token
	// maskValue hides a token that server-supplied text may echo
	maskValue = "***"

	// valueDisplayLimit 是放进错误 Data.Value 的最多 rune 数, 免得超长配置值淹没文案
	// valueDisplayLimit is the most runes an error Data.Value keeps, so an oversized setting does
	// not drown the message
	valueDisplayLimit = 80
)

// priorityEnums 是 spec 第 2 节允许的五个枚举名, 比较时先转小写
//
// priorityEnums holds the five enum names allowed by spec section 2, compared after lowercasing
var priorityEnums = map[string]bool{
	"min":     true,
	"low":     true,
	"default": true,
	"high":    true,
	"max":     true,
}

// rawSettings 是 settings 的解码形态: priority 先留成 json.RawMessage
// priority 允许数字, 枚举名与模板三种写法, 用 settings.Int 直接解码会让 "high" 与 "{{p}}" 在
// Create 就变成笼统的解码错误, 所以这里先取原文, 由 Create 区分类型, 由 Prepare 判定取值
//
// rawSettings is the decoded shape of settings, where priority stays a json.RawMessage
// priority accepts a number, an enum name and a template, while decoding it with settings.Int
// turns "high" and "{{p}}" into a general decoding error back in Create, so the raw text is kept
// here, Create tells the JSON types apart and Prepare judges the value
type rawSettings struct {
	URL      string              `json:"url"`
	Token    string              `json:"token"`
	Priority json.RawMessage     `json:"priority"`
	Tags     settings.StringList `json:"tags"`
}

// ntfySettings 是 Create 之后不再变化的渠道配置
// Priority 存成文本形态 (数字已转成十进制写法), 因此 Prepare 只有一条渲染与校验路径
// Given 表示字段是否显式配置, 未配置时不发 Priority header
//
// ntfySettings holds the channel configuration, which stays as it is after Create
// Priority is kept as text, with a number already turned into its decimal spelling, so Prepare has
// one path for rendering and validation
// Given records whether the field was configured, and a Priority header is sent only when it was
type ntfySettings struct {
	URL      string
	Token    string
	Priority prioritySetting
	Tags     settings.StringList
}

// prioritySetting 是 priority 的归一写法
type prioritySetting struct {
	Given bool
	Text  string
}

// factory 创建 ntfy 渠道实例
//
// factory creates ntfy channel instances
type factory struct{}

// init 在包加载时注册工厂, 装配包以空白导入触发
//
// init registers the factory while the package loads, which the assembly package triggers with a
// blank import
func init() { channel.Register(factory{}) }

// Type 返回注册类型名
//
// Type returns the registered type name
func (factory) Type() string { return typeName }

// Describe 返回渠道描述的消息键
//
// Describe returns the message key describing the channel
func (factory) Describe() string { return keyDesc }

// Fields 返回全部 settings 字段的元数据, 顺序稳定
// priority 的类型写作 int, 与 badge/ttl 这类「数字或十进制字符串」字段的既有写法一致,
// 枚举名与模板的支持写在字段描述里
//
// Fields returns the metadata of every settings field in a stable order
// priority is typed int, the same spelling badge and ttl use for a "number or decimal string"
// field, while the enum names and template support belong to the field description
func (factory) Fields() []channel.Field {
	return []channel.Field{
		{Name: "url", Type: "string", Required: true, DescriptionKey: keyFieldURL},
		{Name: "token", Type: "string", Required: false, DescriptionKey: keyFieldToken},
		{Name: "priority", Type: "int", Required: false, DescriptionKey: keyFieldPriority},
		{Name: "tags", Type: "list<string>", Required: false, DescriptionKey: keyFieldTags},
	}
}

// Create 严格解码 settings 并做静态校验
// url 必填; priority 接受 JSON 数字, 数字字符串与任意字符串写法 (含枚举名与模板), 拒绝其它 JSON 类型
// tags 由 settings.StringList 解析数组或逗号串; 取值合法性 (priority 区间, URL 形态) 留到 Prepare
// 静态失败都包成 Permanent, 因为重试不改变配置判定
//
// Create strictly decodes the settings and validates them statically
// url is required; priority accepts a JSON number, a numeric string and any other string spelling
// (an enum name or a template included), while every other JSON type is refused
// tags go through settings.StringList for an array or a comma string; value legality, meaning the
// priority range and the address form, waits for Prepare
// A static failure is wrapped with Permanent, since a retry cannot change a configuration verdict
func (factory) Create(id string, raw json.RawMessage) (channel.Channel, error) {
	var r rawSettings
	if err := channel.DecodeStrict(raw, &r); err != nil {
		return nil, apperr.Permanent(err)
	}
	if strings.TrimSpace(r.URL) == "" {
		return nil, apperr.Permanent(problem(keyErrorMissingURL, nil))
	}

	priority, err := decodePriority(r.Priority)
	if err != nil {
		return nil, apperr.Permanent(err)
	}

	return &ntfyChannel{
		id: id,
		s: ntfySettings{
			URL:      strings.TrimSpace(r.URL),
			Token:    r.Token,
			Priority: priority,
			Tags:     r.Tags,
		},
	}, nil
}

// decodePriority 把 priority 原文归一成文本形态
// 缺失或 null 表示未配置; JSON 数字与十进制字符串先走 settings.Int, 得到值后写成十进制文本;
// 其余字符串写法 (枚举名, 模板) 原样保留, 由 Prepare 渲染并校验
// 布尔, 数组, 对象这些 JSON 类型不是 priority 的合法写法, 报 invalid_priority, Value 取原文
//
// decodePriority normalises the raw priority text
// A missing field or null means "not supplied"; a JSON number and a decimal string go through
// settings.Int first and come back as decimal text; every other string spelling, an enum name or a
// template, is kept as written for Prepare to render and check
// A boolean, an array and an object are not legal priority types, so invalid_priority reports them
// with Value holding what was written
func decodePriority(raw json.RawMessage) (prioritySetting, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return prioritySetting{}, nil
	}
	var parsed settings.Int
	if err := json.Unmarshal(raw, &parsed); err == nil {
		return prioritySetting{Given: true, Text: strconv.Itoa(parsed.Int())}, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return prioritySetting{Given: true, Text: s}, nil
	}
	return prioritySetting{}, problem(keyErrorInvalidPriority, map[string]any{"Value": displayValue(text)})
}
