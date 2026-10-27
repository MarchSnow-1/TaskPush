package webhook

/*
webhook 渠道的渲染层: header 名与值的渲染和校验, 特殊头写入, 三种正文形态的构造,
以及模板错误与警告的字段路径改写
出网发送在 channel.go, 打码在 mask.go, 配置结构在 settings.go

The rendering layer of the webhook channel: rendering and validating header names and values,
writing the special headers, building the three body shapes, and rewriting the field path of a
template error or warning
Sending sits in channel.go, masking in mask.go, and the configuration shapes in settings.go
*/

import (
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// renderHeaders 渲染 header 名与值, 并检查名称合法性, 大小写重复与渲染后同名
//
// renderHeaders renders header names and values and checks name validity, case-variant duplicates
// and two keys rendering into the same name
func renderHeaders(in map[string]string, vars *template.Vars, mode template.Mode) (map[string]string, []apperr.Problem, error) {
	if len(in) == 0 {
		return map[string]string{}, nil, nil
	}
	names := make([]string, 0, len(in))
	for name := range in {
		names = append(names, name)
	}
	sort.Strings(names)

	type renderedHeader struct {
		name  string
		value string
	}
	headers := make([]renderedHeader, 0, len(names))
	folded := map[string]string{}
	var warnings []apperr.Problem
	for _, original := range names {
		name, warns, err := renderField(original, "settings.headers", vars, mode)
		if err != nil {
			return nil, warnings, err
		}
		warnings = append(warnings, warns...)
		value, warns, err := renderField(in[original], "settings.headers."+original, vars, mode)
		if err != nil {
			return nil, warnings, err
		}
		warnings = append(warnings, warns...)

		if !common.ValidHeaderName(name) {
			return nil, warnings, problem(keyErrorInvalidHeaderName, map[string]any{"Name": name})
		}
		if !common.ValidHeaderValue(value) {
			return nil, warnings, problem(keyErrorInvalidHeaderVal, map[string]any{"Name": name})
		}
		lower := strings.ToLower(name)
		if previous, ok := folded[lower]; ok {
			return nil, warnings, problem(keyErrorDuplicateHeader, map[string]any{"Name": name + ", " + previous})
		}
		folded[lower] = name
		headers = append(headers, renderedHeader{name: name, value: value})
	}

	out := make(map[string]string, len(headers))
	for _, h := range headers {
		out[http.CanonicalHeaderKey(h.name)] = h.value
	}
	return out, warnings, nil
}

// applyHeaders 写入普通头并处理特殊头
// Host 写入 req.Host; Content-Length 必须等于实际正文字节数; Transfer-Encoding 由传输层管理
//
// applyHeaders writes ordinary headers and handles the special ones
// Host goes into req.Host, Content-Length has to equal the real body length, and Transfer-Encoding
// is managed by the transport layer
func applyHeaders(req *http.Request, headers map[string]string, bodyLen int) error {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		value := headers[name]
		switch strings.ToLower(name) {
		case "host":
			if !validHost(value) {
				return problem(keyErrorSpecialHeader, map[string]any{"Name": name})
			}
			req.Host = value
		case "content-length":
			actual, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || actual != bodyLen {
				return problem(keyErrorContentLength, map[string]any{
					"Value":  value,
					"Actual": bodyLen,
				})
			}
		case "transfer-encoding":
			return problem(keyErrorTransferEncoding, nil)
		case "content-type":
			// Content-Type 在 Prepare 定稿后统一写入
			// Content-Type is written once Prepare has settled its final value
		default:
			req.Header.Set(name, value)
		}
	}
	return nil
}

