package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// ---------------------------------------------------------------------------
// JSON helper: 渠道构造请求体与解析响应时共用的两个小工具
// MarshalNoHTMLEscape 关掉标准库对 < > & 的转义, 因为平台侧把正文当文本看, 转义成 \u003c 会让
// 用户看到的文案变形
// DecodeRawJSON 用 UseNumber 解码, 让大整数与小数保持原文精度, 不被 float64 舍入
//
// The JSON helpers two channels share while building a request body and reading a response
// MarshalNoHTMLEscape turns off the standard library escaping of < > &, because the receiving
// platform shows the text to a user and \u003c would distort the copy
// DecodeRawJSON decodes with UseNumber so a large integer or a decimal keeps the precision it was
// written with instead of being rounded through float64
// ---------------------------------------------------------------------------

const keyErrorBadJSON = "common.error.bad_json"

// MarshalNoHTMLEscape 序列化 v 且不转义 HTML 字符, 末尾的换行会被去掉
// 调用方需要默认转义时直接用 json.Marshal
//
// MarshalNoHTMLEscape serialises v without escaping HTML characters and strips the trailing
// newline the encoder appends
// A caller who wants the default escaping uses json.Marshal directly
func MarshalNoHTMLEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// DecodeRawJSON 把 json.RawMessage 解成通用值 (map/slice/string/float64 家族之外的 json.Number/bool/nil)
// 空 raw (缺失或纯空白) 按 {} 处理, 尾随内容 ("1 2", "{}x") 报错, 错误是 *apperr.Problem,
// ID 为 common.error.bad_json, Data 带 Field 与脱敏后的 Detail
//
// DecodeRawJSON turns a json.RawMessage into a generic value (a map, slice, string, json.Number,
// bool or nil)
// An empty raw, missing or whitespace-only, counts as {}, while trailing content such as "1 2" or
// "{}x" is an error returned as an *apperr.Problem with the ID common.error.bad_json and Data
// carrying the Field and a redacted Detail
func DecodeRawJSON(raw json.RawMessage) (any, error) {
	return decodeRawJSON(raw, "body")
}

// decodeRawJSON 是 DecodeRawJSON 的实现, field 供错误定位
//
// decodeRawJSON is the implementation of DecodeRawJSON, where field locates the error
func decodeRawJSON(raw json.RawMessage, field string) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, badJSONProblem(field, err.Error(), err)
	}
	// 只接受一个 JSON 值: 第二个值 (如 `1 2`) 在这里被读出来并报错, 而不是静默丢弃
	// One JSON value only, so a second one such as `1 2` surfaces here rather than being dropped
	if err := dec.Decode(&value); err != io.EOF {
		return nil, badJSONProblem(field, "trailing content after the JSON value",
			fmt.Errorf("json: trailing content"))
	}
	// JSON null 是合法值, 原样返回 nil, 由调用方决定怎么处理, 这里不把它当错误
	// A JSON null is a legal value, so nil comes back as it is for the caller to decide about
	// rather than being treated as an error here
	return value, nil
}

// badJSONProblem 构造 JSON 解码错误, Detail 先过 SanitizeString 防止地址进 Data
//
// badJSONProblem builds the JSON decoding error, with Detail passed through SanitizeString so an
// address cannot reach Data
func badJSONProblem(field, detail string, cause error) *apperr.Problem {
	if cause != nil {
		cause = SanitizeError(cause)
	}
	return &apperr.Problem{
		ID: keyErrorBadJSON,
		Data: map[string]any{
			"Field":  field,
			"Detail": SanitizeString(detail),
		},
		Cause: cause,
	}
}
