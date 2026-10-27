package telegram

/*
telegram 渠道的配置层: 消息键与协议常量, settings 结构, 工厂与静态校验
Prepare 与 Send 在 channel.go, 渲染与路径改写在 render.go, 打码在 mask.go

The configuration layer of the telegram channel: the message keys and protocol constants, the
settings struct, the factory and its static validation
Prepare and Send sit in channel.go, rendering and path rewriting in render.go, and masking in mask.go
*/

import (
	"encoding/json"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
	"github.com/MarchSnow-1/TaskPush/internal/settings"
)

// 消息键与协议常量, 每个消息键都以 channel.telegram. 开头并在 en 与 zh-CN 两份目录里成对存在
//
// The message keys and protocol constants, where every key starts with channel.telegram. and exists
// in both the en and the zh-CN catalog
const (
	keyDesc              = "channel.telegram.desc"
	keyFieldToken        = "channel.telegram.field.token.desc"
	keyFieldChatIDs      = "channel.telegram.field.chatIds.desc"
	keyFieldAPIBase      = "channel.telegram.field.apiBase.desc"
	keyFieldSilent       = "channel.telegram.field.silent.desc"
	keyErrorMissingToken = "channel.telegram.error.missing_token"
	keyErrorMissingChats = "channel.telegram.error.missing_chat_ids"
	keyErrorEmptyContent = "channel.telegram.error.empty_content"
	keyErrorInvalidBase  = "channel.telegram.error.invalid_api_base"
	keyErrorAPIError     = "channel.telegram.error.api_error"
	keyErrorBadResponse  = "channel.telegram.error.bad_response"
	keyWarnTruncated     = "channel.telegram.warn.truncated"
	keyPreviewMessage    = "channel.telegram.preview.message"
)

const (
	// defaultAPIBase 是 apiBase 渲染后为空时使用的官方 Bot API 地址
	// defaultAPIBase is the official Bot API address used when apiBase renders empty
	defaultAPIBase = "https://api.telegram.org"
	// textLimitUTF16 是 sendMessage 的文本上限, Telegram 按 UTF-16 code unit 计长
	// textLimitUTF16 is the sendMessage text limit, which Telegram measures in UTF-16 code units
	textLimitUTF16 = 4096
	// contentTypeJSON 是请求体的媒体类型
	// contentTypeJSON is the media type of the request body
	contentTypeJSON = "application/json; charset=utf-8"
	// maskValue 替换服务端文本里回显的 token
	// maskValue replaces a token echoed inside server-supplied text
	maskValue = "***"
)

// telegramSettings 是 telegram 渠道的 settings
//
// telegramSettings holds the settings of the telegram channel
type telegramSettings struct {
	Token   string              `json:"token"`
	ChatIDs settings.StringList `json:"chatIds"`
	APIBase string              `json:"apiBase"`
	Silent  settings.Bool       `json:"silent"`
}

// factory 创建 telegram 渠道实例
//
// factory creates telegram channel instances
type factory struct{}

// init 在包加载时注册工厂, 装配包以空白导入触发
//
// init registers the factory while the package loads, which the assembly package triggers with a
// blank import
func init() { channel.Register(factory{}) }

// Type 返回注册类型名
//
// Type returns the registered type name
func (factory) Type() string { return "telegram" }

// Describe 返回渠道描述的消息键
//
// Describe returns the message key describing the channel
func (factory) Describe() string { return keyDesc }

// Fields 返回全部 settings 字段的元数据, 顺序稳定
//
// Fields returns the metadata of every settings field in a stable order
func (factory) Fields() []channel.Field {
	return []channel.Field{
		{Name: "token", Type: "string", Required: true, DescriptionKey: keyFieldToken},
		{Name: "chatIds", Type: "list<string>", Required: true, DescriptionKey: keyFieldChatIDs},
		{Name: "apiBase", Type: "string", Required: false, Default: defaultAPIBase, DescriptionKey: keyFieldAPIBase},
		{Name: "silent", Type: "bool", Required: false, Default: "false", DescriptionKey: keyFieldSilent},
	}
}

// Create 严格解码 settings 并做静态校验
// token 与 chatIds 必填; apiBase 可以省略, 给出且不含模板时按 URL 规则校验
// 静态失败一律包成 Permanent, 因为重试不改变配置判定
//
// Create strictly decodes the settings and validates them statically
// token and chatIds are required, apiBase may be omitted and is URL-checked when present without a
// template
// A static failure is wrapped with Permanent, since a retry cannot change a configuration verdict
func (factory) Create(id string, raw json.RawMessage) (channel.Channel, error) {
	var s telegramSettings
	if err := channel.DecodeStrict(raw, &s); err != nil {
		return nil, apperr.Permanent(err)
	}

	s.Token = strings.TrimSpace(s.Token)
	if s.Token == "" {
		return nil, apperr.Permanent(problem(keyErrorMissingToken, nil))
	}
	if len(s.ChatIDs) == 0 {
		return nil, apperr.Permanent(problem(keyErrorMissingChats, nil))
	}

	// apiBase 为空或纯空白时按未提供处理, 由 Prepare 用官方地址兜底; 非空值在 Create 阶段先做
	// 静态 URL 校验, 含 "{{" 的地址留到 Prepare 渲染后再校验, 因为那时才有变量值
	//
	// An empty or whitespace-only apiBase counts as omitted, and Prepare falls back to the official
	// address; a non-empty value goes through the static URL check here, while one holding "{{"
	// waits for Prepare, when the variable values exist
	s.APIBase = strings.TrimSpace(s.APIBase)
	if s.APIBase != "" && !strings.Contains(s.APIBase, "{{") {
		if _, err := common.ValidateHTTPURL(s.APIBase, "apiBase"); err != nil {
			return nil, apperr.Permanent(invalidAPIBaseProblem(s.APIBase, err))
		}
	}

	return &telegramChannel{id: id, s: s}, nil
}
