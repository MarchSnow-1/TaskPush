package config

import (
	"encoding/json"
	"strings"
)

// ---------------------------------------------------------------------------
// 内联渠道构建: 把 --webhook/--type/--set/--payload 合成一个 settings 对象
// 这里只做形状与冲突检查, 字段 schema 仍由渠道工厂在创建时严格解码
//
// Building an inline channel: the --webhook, --type, --set and --payload flags are combined
// into one settings object
// Only shape and conflict checks happen here, while the field schema stays with strict decoding
// inside the channel factory at creation time
// ---------------------------------------------------------------------------

const (
	// inlineChannelID 是内联渠道固定使用的 ID
	// inlineChannelID is the fixed ID used by the inline channel
	inlineChannelID = "inline"
	// inlineWebhookType 是 --webhook 与 --payload 默认选择的渠道类型
	// inlineWebhookType is the channel type selected by default for --webhook and --payload
	inlineWebhookType = "webhook"
	// defaultInlineBody 是内联 webhook 未给出正文相关参数时的默认正文
	// defaultInlineBody is the body used by an inline webhook when no body-related flag is given
	defaultInlineBody = "{{title}}\n\n{{body}}"
)

// InlineOptions 是内联构建的输入
// Sets 已按 KEY=VALUE 拆分, Payload 已由 CLI 校验为合法 JSON, LookupEnv 注入环境变量查询
//
// InlineOptions is the input of inline building
// Sets has already been split as KEY=VALUE, Payload has already been checked as valid JSON by
// the CLI, and LookupEnv injects the environment lookup
type InlineOptions struct {
	Webhook   string
	Type      string
	Payload   string
	Sets      []string
	LookupEnv func(string) (string, bool)
}

// BuildInline 合成一个内联渠道
// 类型优先取显式 --type, 否则取 webhook; 冲突与非法参数返回 config.inline.* 键
// 返回的 Channel 不经过工厂创建, settings 以 JSON 原文保存
//
// BuildInline combines one inline channel
// The type comes from an explicit --type first and webhook otherwise, while conflicts and bad
// arguments return a config.inline.* key
// The returned Channel does not go through a factory, and settings is kept as raw JSON
func BuildInline(opts InlineOptions) (Channel, error) {
	typ, err := inlineType(opts)
	if err != nil {
		return Channel{}, err
	}

	sets, err := parseSetEntries(opts.Sets)
	if err != nil {
		return Channel{}, err
	}

	lookup := opts.LookupEnv
	if lookup == nil {
		lookup = defaultLookupEnv
	}

	settings := map[string]any{}
	hasPayload := strings.TrimSpace(opts.Payload) != ""
	hasWebhook := strings.TrimSpace(opts.Webhook) != ""
	hasBodySet := false

	if hasWebhook {
		expanded, err := expandString("inline", "url", opts.Webhook, lookup)
		if err != nil {
			return Channel{}, err
		}
		if strings.TrimSpace(expanded) == "" {
			return Channel{}, newProblem("config.inline.webhook_url", nil, nil)
		}
		settings["url"] = expanded
	}

	if hasPayload {
		expanded, err := expandJSON("inline", "payload", json.RawMessage(opts.Payload), lookup)
		if err != nil {
			return Channel{}, newProblem("config.inline.payload", map[string]any{
				"Detail": err.Error(),
			}, err)
		}
		settings["body"] = map[string]any{
			"format": "json",
			"json":   json.RawMessage(expanded),
		}
	}

	for _, entry := range sets {
		if entry.key == "url" && hasWebhook {
			return Channel{}, newProblem("config.inline.set_conflict", map[string]any{
				"Key":    entry.key,
				"Reason": "--webhook already sets url",
			}, nil)
		}
		if entry.topLevel() == "body" && hasPayload {
			return Channel{}, newProblem("config.inline.set_conflict", map[string]any{
				"Key":    entry.key,
				"Reason": "--payload already sets body",
			}, nil)
		}
		if entry.topLevel() == "body" {
			hasBodySet = true
		}
		expanded, err := expandString("inline", entry.path(), entry.value, lookup)
		if err != nil {
			return Channel{}, err
		}
		if err := assignSet(settings, entry, expanded); err != nil {
			return Channel{}, err
		}
	}

	if hasWebhook && !hasPayload && !hasBodySet {
		if _, ok := settings["method"]; !ok {
			settings["method"] = "POST"
		}
		settings["body"] = map[string]any{"format": "text", "text": defaultInlineBody}
	}

	if hasPayload {
		if _, ok := settings["url"]; !ok {
			return Channel{}, newProblem("config.inline.payload", map[string]any{
				"Detail": "--payload needs --webhook or --set url=...",
			}, nil)
		}
	}

	raw, err := json.Marshal(settings)
	if err != nil {
		return Channel{}, newProblem("config.inline.set", map[string]any{
			"Value": err.Error(),
		}, err)
	}

	enabled := true
	return Channel{
		ID:       inlineChannelID,
		Type:     typ,
		Enabled:  &enabled,
		Settings: raw,
	}, nil
}

