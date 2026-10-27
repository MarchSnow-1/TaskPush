package config

import (
	"bytes"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// 环境变量展开: 只处理 JSON 字符串叶子, 支持 ${VAR}, ${VAR:-默认值} 与 $${ 转义
// 展开结果按构造顺序写入新字符串, 不再递归展开, 因此 ${VAR} 的值里含 ${OTHER} 时保持字面量
//
// Environment-variable expansion: it walks JSON string leaves only and supports ${VAR},
// ${VAR:-default} and the $${ escape
// The result is written into a new string in one pass and is not expanded again, so a value
// containing ${OTHER} stays literal
// ---------------------------------------------------------------------------

// envNameRE 约束环境变量名: 字母或下划线开头, 之后是字母, 数字或下划线
//
// envNameRE constrains an environment variable name: a letter or underscore first, then
// letters, digits or underscores
var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// envLookup 读取一个环境变量, 第二个返回值表示变量是否存在
//
// envLookup reads one environment variable, with the second value reporting whether it exists
type envLookup func(string) (string, bool)

// expandString 展开一个字符串里的环境变量引用
// 未加花括号的 $VAR 保持字面量, 只有 ${VAR} 与 ${VAR:-默认值} 参与展开
// 未设置且没有默认值时返回 config.error.env_unset, 变量名, 括号或默认值写法有问题时返回对应的键
//
// expandString expands the environment-variable references inside one string
// A bare $VAR stays literal, and only ${VAR} and ${VAR:-default} take part in expansion
// An unset variable without a default returns config.error.env_unset, while a bad name, an
// unclosed brace or a bad default returns the matching key
func expandString(source, path, raw string, lookup envLookup) (string, error) {
	var out strings.Builder
	out.Grow(len(raw))

	for i := 0; i < len(raw); {
		if strings.HasPrefix(raw[i:], "$${") {
			out.WriteString("${")
			i += 3
			continue
		}
		if strings.HasPrefix(raw[i:], "${") {
			rest := raw[i+2:]
			closeAt := strings.IndexByte(rest, '}')
			if closeAt < 0 {
				return "", newProblem("config.error.env_unclosed", map[string]any{"Path": path}, nil)
			}
			inner := rest[:closeAt]
			i += 2 + closeAt + 1

			name, fallback, hasFallback := splitEnvDefault(inner)
			if !envNameRE.MatchString(name) {
				return "", newProblem("config.error.env_name", map[string]any{"Path": path, "Name": name}, nil)
			}
			if hasFallback {
				decoded, err := decodeEnvDefault(path, fallback)
				if err != nil {
					return "", err
				}
				if value, ok := lookup(name); ok && value != "" {
					out.WriteString(value)
				} else {
					out.WriteString(decoded)
				}
				continue
			}
			value, ok := lookup(name)
			if !ok {
				return "", newProblem("config.error.env_unset", map[string]any{
					"Source":   source,
					"Path":     path,
					"Variable": name,
				}, nil)
			}
			out.WriteString(value)
			continue
		}
		out.WriteByte(raw[i])
		i++
	}
	return out.String(), nil
}

// ExpandEnvString 展开一段自由文本里的环境变量引用, 供 CLI 的 --title / --body 使用
// 规则与配置里的字符串一致: 支持 ${VAR}, ${VAR:-默认值} 与 $${ 转义, 未加花括号的 $VAR 保持字面量
// source 与 path 只用于错误定位, 变量未设置且没有默认值时返回 config.error.env_unset
//
// ExpandEnvString expands the environment-variable references inside one piece of free text, which
// the CLI uses for --title and --body
// The rules match a configuration string: ${VAR}, ${VAR:-default} and the $${ escape are supported,
// while a bare $VAR stays literal
// The source and path only locate an error, and an unset variable without a default returns
// config.error.env_unset
func ExpandEnvString(source, path, raw string, lookup func(string) (string, bool)) (string, error) {
	return expandString(source, path, raw, lookup)
}

// splitEnvDefault 把 ${...} 的内容按第一处 ":-" 切成变量名与默认值
// 没有 ":-" 时 hasFallback 为 false, 默认值本身可以再包含 ":-"
//
// splitEnvDefault splits the content of ${...} at the first ":-" into a name and a default
// Without ":-" the hasFallback result is false, and a default may itself contain ":-" again
func splitEnvDefault(inner string) (name, fallback string, hasFallback bool) {
	if at := strings.Index(inner, ":-"); at >= 0 {
		return inner[:at], inner[at+2:], true
	}
	return inner, "", false
}

// decodeEnvDefault 处理默认值里的 $${ 转义, 并拒绝未转义的嵌套 ${
// 默认值不做环境变量查询, 因此 ${VAR:-${OTHER}} 属于写法错误而不是再去读 OTHER
//
// decodeEnvDefault handles the $${ escape inside a default and rejects a nested unescaped ${
// A default performs no environment lookup, so ${VAR:-${OTHER}} is a syntax error rather than a
// second lookup
func decodeEnvDefault(path, raw string) (string, error) {
	var out strings.Builder
	out.Grow(len(raw))
	for i := 0; i < len(raw); {
		if strings.HasPrefix(raw[i:], "$${") {
			out.WriteString("${")
			i += 3
			continue
		}
		if strings.HasPrefix(raw[i:], "${") {
			return "", newProblem("config.error.env_nested", map[string]any{"Path": path}, nil)
		}
		out.WriteByte(raw[i])
		i++
	}
	return out.String(), nil
}

// expandJSON 解析一段 JSON 并展开其中所有字符串叶子
// 数字与布尔原样保留, 对象键名不展开, 解析失败按 JSON 语法错误返回
//
// expandJSON parses one JSON value and expands every string leaf inside it
// Numbers and booleans stay as they are, object keys are not expanded, and a parse failure
// returns the JSON syntax error key
func expandJSON(source, path string, raw json.RawMessage, lookup envLookup) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, newProblem("config.error.json", map[string]any{
			"Source": source,
			"Detail": err.Error(),
		}, err)
	}
	expanded, err := expandValue(source, path, value, lookup)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(expanded)
	if err != nil {
		return nil, newProblem("config.error.json", map[string]any{
			"Source": source,
			"Detail": err.Error(),
		}, err)
	}
	return out, nil
}

