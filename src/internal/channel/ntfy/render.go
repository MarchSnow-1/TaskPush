package ntfy

/*
ntfy 渠道的渲染层: 单字段渲染, priority 的区间判定, tags 的切分, header 取值校验,
以及地址到 topic 的解析
出网发送在 channel.go, 打码与 Problem 构造在 mask.go, 配置结构在 settings.go

The rendering layer of ntfy: rendering one field, the priority range check, cutting the tags into
items, validating the header values, and resolving the address into a topic
Sending sits in channel.go, masking and Problem construction in mask.go, and the configuration shapes
in settings.go
*/

import (
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// renderPriority 渲染并校验 priority, 返回进入 header 的文本
// 未显式配置时返回空串, 表示不发 Priority header; 渲染后为空同样不发, 与其余可选字段的空值处理一致
// 取值必须落在 1-5 或五个枚举名之内, 枚举名比较大小写不敏感
//
// renderPriority renders and checks priority and returns the text going into the header
// An unset field returns an empty string, which sends no Priority header, and so does a value that
// renders empty, matching how the other optional fields treat emptiness
// The value has to sit inside 1-5 or be one of the five enum names, compared case-insensitively
func (c *ntfyChannel) renderPriority(vars *template.Vars, mode template.Mode) (string, []apperr.Problem, error) {
	if !c.s.Priority.Given {
		return "", nil, nil
	}
	rendered, warns, err := renderField(c.s.Priority.Text, settingsPathPriority, vars, mode)
	if err != nil {
		return "", nil, err
	}
	value := strings.TrimSpace(rendered)
	if value == "" {
		return "", warns, nil
	}
	if n, err := strconv.Atoi(value); err == nil {
		if n < priorityMin || n > priorityMax {
			return "", warns, problem(keyErrorInvalidPriority, map[string]any{"Value": displayValue(value)})
		}
		return value, warns, nil
	}
	if priorityEnums[strings.ToLower(value)] {
		return value, warns, nil
	}
	return "", warns, problem(keyErrorInvalidPriority, map[string]any{"Value": displayValue(value)})
}

// renderTags 逐项渲染 tags, 再按逗号类分隔符切分, 去空白与空项, 用半角逗号连接
// 变量可以整体提供列表: "{{tags}}" 渲染成 "ci,build" 后仍被切成两项
//
// renderTags renders the tags item by item, cuts every item on the comma-like separators, drops
// whitespace and empty items and joins the rest with an ASCII comma
// A variable may supply the whole list, since "{{tags}}" rendering into "ci,build" is still cut
// into two items
func (c *ntfyChannel) renderTags(vars *template.Vars, mode template.Mode) (string, []apperr.Problem, error) {
	var warnings []apperr.Problem
	parts := make([]string, 0, len(c.s.Tags))
	for i, configured := range c.s.Tags {
		rendered, warns, err := renderField(configured, settingsPathTags+"."+strconv.Itoa(i), vars, mode)
		if err != nil {
			return "", warnings, err
		}
		warnings = append(warnings, warns...)
		parts = append(parts, splitCommaSeparated(rendered)...)
	}
	return strings.Join(parts, ","), warnings, nil
}

// splitCommaSeparated 按半角逗号, 全角逗号与顿号切分, 去空白与空项
// 这三个分隔符与 settings.StringList 在 Create 阶段识别的集合相同, 因此字符串写法与数组写法归一后一致
//
// splitCommaSeparated cuts on the ASCII comma, the full-width comma and the ideographic comma,
// dropping whitespace and empty items
// The three match what settings.StringList recognises back in Create, so a delimited string and an
// array normalise into the same list
func splitCommaSeparated(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', '，', '、':
			return true
		}
		return false
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// invalidHeaderName 检查 header 取值, 返回第一个不合法的名字
// CR, LF, NUL 与 DEL 都会被 net/http 在写出时拒绝, 头部注入也因此断在这里
// ok 为 true 表示全部合法, 此时 name 为空
//
// invalidHeaderName checks the header values and returns the first name that fails
// CR, LF, NUL and DEL are all refused while net/http writes a request, which is also where a header
// injection is cut short
// A true ok means every value passed, in which case name is empty
func invalidHeaderName(headers map[string]string) (string, bool) {
	for _, name := range sortedHeaderNames(headers) {
		if !common.ValidHeaderValue(headers[name]) {
			return name, false
		}
	}
	return "", true
}

// sortedHeaderNames 返回排序后的 header 名, 让报错取到的名字在同一次输入下稳定
//
// sortedHeaderNames returns the sorted header names, which keeps the name an error reports stable
// for one input
func sortedHeaderNames(headers map[string]string) []string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// topicEndpoint 规范化地址并取出 topic
// 地址先经 common.NormalizeURL 补 scheme 与校验, 再用 url.Parse 取 path 末段
// path 为空或只有分隔符时没有 topic, 报 missing_topic, 因为 ntfy 把订阅目标写在 path 里
//
// topicEndpoint normalises the address and reads the topic out of it
// common.NormalizeURL completes the scheme and checks the form first, then url.Parse supplies the
// last path segment
// An empty path, or one holding separators alone, carries no topic, so missing_topic is reported,
// since ntfy puts the subscription target in the path
func topicEndpoint(raw string) (string, string, error) {
	endpoint, err := common.NormalizeURL(raw)
	if err != nil {
		return "", "", invalidURLProblem(raw, err)
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", "", invalidURLProblem(raw, err)
	}
	topic := lastPathSegment(u.EscapedPath())
	if topic == "" {
		return "", "", problem(keyErrorMissingTopic, nil)
	}
	return endpoint, topic, nil
}

// lastPathSegment 取 path 最后一个非空段, 末尾斜杠与重复斜杠都不算 topic
//
// lastPathSegment returns the last non-empty segment of a path, where a trailing slash and a
// repeated slash neither count as the topic
func lastPathSegment(path string) string {
	trimmed := strings.TrimRight(path, "/")
	idx := strings.LastIndexByte(trimmed, '/')
	if idx < 0 {
		return trimmed
	}
	return trimmed[idx+1:]
}

// renderField 渲染一个 settings 字符串字段
// 模板错误与告警里的 Path 先被改写为该字段路径, 因为模板引擎只填占位路径
//
// renderField renders one settings string field
// The Path inside a template error or warning is rewritten to this field path first, because the
// template engine only fills a placeholder path
func renderField(value, field string, vars *template.Vars, mode template.Mode) (string, []apperr.Problem, error) {
	out, warns, err := template.RenderWithMode(value, vars, mode)
	if err != nil {
		rewritePath(err, field)
		return "", nil, err
	}
	for i := range warns {
		setPath(&warns[i], field)
	}
	return out, warns, nil
}

// rewritePath 把 err 链上的模板 Problem 的 Path 改写为字段路径
//
// rewritePath rewrites the Path of the template Problem on the err chain into a field path
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
