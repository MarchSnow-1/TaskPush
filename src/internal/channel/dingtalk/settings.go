package dingtalk

/*
dingtalk 渠道的配置结构, 消息键与协议常量, 以及渠道工厂: 严格解码与静态校验都在这里
Channel 与 Prepared 的类型, Prepare 与 Send 在 channel.go, 内容构造与字段路径改写在 render.go,
加签, access_token 处理与服务端回显遮蔽在 mask.go

The configuration of the dingtalk channel together with its message keys, protocol constants and the
channel factory: strict decoding and static validation all live here
The Channel and Prepared types with Prepare and Send sit in channel.go, content construction and the
field-path rewriting in render.go, and signing, the access_token handling and the masking of what a
server echoes back in mask.go
*/

import (
	"encoding/json"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
	"github.com/MarchSnow-1/TaskPush/internal/settings"
)

// 消息键与协议常量, 每个消息键都以 channel.dingtalk. 开头并在 en 与 zh-CN 两份目录里成对存在
//
// The message keys and protocol constants, where every key starts with channel.dingtalk. and exists
// in both the en and the zh-CN catalog
const (
	keyDesc                = "channel.dingtalk.desc"
	keyDefaultMarkdownText = "channel.dingtalk.default_markdown_title"
	keyFieldURL            = "channel.dingtalk.field.url.desc"
	keyFieldSecret         = "channel.dingtalk.field.secret.desc"
	keyFieldMsgType        = "channel.dingtalk.field.msgType.desc"
	keyFieldAtAll          = "channel.dingtalk.field.atAll.desc"
	keyErrorMissingURL     = "channel.dingtalk.error.missing_url"
	keyErrorMissingToken   = "channel.dingtalk.error.missing_access_token"
	keyErrorInvalidURL     = "channel.dingtalk.error.invalid_url"
	keyErrorInvalidMsgType = "channel.dingtalk.error.invalid_msg_type"
	keyErrorEmptyContent   = "channel.dingtalk.error.empty_content"
	keyErrorEmptyMarkdown  = "channel.dingtalk.error.empty_markdown_body"
	keyErrorAtAllMarkdown  = "channel.dingtalk.error.at_all_markdown"
	keyErrorAPICode        = "channel.dingtalk.error.api_code"
	keyErrorBadResponse    = "channel.dingtalk.error.bad_response"
	keyWarnTruncated       = "channel.dingtalk.warn.truncated"
	keyPreviewMessage      = "channel.dingtalk.preview.message"
)

const (
	// msgTypeText 与 msgTypeMarkdown 是允许的两种消息类型, Create 判定时小写归一
	// msgTypeText and msgTypeMarkdown are the two message types allowed, lowercased before Create
	// judges them
	msgTypeText     = "text"
	msgTypeMarkdown = "markdown"

	// defaultMsgType 是 msgType 缺省或渲染为空时的取值
	// defaultMsgType is the value used when msgType is absent or renders empty
	defaultMsgType = msgTypeText

	// contentLimit 是 text/markdown 内容的 UTF-8 字节上限
	// 平台口径 4000 字符, 按字节截断是保守换算: 纯 ASCII 内容恰好在平台上限内,
	// 多字节内容会更早截断, 因此不会越过平台的判定边界
	//
	// contentLimit is the UTF-8 byte cap of the text and markdown content
	// The platform measures 4000 characters, so cutting by bytes is the conservative reading:
	// pure ASCII stays exactly inside the platform cap, while multi-byte text is cut earlier and
	// therefore cannot pass the boundary the platform judges
	contentLimit = 4000

	// signTimestampKey 与 signValueKey 是加签写入的 query 参数名
	// signTimestampKey and signValueKey are the query parameter names signing writes
	signTimestampKey = "timestamp"
	signValueKey     = "sign"

	// accessTokenKey 是端点必须携带的 query 参数名
	// accessTokenKey is the query parameter name the endpoint has to carry
	accessTokenKey = "access_token"

	// contentTypeJSON 是请求体的媒体类型
	// contentTypeJSON is the media type of the request body
	contentTypeJSON = "application/json; charset=utf-8"
)

