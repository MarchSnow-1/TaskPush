package config

import (
	"bytes"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/settings"
)

// ---------------------------------------------------------------------------
// 配置校验: 把原始 JSON 值解析成模型字段, 并把每一种失败映射到固定的消息键
// 这里不调用渠道工厂, settings 只做对象形状检查, 具体 schema 留给创建期
//
// Configuration validation: raw JSON values are parsed into model fields and every kind of
// failure maps to a fixed message key
// Channel factories are not called here, and settings gets only an object-shape check while the
// concrete schema stays with creation time
// ---------------------------------------------------------------------------

// supportedVersion 是目前唯一支持的配置版本
//
// supportedVersion is the only configuration version supported at present
const supportedVersion = 1

// idPattern 约束渠道 ID: 字母或数字开头, 之后是字母, 数字, 点, 下划线或连字符, 最长 64
//
// idPattern constrains a channel ID: it starts with a letter or digit, continues with letters,
// digits, dots, underscores or hyphens, and is at most 64 characters long
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// topLevelFields 是顶层允许出现的键, 用于未知键报错与拼写建议
//
// topLevelFields holds the keys allowed at the top level, serving unknown-field errors and
// spelling suggestions
var topLevelFields = []string{"version", "$schema", "timeout", "retries", "retryDelay", "proxy", "channels"}

// channelFields 是渠道条目允许出现的公共键
//
// channelFields holds the common keys allowed inside a channel entry
var channelFields = []string{"id", "type", "enabled", "timeout", "retries", "retryDelay", "settings"}

// newProblem 构造一个带消息键的 Problem
//
// newProblem builds a Problem carrying a message key
func newProblem(id string, data map[string]any, cause error) *apperr.Problem {
	return &apperr.Problem{ID: id, Data: data, Cause: cause}
}

// unknownFieldProblem 构造未知字段错误, 并给出最接近的已知字段建议
//
// unknownFieldProblem builds an unknown-field error and offers the closest known field as a
// suggestion
func unknownFieldProblem(path, field string, known []string) *apperr.Problem {
	return newProblem("config.error.unknown_field", map[string]any{
		"Path":       fieldPath(path, field),
		"Field":      field,
		"Suggestion": suggestField(field, known),
	}, nil)
}

// typeMismatchProblem 构造字段类型错误
//
// typeMismatchProblem builds a field-type error
func typeMismatchProblem(path, field, expected string) *apperr.Problem {
	return newProblem("config.error.type_mismatch", map[string]any{
		"Path":     fieldPath(path, field),
		"Field":    field,
		"Expected": expected,
	}, nil)
}

// fieldPath 拼出字段的完整路径, 路径为空时直接返回字段名
//
// fieldPath joins the full path of a field and returns the field name when the path is empty
func fieldPath(path, field string) string {
	if path == "" {
		return field
	}
	return path + "." + field
}

// sortedKeys 返回 map 的键并按字典序排列, 让多字段错误的选择不依赖遍历顺序
//
// sortedKeys returns the keys of a map in lexicographic order, which keeps the choice among
// several bad fields independent of map iteration order
func sortedKeys(fields map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// objectFields 把原始值解码成 JSON 对象
// 非对象返回 type_mismatch, 已在别处的对象可直接用 json.Unmarshal 到 map
//
// objectFields decodes a raw value into a JSON object
// A non-object returns type_mismatch, and an object already checked elsewhere can be decoded
// into a map directly
func objectFields(path string, raw json.RawMessage) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, typeMismatchProblem(path, lastSegment(path), "object")
	}
	return fields, nil
}

// lastSegment 返回路径的最后一段, 供 Field 占位符使用
//
// lastSegment returns the final segment of a path for the Field placeholder
func lastSegment(path string) string {
	if at := strings.LastIndexByte(path, '.'); at >= 0 {
		return path[at+1:]
	}
	return path
}

// stringField 读取一个 JSON 字符串字段
//
// stringField reads a JSON string field
func stringField(path, field string, raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", typeMismatchProblem(path, field, "string")
	}
	return value, nil
}

// intField 读取一个整数, 接受 JSON 数字与十进制字符串
// 字符串形式经 settings.Int 解析, 因此 " 3 " 可用而 "3.5" 不可用
//
// intField reads an integer and accepts a JSON number or a decimal string
// The string form goes through settings.Int, so " 3 " works while "3.5" does not
func intField(path, field string, raw json.RawMessage) (int, error) {
	var value settings.Int
	if err := value.UnmarshalJSON(raw); err != nil {
		return 0, newProblem("config.error.int", map[string]any{
			"Path":  fieldPath(path, field),
			"Value": rawText(raw),
		}, err)
	}
	return value.Int(), nil
}

// boolField 读取一个布尔值, 接受 JSON 布尔与 true/false/1/0 字符串
//
// boolField reads a boolean and accepts a JSON boolean or the strings true, false, 1 and 0
func boolField(path, field string, raw json.RawMessage) (bool, error) {
	var value settings.Bool
	if err := value.UnmarshalJSON(raw); err != nil {
		return false, newProblem("config.error.bool", map[string]any{
			"Path":  fieldPath(path, field),
			"Value": rawText(raw),
		}, err)
	}
	return value.Bool(), nil
}