// expandValue 递归展开一个已解析 JSON 值里的字符串
// 数组路径使用 [i], 对象路径使用点号, 错误里因此能定位到具体的叶子字段
//
// expandValue recursively expands the strings of one parsed JSON value
// An array path uses [i] and an object path uses a dot, so an error can point at the exact
// leaf field
func expandValue(source, path string, value any, lookup envLookup) (any, error) {
	switch typed := value.(type) {
	case string:
		return expandString(source, path, typed, lookup)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			expanded, err := expandValue(source, childIndexPath(path, i), item, lookup)
			if err != nil {
				return nil, err
			}
			out[i] = expanded
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(typed))
		// 键按字典序遍历, 让多个字符串同时展开失败时的首条错误路径不依赖 map 遍历顺序
		// Keys are walked in lexicographic order, which keeps the first failing path from
		// depending on map iteration order when several strings fail at once
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			expanded, err := expandValue(source, childFieldPath(path, key), typed[key], lookup)
			if err != nil {
				return nil, err
			}
			out[key] = expanded
		}
		return out, nil
	default:
		return value, nil
	}
}

// childFieldPath 拼出对象字段的路径
//
// childFieldPath joins the path of an object field
func childFieldPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// childIndexPath 拼出数组元素的路径
//
// childIndexPath joins the path of an array element
func childIndexPath(path string, index int) string {
	return path + "[" + itoa(index) + "]"
}

// itoa 把整数转成十进制文本, 只服务于路径拼接与标量归一
// 负号单独处理, 因为逐位取模的循环只适用于正数
//
// itoa renders an integer in decimal, serving path building and scalar normalisation
// The sign is handled separately, since the digit loop only covers positive values
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	var digits [20]byte
	pos := len(digits)
	for value != 0 {
		digit := value % 10
		if digit < 0 {
			digit = -digit
		}
		pos--
		digits[pos] = byte('0' + digit)
		value /= 10
	}
	if negative {
		pos--
		digits[pos] = '-'
	}
	return string(digits[pos:])
}