// dingtalkSettings 是 settings 的解码形态
// msgType 先留字符串, 让非法取值落进 invalid_msg_type 而不是笼统的解码错误;
// atAll 用 settings.Bool, 接受布尔与 "true"/"false"/"1"/"0" 写法
// atAll 用指针承载: 标准库对指针字段解码 null 时不调用 UnmarshalJSON, 因此 "atAll":null 与
// 字段缺省一样读作 false, 而 settings.Bool 本身拒绝 null
//
// dingtalkSettings is the decoded shape of the settings
// msgType stays a string first, so an invalid value reaches invalid_msg_type rather than a generic
// decoding error, while atAll goes through settings.Bool, which takes a boolean or the
// "true", "false", "1" and "0" spellings
// atAll is held by a pointer: the standard library skips UnmarshalJSON for a null on a pointer
// field, so "atAll":null and an absent field both read as false, while settings.Bool itself
// rejects a null
type dingtalkSettings struct {
	URL     string         `json:"url"`
	Secret  string         `json:"secret"`
	MsgType string         `json:"msgType"`
	AtAll   *settings.Bool `json:"atAll"`
}

// atAllValue 读取 atAll 的布尔值, 字段缺省或为 null 时是 false
//
// atAllValue reads the atAll boolean, where an absent field or a null is false
func (s dingtalkSettings) atAllValue() bool {
	return s.AtAll != nil && bool(*s.AtAll)
}

// factory 创建 dingtalk 渠道实例
//
// factory creates dingtalk channel instances
type factory struct{}

// init 在包加载时注册工厂, 装配包以空白导入触发
//
// init registers the factory while the package loads, which the assembly package triggers with a
// blank import
func init() { channel.Register(factory{}) }

// Type 返回注册类型名
//
// Type returns the registered type name
func (factory) Type() string { return "dingtalk" }

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
		{Name: "secret", Type: "string", Required: false, DescriptionKey: keyFieldSecret},
		{Name: "msgType", Type: "string", Required: false, Default: defaultMsgType, DescriptionKey: keyFieldMsgType},
		{Name: "atAll", Type: "bool", Required: false, Default: "false", DescriptionKey: keyFieldAtAll},
	}
}

// Create 严格解码 settings 并做静态校验
// url 必填, 不含模板时按 URL 规则校验; msgType 大小写不敏感且只允许 text/markdown;
// 静态失败一律包成 Permanent, 因为重试不改变配置判定
//
// Create strictly decodes the settings and validates them statically
// url is required and is URL-checked when it holds no template, while msgType is case-insensitive
// and limited to text and markdown
// A static failure is wrapped with Permanent, since a retry cannot change a configuration verdict
func (factory) Create(id string, raw json.RawMessage) (channel.Channel, error) {
	var s dingtalkSettings
	if err := channel.DecodeStrict(raw, &s); err != nil {
		return nil, apperr.Permanent(err)
	}

	s.URL = strings.TrimSpace(s.URL)
	s.Secret = strings.TrimSpace(s.Secret)
	if s.URL == "" {
		return nil, apperr.Permanent(problem(keyErrorMissingURL, nil))
	}
	// msgType 的静态形态在这里判定, 渲染后的取值由 Prepare 再归一次, 因此不保存归一结果
	// 含模板时把枚举判定留到 Prepare, 那时变量才有值, 与 url 的处理一致
	//
	// The static shape of msgType is judged here and the rendered value is normalised again by
	// Prepare, so the normalised result is not stored
	// A value holding a template waits for Prepare, where the variables have values, matching how
	// url is handled
	if !strings.Contains(s.MsgType, "{{") {
		if _, err := normalizeMsgType(s.MsgType); err != nil {
			return nil, apperr.Permanent(err)
		}
	}

	// url 含模板时把校验留到 Prepare, 那时变量才有值
	// A url holding a template waits for Prepare, where the variables have values
	if !strings.Contains(s.URL, "{{") {
		normalized, err := common.NormalizeURL(s.URL)
		if err != nil {
			return nil, apperr.Permanent(invalidURLProblem(s.URL, err))
		}
		if err := checkAccessToken(normalized); err != nil {
			return nil, apperr.Permanent(err)
		}
	}

	return &dingtalkChannel{id: id, s: s}, nil
}

// normalizeMsgType 归一并校验消息类型: TrimSpace 后小写, 空串落到默认 text
// 只允许 text/markdown, 其它取值返回 invalid_msg_type, Type 取原始写法供用户定位
//
// normalizeMsgType normalises and validates the message type: it trims, lowercases and lets an
// empty string fall to the default text
// Only text and markdown are allowed, and any other value returns invalid_msg_type with the value
// as written in Data.Type so the user can find it
func normalizeMsgType(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return defaultMsgType, nil
	}
	if value != msgTypeText && value != msgTypeMarkdown {
		return "", problem(keyErrorInvalidMsgType, map[string]any{"Type": raw})
	}
	return value, nil
}
