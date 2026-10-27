package webhook

/*
webhook 渠道的配置结构, 消息键与工厂: settings/body 两种形态, 严格解码与静态校验都在这里
Prepare 与 Send 在 channel.go, 模板渲染与请求体构造在 render.go, 打码与脱敏在 mask.go

The configuration of the webhook channel together with its message keys and factory: the
settings/body shapes, strict decoding and static validation all live here
Prepare and Send sit in channel.go, template rendering and request construction in render.go, and
masking and redaction in mask.go
*/

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
)

// 消息键, 每个都以 channel.webhook. 开头并在 en 与 zh-CN 两份目录里成对存在
//
// The message keys, each starting with channel.webhook. and existing in both the en and the zh-CN
// catalog
const (
	keyDesc                 = "channel.webhook.desc"
	keyFieldURL             = "channel.webhook.field.url.desc"
	keyFieldMethod          = "channel.webhook.field.method.desc"
	keyFieldHeaders         = "channel.webhook.field.headers.desc"
	keyFieldBody            = "channel.webhook.field.body.desc"
	keyFieldBodyFormat      = "channel.webhook.field.body.format.desc"
	keyFieldBodyJSON        = "channel.webhook.field.body.json.desc"
	keyFieldBodyText        = "channel.webhook.field.body.text.desc"
	keyFieldBodyFields      = "channel.webhook.field.body.fields.desc"
	keyFieldBodyContentType = "channel.webhook.field.body.contentType.desc"
	keyFieldMaskSecrets     = "channel.webhook.field.maskSecrets.desc"

	keyErrorMissingURL        = "channel.webhook.error.missing_url"
	keyErrorInvalidURL        = "channel.webhook.error.invalid_url"
	keyErrorInvalidMethod     = "channel.webhook.error.invalid_method"
	keyErrorInvalidHeaderName = "channel.webhook.error.invalid_header_name"
	keyErrorInvalidHeaderVal  = "channel.webhook.error.invalid_header_value"
	keyErrorDuplicateHeader   = "channel.webhook.error.duplicate_header"
	keyErrorSpecialHeader     = "channel.webhook.error.special_header"
	keyErrorBodyEmpty         = "channel.webhook.error.body_empty"
	keyErrorBodyFormat        = "channel.webhook.error.body_format"
	keyErrorBodyJSONMissing   = "channel.webhook.error.body_json_missing"
	keyErrorInvalidJSONBody   = "channel.webhook.error.invalid_json_body"
	keyErrorContentLength     = "channel.webhook.error.content_length"
	keyErrorTransferEncoding  = "channel.webhook.error.transfer_encoding"
	keyErrorMaskEmpty         = "channel.webhook.error.mask_empty"

	keyWarnUnresolved  = "channel.webhook.warn.unresolved"
	keyWarnMaskMiss    = "channel.webhook.warn.mask_miss"
	keyWarnMaskAllMiss = "channel.webhook.warn.mask_all_miss"
	keyPreviewHTTP     = "channel.webhook.preview.http"
)

const (
	// formatJSON, formatText 与 formatForm 是对象正文允许的三种格式
	// formatJSON, formatText and formatForm are the three formats the object body accepts
	formatJSON = "json"
	formatText = "text"
	formatForm = "form"

	// formatNone 是正文缺省时的预览格式, 用于区分"没有正文"与空的 text 正文
	// formatNone is the preview format for an absent body, telling it apart from an empty text body
	formatNone = "none"

	contentTypeJSON = "application/json; charset=utf-8"
	contentTypeText = "text/plain; charset=utf-8"
	contentTypeForm = "application/x-www-form-urlencoded"

	defaultMethod = http.MethodPost

	// allowedFormats 是 body.format 的取值列表, 出现在 body_format 文案的 Allowed 占位符里
	// allowedFormats lists the body.format values shown in the Allowed placeholder of body_format
	allowedFormats = "json, text, form"

	// formatMissing 是 format 键缺省时写进 Data.Format 的标记, 与「写了但不在白名单」区分开
	// formatMissing is the Data.Format marker when the format key is absent, telling it apart from
	// a value that is present but outside the whitelist
	formatMissing = "(missing)"
)

