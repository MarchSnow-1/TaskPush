package wecom

/*
wecom 渠道的配置结构, 消息键与协议常量, 以及渠道工厂: 严格解码, msgType 归一与静态校验都在这里
Channel 与 Prepared 的类型, Prepare 与 Send 在 channel.go, 内容拼接与截断在 render.go,
URL 里 key 的提取, 遮蔽与 Problem 构造在 mask.go

The configuration of the wecom channel together with its message keys, protocol constants and the
channel factory: strict decoding, msgType normalisation and static validation all live here
The Channel and Prepared types with Prepare and Send sit in channel.go, content joining and
truncation in render.go, and the extraction of the URL key together with masking and Problem
construction in mask.go
*/

import (
	"encoding/json"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
)

// 消息键与协议常量, 每个消息键都以 channel.wecom. 开头并在 en 与 zh-CN 两份目录里成对存在
//
// The message keys and protocol constants, where every key starts with channel.wecom. and exists in
// both the en and the zh-CN catalog
const (
	keyDesc              = "channel.wecom.desc"
	keyFieldURL          = "channel.wecom.field.url.desc"
	keyFieldMsgType      = "channel.wecom.field.msgType.desc"
	keyErrorMissingURL   = "channel.wecom.error.missing_url"
	keyErrorMissingKey   = "channel.wecom.error.missing_key"
	keyErrorInvalidURL   = "channel.wecom.error.invalid_url"
	keyErrorInvalidType  = "channel.wecom.error.invalid_msg_type"
	keyErrorEmptyContent = "channel.wecom.error.empty_content"
	keyErrorAPICode      = "channel.wecom.error.api_code"
	keyErrorBadResponse  = "channel.wecom.error.bad_response"
	keyWarnTruncated     = "channel.wecom.warn.truncated"
	keyPreviewMessage    = "channel.wecom.preview.message"
)

const (
	// msgTypeText 是默认消息类型, 走请求体的 text 字段
	// msgTypeText is the default message type, carried by the text field of the request body
	msgTypeText = "text"
	// msgTypeMarkdown 是 markdown 消息类型, 走请求体的 markdown 字段
	// msgTypeMarkdown is the markdown message type, carried by the markdown field of the request body
	msgTypeMarkdown = "markdown"
	// textLimitBytes 是 text 正文的 UTF-8 字节上限
	// textLimitBytes is the UTF-8 byte cap of a text body
	textLimitBytes = 2048
	// markdownLimitBytes 是 markdown 正文的 UTF-8 字节上限
	// markdownLimitBytes is the UTF-8 byte cap of a markdown body
	markdownLimitBytes = 4096
	// targetCount 是单目标渠道的固定目标数, 供预览使用
	// targetCount is the fixed target count of this single-target channel, used by the preview
	targetCount = 1
	// maskValue 替换服务端文本里回显的 key
	// maskValue replaces a key echoed inside server-supplied text
	maskValue = "***"
)

// wecomSettings 是 wecom 渠道的 settings
//
// wecomSettings holds the settings of the wecom channel
type wecomSettings struct {
	URL     string `json:"url"`
	MsgType string `json:"msgType"`
}

// factory 创建 wecom 渠道实例
//
// factory creates wecom channel instances
type factory struct{}

// init 在包加载时注册工厂, 装配包以空白导入触发
//
// init registers the factory while the package loads, which the assembly package triggers with a
// blank import
func init() { channel.Register(factory{}) }

// Type 返回注册类型名
//
// Type returns the registered type name
func (factory) Type() string { return "wecom" }

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
		{Name: "msgType", Type: "string", Required: false, Default: msgTypeText, DescriptionKey: keyFieldMsgType},
	}
}

// Create 严格解码 settings 并做静态校验
// url 必填; 不含模板的 url 先做地址校验, 并要求 key query 非空; msgType 空值按 text 处理
// 静态失败一律包成 Permanent, 因为重试不改变配置判定
//
// Create strictly decodes the settings and validates them statically
// url is required, and one without a template goes through the address check plus a non-empty key
// query requirement, while an empty msgType reads as text
// A static failure is wrapped with Permanent, since a retry cannot change a configuration verdict
func (factory) Create(id string, raw json.RawMessage) (channel.Channel, error) {
	var s wecomSettings
	if err := channel.DecodeStrict(raw, &s); err != nil {
		return nil, apperr.Permanent(err)
	}

	s.URL = strings.TrimSpace(s.URL)
	if s.URL == "" {
		return nil, apperr.Permanent(problem(keyErrorMissingURL, nil))
	}

	// msgType 含模板时枚举判定留给 Prepare: 变量值要到那时才存在
	// 不含模板的取值在这里归一, 空值取默认 text
	//
	// A msgType holding a template leaves the enum verdict to Prepare, since the variable values
	// exist only then
	// A value without a template is normalised here, and an empty one takes the text default
	if !strings.Contains(s.MsgType, "{{") {
		msgType, err := normalizeMsgType(s.MsgType)
		if err != nil {
			return nil, apperr.Permanent(err)
		}
		s.MsgType = msgType
	}

	// 不含模板的地址在这里就能定论: 校验 scheme 与主机, 并确认 key query 非空
	// 含模板的地址要等变量值, key 判定因此留给 Prepare
	//
	// An address without a template can be settled here: the scheme and the host are checked and a
	// non-empty key query is required
	// An address holding a template waits for the variable values, so the key verdict belongs to
	// Prepare
	if !strings.Contains(s.URL, "{{") {
		if _, err := common.ValidateHTTPURL(s.URL, "url"); err != nil {
			return nil, apperr.Permanent(invalidURLProblem(s.URL, err))
		}
		if queryKey(s.URL) == "" {
			return nil, apperr.Permanent(problem(keyErrorMissingKey, nil))
		}
	}

	return &wecomChannel{id: id, s: s}, nil
}

// normalizeMsgType 归一 msgType: 去空白后大小写不敏感比较, 空值取默认 text
// 其它取值返回 channel.wecom.error.invalid_msg_type
//
// normalizeMsgType normalises msgType: surrounding space is trimmed, the comparison ignores case,
// and an empty value takes the text default
// Any other value returns channel.wecom.error.invalid_msg_type
func normalizeMsgType(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	switch strings.ToLower(trimmed) {
	case "":
		return msgTypeText, nil
	case msgTypeText:
		return msgTypeText, nil
	case msgTypeMarkdown:
		return msgTypeMarkdown, nil
	default:
		return "", problem(keyErrorInvalidType, map[string]any{"Type": trimmed})
	}
}