// durationField 读取一个时长字符串
// 解析失败返回 config.error.duration, 范围校验由调用方按字段语义继续做
//
// durationField reads a duration string
// A parse failure returns config.error.duration, and range validation continues in the caller
// according to the field meaning
func durationField(path, field string, raw json.RawMessage) (settings.Duration, error) {
	var value settings.Duration
	if err := value.UnmarshalJSON(raw); err != nil {
		return 0, newProblem("config.error.duration", map[string]any{
			"Path":  fieldPath(path, field),
			"Value": rawText(raw),
		}, err)
	}
	return value, nil
}

// validateVersion 校验配置版本
// 0 表示字段缺省, 由调用方填成 1; 其它不等于 1 的值返回 config.error.version
//
// validateVersion checks the configuration version
// Zero means the field was absent and the caller fills 1, while any value other than 1 returns
// config.error.version
func validateVersion(version int) error {
	if version == 0 || version == supportedVersion {
		return nil
	}
	return newProblem("config.error.version", map[string]any{
		"Version":   version,
		"Supported": itoa(supportedVersion),
	}, nil)
}

// validateChannelID 校验渠道 ID 的字符集与长度
//
// validateChannelID checks the character set and length of a channel ID
func validateChannelID(path, id string) error {
	if !idPattern.MatchString(id) {
		return newProblem("config.error.id_invalid", map[string]any{
			"Path": fieldPath(path, "id"),
			"ID":   id,
		}, nil)
	}
	return nil
}

// validateChannelType 校验渠道类型是否已注册
//
// validateChannelType checks whether a channel type is registered
func validateChannelType(path, typ string) error {
	if channel.IsRegistered(typ) {
		return nil
	}
	return newProblem("config.error.type_unknown", map[string]any{
		"Path":      fieldPath(path, "type"),
		"Type":      typ,
		"Available": strings.Join(channel.RegisteredTypes(), ", "),
	}, nil)
}

// validateSettingsObject 校验 settings 是非 null 的 JSON 对象
// 空对象合法, 数组, 字符串, 数字与 null 都返回 config.error.settings_not_object
//
// validateSettingsObject checks that settings is a JSON object other than null
// An empty object is valid, while an array, a string, a number and null return
// config.error.settings_not_object
func validateSettingsObject(path string, raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return newProblem("config.error.settings_not_object", map[string]any{"Path": path}, nil)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil || fields == nil {
		return newProblem("config.error.settings_not_object", map[string]any{"Path": path}, err)
	}
	return nil
}

// validateProxy 校验 proxy 的三态取值
// 空串表示直连, env 表示沿用环境代理, http 与 https URL 表示显式代理, socks5 与其它写法被拒绝
//
// validateProxy checks the three accepted proxy forms
// An empty string means a direct connection, env reuses the environment proxy, and an http or
// https URL selects an explicit proxy, while socks5 and other forms are rejected
func validateProxy(value string) error {
	trimmed := strings.TrimSpace(value)
	switch trimmed {
	case "", "env":
		return nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return newProblem("config.error.proxy", map[string]any{"Value": maskProxy(trimmed)}, err)
	}
	return nil
}

// maskProxy 隐藏代理 URL 里的用户信息, 其余部分保留
// 没有任何用户信息时原样返回, 因此常规主机的报错仍能显示是哪一条代理
//
// maskProxy hides the user information of a proxy URL and keeps the rest
// A URL without user information comes back unchanged, so an error about a plain host still
// shows which proxy it meant
func maskProxy(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.User == nil {
		return value
	}
	parsed.User = url.User("***")
	return parsed.String()
}

// rawText 把一个原始 JSON 值转成适合放进错误的短文本
// JSON 字符串取其内容, 其它值取紧凑文本; 过长时截断, 避免错误载荷里带整段配置
//
// rawText turns a raw JSON value into a short text suitable for an error
// A JSON string yields its content and another value yields compact text, and an overlong value
// is truncated so an error payload does not carry a whole configuration fragment
func rawText(raw json.RawMessage) string {
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return truncateText(value)
	}
	return truncateText(strings.TrimSpace(string(raw)))
}

// truncateText 把文本限制在 80 个字符以内
//
// truncateText limits a text to 80 characters
func truncateText(value string) string {
	const limit = 80
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

// suggestField 在已知字段里找一个与拼写最接近的名字
// 距离超过 2 时返回空串, 避免给出无意义的建议
//
// suggestField finds the closest known field name to a misspelling
// It returns an empty string when the distance is above 2, which avoids a meaningless suggestion
func suggestField(field string, known []string) string {
	best := ""
	bestDistance := 3
	lowered := strings.ToLower(field)
	for _, candidate := range known {
		distance := editDistance(lowered, strings.ToLower(candidate))
		if distance < bestDistance {
			bestDistance = distance
			best = candidate
		}
	}
	return best
}

// editDistance 计算两个字符串的 Levenshtein 距离
//
// editDistance computes the Levenshtein distance between two strings
func editDistance(left, right string) int {
	a := []rune(left)
	b := []rune(right)
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min3(current[j-1]+1, previous[j]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}

// min3 返回三个整数里的最小值
//
// min3 returns the smallest of three integers
func min3(a, b, c int) int {
	smallest := a
	if b < smallest {
		smallest = b
	}
	if c < smallest {
		smallest = c
	}
	return smallest
}