// settings 是 webhook 渠道的静态配置
// body 用自定义类型承载字符串与对象两种形态, 因此不能只写成 json.RawMessage
//
// settings is the static configuration of the webhook channel
// body uses a custom type to carry the string and object forms, so a plain json.RawMessage cannot
// express both
type settings struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Body    *bodySettings     `json:"body"`
	// MaskSecrets 列出必须从预览里遮蔽的地址片段, 每一项都必须是渲染后完整 URL 的子串
	// 匹配成功的片段替换为 ***, 一个都匹配不上时整个地址打成 <redacted-url> 并追加告警,
	// 因为不知道凭据在哪一段时, 显示整条地址就有泄漏风险
	//
	// MaskSecrets lists the address fragments that have to be hidden from the preview, and every
	// entry has to be a substring of the rendered full URL
	// A matching fragment becomes ***, and when nothing matches the whole address turns into
	// <redacted-url> together with a warning, since showing the entire address risks a leak when the
	// credential position is unknown
	MaskSecrets []string `json:"maskSecrets"`
}

// bodySettings 是 body 的两种形态
// isObject 在解析时置位, 因为空字符串形态与空对象都可能留下全零字段
// JSON 字段保留原始字节, 交给 common.DecodeRawJSON 用 UseNumber 解码, 大整数因此保持精度
//
// bodySettings holds the two shapes of body
// isObject is set while parsing, since an empty string form and an empty object can both leave
// every field at its zero value
// The JSON field keeps the raw bytes and is decoded by common.DecodeRawJSON with UseNumber, so a
// large integer keeps its precision
type bodySettings struct {
	Format      string            `json:"format"`
	JSON        json.RawMessage   `json:"json"`
	Text        string            `json:"text"`
	Fields      map[string]string `json:"fields"`
	ContentType string            `json:"contentType"`

	raw       string
	isObject  bool
	hasJSON   bool
	hasText   bool
	hasFields bool
}

// UnmarshalJSON 解析 body 的字符串与对象两种形态
// 对象形态用 DisallowUnknownFields 拒绝未知键; 类型不是字符串或对象时返回错误, 由 Create 包成配置错误
//
// UnmarshalJSON parses the string and the object forms of body
// The object form rejects an unknown key through DisallowUnknownFields, while a value that is
// neither a string nor an object returns an error for Create to wrap as a configuration problem
func (b *bodySettings) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	// null 的常规路径由标准库处理: settings.Body 是可置零的指针字段, 解码 null 时直接置 nil,
	// 因此 body 缺省与 body:null 都是「不发请求体」; 这里的分支只挡住非指针形式的直接解码
	//
	// The usual path for null belongs to the standard library: settings.Body is a settable pointer
	// field, so a null makes it nil, which means both an absent body and body:null mean "no request
	// body"; this branch only blocks a direct decode into a non-pointer value
	if trimmed == "null" {
		return errors.New("body must be a string or an object")
	}
	if strings.HasPrefix(trimmed, `"`) {
		var raw string
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		*b = bodySettings{raw: raw}
		return nil
	}
	if !strings.HasPrefix(trimmed, "{") {
		return errors.New("body must be a string or an object")
	}

	// 先探测键是否出现, 再用严格解码取字段值: 前者区分 "text":"" 与 text 缺失, 后者拒绝未知键
	// The keys are probed first and the strict decoder then reads the values: the probe tells
	// "text":"" apart from an absent text, while the decoder rejects an unknown key
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var obj struct {
		Format      string            `json:"format"`
		JSON        json.RawMessage   `json:"json"`
		Text        *string           `json:"text"`
		Fields      map[string]string `json:"fields"`
		ContentType *string           `json:"contentType"`
	}
	if err := dec.Decode(&obj); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("body has trailing content")
	}

	out := bodySettings{Format: obj.Format, isObject: true}
	if _, ok := probe["json"]; ok {
		out.hasJSON = true
		out.JSON = obj.JSON
	}
	if _, ok := probe["text"]; ok {
		out.hasText = true
		if obj.Text != nil {
			out.Text = *obj.Text
		}
	}
	if _, ok := probe["fields"]; ok {
		out.hasFields = true
		out.Fields = obj.Fields
	}
	if _, ok := probe["contentType"]; ok && obj.ContentType != nil {
		out.ContentType = *obj.ContentType
	}
	*b = out
	return nil
}

// factory 创建 webhook 渠道实例
//
// factory creates webhook channel instances
type factory struct{}

// init 在包加载时注册工厂, 装配包以空白导入触发
//
// init registers the factory while the package loads, which the assembly package triggers with a
// blank import
func init() { channel.Register(factory{}) }