// renderBody 按 body 形态渲染正文, 返回字节, 格式, 内容类型与警告
// 字符串形态固定为 text/plain; 对象形态按 format 分派; 缺省 body 返回无正文
//
// renderBody renders the body by shape and returns the bytes, format, content type and warnings
// The string form takes text/plain, the object form is dispatched by format, and an absent body
// returns no content
func renderBody(b *bodySettings, vars *template.Vars, mode template.Mode) ([]byte, string, string, []apperr.Problem, error) {
	if b == nil {
		return nil, formatNone, "", nil, nil
	}
	if !b.objectShape() {
		out, warns, err := renderField(b.raw, "settings.body", vars, mode)
		if err != nil {
			return nil, "", "", nil, err
		}
		return []byte(out), formatText, contentTypeText, warns, nil
	}

	explicitCT := strings.TrimSpace(b.ContentType) != ""
	contentType := ""
	var warnings []apperr.Problem

	// Create 已经做过同一判定, 这里再走一次是为了让绕开 Create 直接进入 Prepare 的实例也按同一规则报错
	//
	// Create ran the same decision already, so repeating it here keeps an instance that reaches
	// Prepare without Create failing by the same rule
	if err := bodyShapeError(b); err != nil {
		return nil, "", "", warnings, err
	}

	if explicitCT {
		out, warns, err := renderField(b.ContentType, "settings.body.contentType", vars, mode)
		if err != nil {
			return nil, "", "", nil, err
		}
		warnings = append(warnings, warns...)
		contentType = strings.TrimSpace(out)
	}

	format := strings.ToLower(strings.TrimSpace(b.Format))
	switch format {
	case formatJSON:
		root, err := common.DecodeRawJSON(b.JSON)
		if err != nil {
			return nil, "", "", warnings, problem(keyErrorInvalidJSONBody, map[string]any{"Detail": errorDetail(err)})
		}
		rendered, warns, err := renderJSONValue(root, "settings.body.json", vars, mode)
		if err != nil {
			return nil, "", "", warnings, err
		}
		warnings = append(warnings, warns...)
		out, err := common.MarshalNoHTMLEscape(rendered)
		if err != nil {
			return nil, "", "", warnings, err
		}
		if !explicitCT {
			contentType = contentTypeJSON
		}
		return out, formatJSON, contentType, warnings, nil
	case formatText:
		out, warns, err := renderField(b.Text, "settings.body.text", vars, mode)
		if err != nil {
			return nil, "", "", warnings, err
		}
		warnings = append(warnings, warns...)
		if !explicitCT {
			contentType = contentTypeText
		}
		return []byte(out), formatText, contentType, warnings, nil
	case formatForm:
		form := url.Values{}
		keys := make([]string, 0, len(b.Fields))
		for key := range b.Fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value, warns, err := renderField(b.Fields[key], "settings.body.fields."+key, vars, mode)
			if err != nil {
				return nil, "", "", warnings, err
			}
			warnings = append(warnings, warns...)
			form.Set(key, value)
		}
		if !explicitCT {
			contentType = contentTypeForm
		}
		return []byte(form.Encode()), formatForm, contentType, warnings, nil
	default:
		return nil, "", "", warnings, problem(keyErrorBodyFormat, map[string]any{
			"Format":  b.Format,
			"Allowed": allowedFormats,
		})
	}
}

// renderJSONValue 递归渲染 JSON 树的字符串叶子, 非字符串叶子原样保留
//
// renderJSONValue recursively renders the string leaves of a JSON tree and leaves non-string leaves
// as they are
func renderJSONValue(value any, path string, vars *template.Vars, mode template.Mode) (any, []apperr.Problem, error) {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var warnings []apperr.Problem
		for _, key := range keys {
			rendered, warns, err := renderJSONValue(v[key], path+"."+key, vars, mode)
			if err != nil {
				return nil, warnings, err
			}
			warnings = append(warnings, warns...)
			out[key] = rendered
		}
		return out, warnings, nil
	case []any:
		out := make([]any, 0, len(v))
		var warnings []apperr.Problem
		for i, item := range v {
			rendered, warns, err := renderJSONValue(item, path+"."+strconv.Itoa(i), vars, mode)
			if err != nil {
				return nil, warnings, err
			}
			warnings = append(warnings, warns...)
			out = append(out, rendered)
		}
		return out, warnings, nil
	case string:
		rendered, warns, err := template.RenderJSONLeaf(v, vars, mode)
		if err != nil {
			rewritePath(err, path)
			return nil, nil, err
		}
		for i := range warns {
			warns[i] = webhookWarning(warns[i], path)
		}
		return rendered, warns, nil
	default:
		return value, nil, nil
	}
}