// inlineType 确定内联渠道的类型
// 显式 --type 优先, 否则 --webhook 或 --payload 选择 webhook, 都没有时返回用法错误
//
// inlineType determines the type of the inline channel
// An explicit --type wins, and otherwise --webhook or --payload selects webhook, while an
// absence of both returns a usage error
func inlineType(opts InlineOptions) (string, error) {
	if trimmed := strings.TrimSpace(opts.Type); trimmed != "" {
		return trimmed, nil
	}
	if strings.TrimSpace(opts.Webhook) != "" || strings.TrimSpace(opts.Payload) != "" {
		return inlineWebhookType, nil
	}
	return "", newProblem("config.inline.mode", nil, nil)
}

// setEntry 是一个已按第一个等号拆分的 --set 参数
//
// setEntry is one --set argument already split at its first equals sign
type setEntry struct {
	key   string
	value string
}

// topLevel 返回点路径的第一段, 无点号时就是整个键
//
// topLevel returns the first segment of a dotted path, which is the whole key without a dot
func (e setEntry) topLevel() string {
	if at := strings.IndexByte(e.key, '.'); at >= 0 {
		return e.key[:at]
	}
	return e.key
}

// path 返回用于错误定位的路径
//
// path returns the path used for error location
func (e setEntry) path() string {
	return "set." + e.key
}

// parseSetEntries 解析 --set 列表并拒绝重复键
// 按第一个等号切分, 键与值都去除首尾空白; 缺少等号或空键返回 config.inline.set
//
// parseSetEntries parses the --set list and rejects a duplicate key
// Each value is split at its first equals sign with both sides trimmed, and a missing equals
// sign or an empty key returns config.inline.set
func parseSetEntries(values []string) ([]setEntry, error) {
	entries := make([]setEntry, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		at := strings.IndexByte(raw, '=')
		if at < 0 {
			return nil, newProblem("config.inline.set", map[string]any{"Value": truncateText(raw)}, nil)
		}
		key := strings.TrimSpace(raw[:at])
		if key == "" {
			return nil, newProblem("config.inline.set", map[string]any{"Value": truncateText(raw)}, nil)
		}
		if _, dup := seen[key]; dup {
			return nil, newProblem("config.inline.set_duplicate", map[string]any{"Key": key}, nil)
		}
		seen[key] = struct{}{}
		entries = append(entries, setEntry{key: key, value: raw[at+1:]})
	}
	return entries, nil
}

// assignSet 把一个 --set 写进 settings
// 一级键直接赋值, 点路径按段创建对象; 中间段是标量时返回 config.inline.set
//
// assignSet writes one --set into settings
// A top-level key is assigned directly and a dotted path creates objects per segment, while a
// scalar in the middle returns config.inline.set
func assignSet(settings map[string]any, entry setEntry, value string) error {
	segments := strings.Split(entry.key, ".")
	current := settings
	for _, segment := range segments[:len(segments)-1] {
		next, ok := current[segment]
		if !ok {
			child := map[string]any{}
			current[segment] = child
			current = child
			continue
		}
		child, ok := next.(map[string]any)
		if !ok {
			return newProblem("config.inline.set", map[string]any{"Value": entry.key}, nil)
		}
		current = child
	}
	last := segments[len(segments)-1]
	if _, ok := current[last]; ok {
		if _, isObject := current[last].(map[string]any); !isObject {
			return newProblem("config.inline.set", map[string]any{"Value": entry.key}, nil)
		}
	}
	current[last] = value
	return nil
}