// Type 返回注册类型名
//
// Type returns the registered type name
func (factory) Type() string { return "webhook" }

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
		{Name: "method", Type: "string", Required: false, Default: defaultMethod, DescriptionKey: keyFieldMethod},
		{Name: "headers", Type: "object", Required: false, DescriptionKey: keyFieldHeaders},
		{Name: "body", Type: "object", Required: false, DescriptionKey: keyFieldBody},
		{Name: "body.format", Type: "string", Required: false, DescriptionKey: keyFieldBodyFormat},
		{Name: "body.json", Type: "object", Required: false, DescriptionKey: keyFieldBodyJSON},
		{Name: "body.text", Type: "string", Required: false, DescriptionKey: keyFieldBodyText},
		{Name: "body.fields", Type: "object", Required: false, DescriptionKey: keyFieldBodyFields},
		{Name: "body.contentType", Type: "string", Required: false, DescriptionKey: keyFieldBodyContentType},
		{Name: "maskSecrets", Type: "stringlist", Required: false, DescriptionKey: keyFieldMaskSecrets},
	}
}

// Create 严格解码 settings 并做静态校验
// url 必填; 对象 body 必须有 format 且带对应内容键
// 静态失败一律包成 Permanent, 因为重试不改变配置判定
//
// Create strictly decodes the settings and validates them statically
// url is required, and an object body needs a format plus the matching content key
// A static failure is wrapped with Permanent, since a retry cannot change a configuration verdict
func (factory) Create(id string, raw json.RawMessage) (channel.Channel, error) {
	var s settings
	if err := channel.DecodeStrict(raw, &s); err != nil {
		return nil, apperr.Permanent(err)
	}

	if strings.TrimSpace(s.URL) == "" {
		return nil, apperr.Permanent(problem(keyErrorMissingURL, nil))
	}
	if err := validateBodyShape(s.Body); err != nil {
		return nil, apperr.Permanent(err)
	}
	// maskSecrets 的每一项都必须是渲染后完整 URL 的子串, 但静态阶段只能校验非空;
	// 真正的子串匹配留到 Prepare, 因为地址本身可能是模板
	//
	// Every maskSecrets entry has to be a substring of the rendered full URL, while the static stage
	// can only reject an empty entry; the substring match waits for Prepare, since the address itself
	// may be a template
	for _, fragment := range s.MaskSecrets {
		if strings.TrimSpace(fragment) == "" {
			return nil, apperr.Permanent(problem(keyErrorMaskEmpty, map[string]any{"Field": "settings.maskSecrets"}))
		}
	}

	return &webhookChannel{id: id, settings: s}, nil
}

// validateBodyShape 校验对象 body 的形态
// 缺省 body 与字符串 body 都不在这里判定: 前者合法, 后者的空串是允许的正文
// 对象形态要求 format 明确; json 需要 json 键, text 需要 text 键 (允许空串), form 需要 fields 键 (允许空对象)
//
// validateBodyShape validates the shape of an object body
// An absent body and a string body are not judged here: the former is valid and the empty string of
// the latter is allowed content
// The object shape requires an explicit format: json needs its json key, text needs its text key
// with an empty string allowed, and form needs its fields key with an empty object allowed
func validateBodyShape(b *bodySettings) error {
	if b == nil || !b.objectShape() {
		return nil
	}
	return bodyShapeError(b)
}

// bodyShapeError 判定对象 body 是否给出 format 以及与之匹配的内容键
// format 缺省时: 没有任何内容键报 body_empty, 有内容键却漏了 format 报 body_format 并标记缺失,
// 因为空串占位符会让文案读起来像「格式为空」而看不出该补哪个键
//
// bodyShapeError decides whether an object body gives a format plus the content key matching it
// With format absent: no content key at all reports body_empty, while content without a format
// reports body_format marked as missing, since an empty placeholder would read like "the format is
// blank" instead of showing which key has to be added
func bodyShapeError(b *bodySettings) error {
	format := strings.ToLower(strings.TrimSpace(b.Format))
	if format == "" {
		if !b.hasJSON && !b.hasText && !b.hasFields {
			return problem(keyErrorBodyEmpty, nil)
		}
		return problem(keyErrorBodyFormat, map[string]any{
			"Format":  formatMissing,
			"Allowed": allowedFormats,
		})
	}
	switch format {
	case formatJSON:
		if !b.hasJSON {
			return problem(keyErrorBodyJSONMissing, nil)
		}
	case formatText:
		if !b.hasText {
			return problem(keyErrorBodyEmpty, nil)
		}
	case formatForm:
		if !b.hasFields {
			return problem(keyErrorBodyEmpty, nil)
		}
	default:
		return problem(keyErrorBodyFormat, map[string]any{
			"Format":  b.Format,
			"Allowed": allowedFormats,
		})
	}
	return nil
}

// objectShape 报告 body 是否来自对象形态
//
// objectShape reports whether body came from the object form
func (b *bodySettings) objectShape() bool {
	return b != nil && b.isObject
}