// renderField 渲染一个 settings 字符串字段, 并把模板错误与警告的 Path 改写为该字段路径
// check 模式下未解析变量的警告换成 webhook 自己的键, Data 带 Field 与 Snippet
//
// renderField renders one string settings field and rewrites the Path of a template error or
// warning into that field path
// Under check mode the unresolved-variable warning takes the webhook key, with Field and Snippet in
// Data
func renderField(value, field string, vars *template.Vars, mode template.Mode) (string, []apperr.Problem, error) {
	out, warns, err := template.RenderWithMode(value, vars, mode)
	if err != nil {
		rewritePath(err, field)
		return "", nil, err
	}
	for i := range warns {
		warns[i] = webhookWarning(warns[i], field)
	}
	return out, warns, nil
}

// webhookWarning 把模板层的警告适配成 webhook 的键
// template.warn.unresolved 换成 channel.webhook.warn.unresolved, 其余警告只改写 Path
//
// webhookWarning adapts a template-layer warning to the webhook keys
// template.warn.unresolved becomes channel.webhook.warn.unresolved, while any other warning only
// has its Path rewritten
func webhookWarning(warn apperr.Problem, field string) apperr.Problem {
	if warn.ID == "template.warn.unresolved" {
		variable, _ := warn.Data["Variable"].(string)
		return apperr.Problem{
			ID: keyWarnUnresolved,
			Data: map[string]any{
				"Field":   field,
				"Snippet": unresolvedSnippet(variable),
			},
		}
	}
	setPath(&warn, field)
	return warn
}

// rewritePath 把错误链上第一个模板 Problem 的 Path 改写为字段路径
//
// rewritePath rewrites the Path of the first template Problem on the error chain into a field path
func rewritePath(err error, field string) {
	var prob *apperr.Problem
	if errors.As(err, &prob) {
		setPath(prob, field)
	}
}

// setPath 覆写 Problem.Data 里的 Path, Data 为 nil 时先建表
//
// setPath overwrites Path inside Problem.Data, creating the map when it is nil
func setPath(prob *apperr.Problem, field string) {
	if prob == nil {
		return
	}
	if prob.Data == nil {
		prob.Data = map[string]any{}
	}
	prob.Data["Path"] = field
}

// unresolvedSnippet 返回未解析变量片段, 供 webhook 自己的警告键使用
//
// unresolvedSnippet returns the snippet of an unresolved variable for the webhook warning key
func unresolvedSnippet(variable string) string {
	if variable == "" {
		return ""
	}
	return "{{" + variable + "}}"
}

// headerValue 按大小写不敏感的名字读取 header 值, 同时报告是否存在
//
// headerValue reads a header value by a case-insensitive name and reports whether it was present
func headerValue(headers map[string]string, name string) (string, bool) {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value, true
		}
	}
	return "", false
}

// isJSONContentType 判断最终内容类型是否为 JSON, 只比较媒体类型并忽略参数
//
// isJSONContentType reports whether the final content type is JSON, comparing the media type and
// ignoring its parameters
func isJSONContentType(contentType string) bool {
	mediaType := strings.TrimSpace(contentType)
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = mediaType[:i]
	}
	return strings.EqualFold(strings.TrimSpace(mediaType), "application/json")
}

// validHost 校验 Host 值: 非空, 不含空白, 控制字符或 "/"
// 非 ASCII 允许, net/http 在写出前会按 IDNA 处理
//
// validHost checks a Host value: non-empty and free of whitespace, control characters and "/"
// Non-ASCII is allowed, since net/http applies IDNA before writing it
func validHost(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b >= 0x80 {
			continue
		}
		if b <= 0x20 || b == 0x7f || b == '/' {
			return false
		}
	}
	return true
}
