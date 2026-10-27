package channel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// ---------------------------------------------------------------------------
// 严格解码: 渠道 settings 的 JSON 只接受已知字段, 未知字段按错误处理
// 拒绝未知字段能及早暴露配置拼写错误, 避免渠道拿到一个静默缺失的字段
//
// Strict decoding: channel settings accept known fields only and treat an unknown one as an
// error
// Rejecting unknown fields surfaces a configuration typo early instead of leaving a field
// silently unset for the channel
// ---------------------------------------------------------------------------

// invalidSettingsProblem 把解码失败包装成用户可见的 Problem
// Field 是未写字段的顶层键或错误发生位置, Detail 是不含配置原文的技术细节
//
// invalidSettingsProblem wraps a decoding failure into a user-visible Problem
// Field is the top-level key of an unknown field or the position of the error, and Detail is
// the technical detail without the configuration text
func invalidSettingsProblem(field, detail string, cause error) *apperr.Problem {
	return &apperr.Problem{
		ID: "channel.error.invalid_settings",
		Data: map[string]any{
			"Field":  field,
			"Detail": detail,
		},
		Cause: cause,
	}
}

// unknownField 从解码错误文本里提取未知字段的顶层键
// encoding/json 的错误形如 json: unknown field "name", 带引号的名字是最后一个错误字段
// 不匹配时返回空串, 由文案以空占位符处理
//
// unknownField extracts the top-level key of an unknown field from the decoding error text
// The encoding/json error reads json: unknown field "name", where the quoted name is the last
// field rejected
// A mismatch returns an empty string, which the message leaves as an empty placeholder
func unknownField(err error) string {
	var prefix = "json: unknown field "
	msg := err.Error()
	if !strings.HasPrefix(msg, prefix) {
		return ""
	}
	name := strings.TrimPrefix(msg, prefix)
	return strings.Trim(name, `"`)
}

// DecodeStrict 把 settings JSON 解码到 target
// raw 为空按空对象处理, 未知字段报错, 顶层必须是 JSON 对象且不允许尾随内容
// 失败返回 *apperr.Problem, ID 为 channel.error.invalid_settings, Data 带 Field 与 Detail
//
// DecodeStrict decodes the settings JSON into target
// An empty raw is treated as an empty object, unknown fields fail, and the top level has to be
// one JSON object with no trailing content
// A failure returns an *apperr.Problem with the ID channel.error.invalid_settings and Data
// carrying Field and Detail
func DecodeStrict(raw json.RawMessage, target any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return invalidSettingsProblem(unknownField(err), err.Error(), err)
	}

	// 顶层只接受一个 JSON 对象: 尾随内容 (如 {"a":1} {"b":2}) 显式报错而不是静默丢弃
	// Only one JSON object is accepted at the top level, so trailing content such as
	// {"a":1} {"b":2} fails explicitly rather than being dropped silently
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return invalidSettingsProblem("", "trailing content after the JSON object", errTrailing)
	}

	// 顶层必须是对象: 数组, 标量与 null 也能解码进结构体指针 target 且不报错
	// (null 是标准库的显式 no-op), 所以这里用 map 探测明确拒绝
	//
	// The top level has to be an object, since an array, a scalar or null can still decode into
	// a struct pointer target without an error (null is an explicit no-op in the standard
	// library), so a map probe rejects them here
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil || probe == nil {
		return invalidSettingsProblem("", "the top level must be a JSON object", err)
	}
	return nil
}

// errTrailing 是尾随内容的内部哨兵, 让尾随分支返回的 Cause 与解码错误可区分
//
// errTrailing is the internal sentinel for trailing content, keeping the Cause of that branch
// distinguishable from a decoding error
var errTrailing = fmt.Errorf("json: trailing content")
