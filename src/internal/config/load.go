package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/settings"
)

// ---------------------------------------------------------------------------
// 配置加载: 先保留 channels 原文, 再按启用状态展开, 最后严格解码成 Root
// 展开分两步是为了让禁用渠道的坏变量只进入 Deferred, 而启用渠道的同类问题立即失败
// 禁用渠道的公共标量同样延迟: 展开失败或被写坏时删掉该键并视情况追加 Deferred, 让 list 与 send 照常工作
//
// Loading a configuration: the raw channels are kept first, expansion follows per enabled
// state, and a strict decode into Root comes last
// The two-step expansion keeps a bad variable in a disabled channel inside Deferred, while the
// same problem in an enabled channel fails immediately
// The common scalars of a disabled channel are deferred the same way: a failed expansion or a
// broken value drops the key and appends a Deferred problem where useful, which keeps list and
// send working as designed
// ---------------------------------------------------------------------------

const (
	// defaultTimeout 是顶层 timeout 缺省时使用的时长
	// defaultTimeout is the span used when the top-level timeout is absent
	defaultTimeout = 15 * time.Second
	// defaultRetryDelay 是顶层 retryDelay 缺省时使用的间隔
	// defaultRetryDelay is the interval used when the top-level retryDelay is absent
	defaultRetryDelay = time.Second
)

// Load 读取一份已解析来源的配置
// 顺序为 BOM, 顶层对象, 未知键, 类型, 展开, 严格解码与校验; 失败统一返回 *apperr.Problem
// Load 不调用渠道工厂, 因此 settings 的 schema 问题留到创建渠道时暴露
// 禁用渠道的公共标量与 settings 一样按设计延迟解析, 因此它们的错误同样只进 Deferred 而不结束加载
//
// Load reads a configuration from a resolved source
// The order is BOM, top-level object, unknown keys, types, expansion, strict decode and
// validation, with every failure returned as *apperr.Problem
// Load does not call a channel factory, so a settings schema problem surfaces when the channel
// is created
// The common scalars of a disabled channel are deferred by design like settings, so their
// failures also land in Deferred only instead of ending the load
func Load(src Source) (*Root, error) {
	source := src.Label()
	data := stripBOM(src.Data)
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, newProblem("config.error.empty", map[string]any{"Source": source}, nil)
	}

	top, err := decodeTopObject(source, data)
	if err != nil {
		return nil, err
	}
	if err := checkUnknownFields("", top, topLevelFields); err != nil {
		return nil, err
	}

	channels, err := decodeChannelList(top)
	if err != nil {
		return nil, err
	}
	entries, err := parseChannelEntries(channels)
	if err != nil {
		return nil, err
	}

	// 顶层字符串无论渠道状态都展开, 因此这里的失败直接结束加载
	// Top-level strings are expanded whatever the channel states are, so a failure here ends
	// the load
	if err := expandTopFields(source, top, os.LookupEnv); err != nil {
		return nil, err
	}

	expanded, deferred, err := expandChannels(source, channels, entries, os.LookupEnv)
	if err != nil {
		return nil, err
	}

	// 逐字段解码标量并把字符串形式规范化成原生 JSON
	// 冻结模型用 int 与 bool, 而契约允许这两类字段写成十进制字符串, 归一化让两者同时成立
	// 启用渠道的公共标量在这里解析; 禁用渠道保留未展开原文, 由 deferredChannelObject 延迟处理
	//
	// Scalars are decoded field by field and a string form is normalised into native JSON
	// The frozen model uses int and bool, while the contract allows the two kinds of field to be
	// written as decimal strings, and normalisation satisfies both
	// The common scalars of an enabled channel are parsed here, while a disabled channel keeps its
	// unexpanded raw text and goes through the deferred handling of deferredChannelObject
	normalized, deferredScalars, err := normalizeScalarFields(top, expanded, entries, source, os.LookupEnv, deferred)
	if err != nil {
		return nil, err
	}
	deferred = append(deferred, deferredScalars...)

	assembled, err := json.Marshal(normalized)
	if err != nil {
		return nil, newProblem("config.error.json", map[string]any{
			"Source": source,
			"Detail": err.Error(),
		}, err)
	}

	root, err := decodeRoot(source, assembled)
	if err != nil {
		return nil, err
	}
	root.Deferred = deferred

	applyDefaults(root, normalized)
	if err := validateRoot(root); err != nil {
		return nil, err
	}
	return root, nil
}

// decodeTopObject 把顶层 JSON 解析成键到原文的映射
// 非对象顶层与语法错误都返回 config.error.json, 因为此时还没有可用的字段路径
//
// decodeTopObject parses the top-level JSON into a key-to-raw mapping
// A non-object top level and a syntax error both return config.error.json, since no usable field
// path exists yet
func decodeTopObject(source string, data []byte) (map[string]json.RawMessage, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil || top == nil {
		detail := "the top level must be a JSON object"
		var cause error
		if err != nil {
			detail = err.Error()
			cause = err
		}
		return nil, newProblem("config.error.json", map[string]any{
			"Source": source,
			"Detail": detail,
		}, cause)
	}
	return top, nil
}

// checkUnknownFields 检查一层对象里的未知键
// 键按排序后遍历, 因此多个未知键时的首条错误在不同运行间一致
//
// checkUnknownFields checks one object level for unknown keys
// Keys are walked in sorted order, so the first error among several unknown keys stays the same
// between runs
func checkUnknownFields(path string, fields map[string]json.RawMessage, known []string) error {
	allowed := make(map[string]struct{}, len(known))
	for _, key := range known {
		allowed[key] = struct{}{}
	}
	for _, key := range sortedKeys(fields) {
		if _, ok := allowed[key]; !ok {
			return unknownFieldProblem(path, key, known)
		}
	}
	return nil
}

// decodeChannelList 读取顶层 channels 数组
// 缺失, null 与非数组都返回 config.error.type_mismatch, 空数组是合法输入
//
// decodeChannelList reads the top-level channels array
// A missing, null or non-array value returns config.error.type_mismatch, while an empty array is
// valid input
func decodeChannelList(top map[string]json.RawMessage) ([]json.RawMessage, error) {
	raw, ok := top["channels"]
	if !ok {
		return nil, typeMismatchProblem("", "channels", "array")
	}
	var channels []json.RawMessage
	if err := json.Unmarshal(raw, &channels); err != nil || channels == nil {
		return nil, typeMismatchProblem("", "channels", "array")
	}
	return channels, nil
}

// channelEntry 保存一个渠道在展开前需要的信息
// Enabled 为 true 时展开失败会直接结束加载, 为 false 时进入 Deferred
// Enabled 同时决定公共标量在什么时候解析: 启用渠道在归一化阶段解析, 禁用渠道延迟处理
// ID 与 Type 让延迟问题能带上渠道归属, 读不到时留空, 由调用方回退到配置路径
//
// channelEntry holds the information one channel needs before expansion
// A true Enabled makes an expansion failure end the load, while false sends it to Deferred
// Enabled also decides when the common scalars are parsed: an enabled channel parses them during
// normalisation, while a disabled channel gets the deferred handling
// ID and Type let a deferred problem carry the owning channel, and an unreadable value stays empty
// so the caller falls back to the configuration path
type channelEntry struct {
	Path    string
	ID      string
	Type    string
	Enabled bool
}

// channelPath 拼出一个渠道条目的配置路径
// 下标从 1 开始, 让面向用户的报错不需要再做 0/1 换算
//
// channelPath joins the configuration path of one channel entry
// The subscript starts at 1, so user-facing errors need no zero-to-one conversion
func channelPath(index int) string {
	return "channels[" + itoa(index+1) + "]"
}

// parseChannelEntries 解析每个渠道对象并记录启用状态, ID 与类型
// 同时检查公共字段的未知键与基础类型, 让错误路径停留在展开前的稳定位置
// id 与 type 在这里只按字符串试读, 写法错误的严格报错留给后面的归一化与校验
//
// parseChannelEntries parses every channel object and records its enabled state, its ID and its
// type
// It also checks the unknown keys and base types of the common fields, which keeps the error
// path at the stable position from before expansion
// The ID and type are only read optimistically as strings here, and the strict rejection of a
// malformed value stays with the later normalisation and validation
func parseChannelEntries(channels []json.RawMessage) ([]channelEntry, error) {
	entries := make([]channelEntry, 0, len(channels))
	for i, raw := range channels {
		path := channelPath(i)
		fields, err := objectFields(path, raw)
		if err != nil {
			return nil, err
		}
		if err := checkUnknownFields(path, fields, channelFields); err != nil {
			return nil, err
		}
		enabled := true
		if rawEnabled, ok := fields["enabled"]; ok {
			parsed, err := boolField(path, "enabled", rawEnabled)
			if err != nil {
				return nil, err
			}
			enabled = parsed
		}
		entry := channelEntry{Path: path, Enabled: enabled}
		if rawID, ok := fields["id"]; ok {
			entry.ID, _ = stringField(path, "id", rawID)
		}
		if rawType, ok := fields["type"]; ok {
			entry.Type, _ = stringField(path, "type", rawType)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// normalizeScalarFields 逐字段解码顶层与渠道公共字段, 并把字符串形式写回原生 JSON
// 这样 duration, int 与 bool 的错误都能带上 `channels[N].timeout` 形式的路径, 同时冻结模型
// 仍然只看到 int/bool 的原生表示, 其中 N 从 1 开始
// 禁用渠道按设计保留未展开原文: 公共标量走 deferredChannelObject 的延迟处理, 失败不结束加载
// 而是删掉该键并按需追加 Deferred, 由 check --all 统一报告
//
// normalizeScalarFields decodes the top-level and channel common fields one by one and writes a
// string form back as native JSON
// That gives the duration, int and bool errors a path of the form channels[N].timeout while the
// frozen model still sees the native int and bool representation only, and N starts at 1
// A disabled channel keeps its unexpanded raw text by design: its common scalars go through the
// deferred handling of deferredChannelObject, and a failure there neither ends the load nor
// keeps the key, it drops the key and appends a Deferred problem for check --all to report
func normalizeScalarFields(top map[string]json.RawMessage, channels []json.RawMessage, entries []channelEntry, source string, lookup envLookup, already []apperr.Problem) (map[string]json.RawMessage, []apperr.Problem, error) {
	normalized := make(map[string]json.RawMessage, len(top))
	for key, raw := range top {
		if key == "channels" {
			continue
		}
		normalized[key] = raw
	}

	topScalars := []struct {
		field     string
		normalize func(string, string, json.RawMessage) (json.RawMessage, error)
	}{
		{field: "version", normalize: normalizeIntAt},
		{field: "$schema", normalize: normalizeStringAt},
		{field: "timeout", normalize: normalizeDurationAt},
		{field: "retries", normalize: normalizeIntAt},
		{field: "retryDelay", normalize: normalizeDurationAt},
		{field: "proxy", normalize: normalizeStringAt},
	}
	for _, scalar := range topScalars {
		raw, ok := normalized[scalar.field]
		if !ok {
			continue
		}
		value, err := scalar.normalize("", scalar.field, raw)
		if err != nil {
			return nil, nil, err
		}
		normalized[scalar.field] = value
	}

	normalizedChannels := make([]json.RawMessage, len(channels))
	var deferred []apperr.Problem
	for i, raw := range channels {
		path := channelPath(i)
		if !entries[i].Enabled {
			encoded, problems, err := deferredChannelObject(source, raw, entries[i], lookup, already)
			if err != nil {
				return nil, nil, err
			}
			normalizedChannels[i] = encoded
			deferred = append(deferred, problems...)
			continue
		}
		fields, err := objectFields(path, raw)
		if err != nil {
			return nil, nil, err
		}
		channelScalars := []struct {
			field     string
			normalize func(string, string, json.RawMessage) (json.RawMessage, error)
		}{
			{field: "id", normalize: normalizeStringAt},
			{field: "type", normalize: normalizeStringAt},
			{field: "enabled", normalize: normalizeBoolAt},
			{field: "timeout", normalize: normalizeDurationAt},
			{field: "retries", normalize: normalizeIntAt},
			{field: "retryDelay", normalize: normalizeDurationAt},
		}
		for _, scalar := range channelScalars {
			value, ok := fields[scalar.field]
			if !ok {
				continue
			}
			normalizedValue, err := scalar.normalize(path, scalar.field, value)
			if err != nil {
				return nil, nil, err
			}
			fields[scalar.field] = normalizedValue
		}
		encoded, err := json.Marshal(fields)
		if err != nil {
			return nil, nil, newProblem("config.error.json", map[string]any{
				"Source": path,
				"Detail": err.Error(),
			}, err)
		}
		normalizedChannels[i] = encoded
	}
	encodedChannels, err := json.Marshal(normalizedChannels)
	if err != nil {
		return nil, nil, newProblem("config.error.json", map[string]any{
			"Source": "channels",
			"Detail": err.Error(),
		}, err)
	}
	normalized["channels"] = encodedChannels
	return normalized, deferred, nil
}

// deferredChannelObject 修剪一条禁用渠道的公共字段, 让装配文本能安全解码并返回延迟的标量问题
// id, type 与 enabled 的写法错误仍然致命, 渠道身份与启用状态不属于延迟范围
// timeout, retries 与 retryDelay 先按原文归一化; 失败则用真实环境单独展开这个叶子, 展开失败得到
// 该标量自己的环境变量问题, 展开成功但解析仍失败则保留解析问题, 两种情况都删掉该键并把问题追加
// 进返回值, 由 check --all 报告; 展开后解析成功则写入展开值
// 每条回头的问题都带上渠道归属, 让 check --all 的表格显示渠道名而不是配置路径
//
// deferredChannelObject prunes the common fields of one disabled channel so the assembled text
// decodes, and returns the deferred scalar problems
// A bad id, type or enabled stays fatal, since channel identity and enabled state are out of the
// deferral's scope
// timeout, retries and retryDelay normalise from the raw text first; on failure the leaf is
// expanded alone against the real environment, an expansion failure yields the scalar's own
// environment-variable problem, while a value that expands but still does not parse keeps the
// parse problem, and both drop the key and append their problem to the result for check --all to
// report, whereas a value that parses after expansion is written back
// Every returned problem carries the owning channel, which lets the check --all table show the
// channel name instead of the configuration path
func deferredChannelObject(source string, raw json.RawMessage, entry channelEntry, lookup envLookup, already []apperr.Problem) (json.RawMessage, []apperr.Problem, error) {
	path := entry.Path
	fields, err := objectFields(path, raw)
	if err != nil {
		return nil, nil, err
	}
	strictScalars := []struct {
		field     string
		normalize func(string, string, json.RawMessage) (json.RawMessage, error)
	}{
		{field: "id", normalize: normalizeStringAt},
		{field: "type", normalize: normalizeStringAt},
		{field: "enabled", normalize: normalizeBoolAt},
	}
	for _, scalar := range strictScalars {
		value, ok := fields[scalar.field]
		if !ok {
			continue
		}
		normalizedValue, err := scalar.normalize(path, scalar.field, value)
		if err != nil {
			return nil, nil, err
		}
		fields[scalar.field] = normalizedValue
	}

	var problems []apperr.Problem
	seen := deferredSeen(already)
	deferredScalars := []struct {
		field     string
		normalize func(string, string, json.RawMessage) (json.RawMessage, error)
	}{
		{field: "timeout", normalize: normalizeDurationAt},
		{field: "retries", normalize: normalizeIntAt},
		{field: "retryDelay", normalize: normalizeDurationAt},
	}
	for _, scalar := range deferredScalars {
		value, ok := fields[scalar.field]
		if !ok {
			continue
		}
		if normalizedValue, err := scalar.normalize(path, scalar.field, value); err == nil {
			fields[scalar.field] = normalizedValue
			continue
		}
		leafPath := fieldPath(path, scalar.field)
		expanded, expandErr := expandJSON(source, leafPath, value, lookup)
		if expandErr != nil {
			var problem *apperr.Problem
			if errors.As(expandErr, &problem) {
				// 整渠道展开只记录第一个失败叶子, 这里按叶子逐条补齐, 同一路径不重复追加,
				// 删键让指针字段缺省成继承顶层, 全部问题由 check --all 报告
				// A whole-channel expansion records only the first failing leaf, so the per-leaf
				// pass here fills in the rest without repeating a path already present, and the
				// dropped key lets the pointer field fall back to the top level, with every
				// problem reported by check --all
				addDeferred(&problems, seen, withDeferredIdentity(*problem, entry))
			}
			delete(fields, scalar.field)
			continue
		}
		normalizedValue, err := scalar.normalize(path, scalar.field, expanded)
		if err == nil {
			fields[scalar.field] = normalizedValue
			continue
		}
		var problem *apperr.Problem
		if errors.As(err, &problem) {
			addDeferred(&problems, seen, withDeferredIdentity(*problem, entry))
		}
		delete(fields, scalar.field)
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, nil, newProblem("config.error.json", map[string]any{
			"Source": path,
			"Detail": err.Error(),
		}, err)
	}
	return encoded, problems, nil
}

// withDeferredIdentity 给一条延迟问题补上渠道归属, Data 里因此带上 ChannelID 与 ChannelType
// 读不到 id 或 type 时不写该键: 空串会被 cli 层当成「有值」从而显示空白, 缺键才能让它回退
// Data 里已存在的键不会被覆盖, 该字段的归属保留给写入它的那一方
// 复制 Data 再写入, 调用方手里的原 Problem 不会被改动
//
// withDeferredIdentity adds the owning channel to one deferred problem, so its Data carries
// ChannelID and ChannelType
// An unreadable ID or type leaves the key out, because an empty string would look like a value to
// the cli layer and render a blank cell, while a missing key lets it fall back
// A key already present in Data is never overwritten, which keeps the ownership of that field with
// whoever produced it
// The Data map is copied before writing, so the original Problem held by the caller stays as it was
func withDeferredIdentity(problem apperr.Problem, entry channelEntry) apperr.Problem {
	data := make(map[string]any, len(problem.Data)+2)
	for key, value := range problem.Data {
		data[key] = value
	}
	if _, exists := data["ChannelID"]; !exists && entry.ID != "" {
		data["ChannelID"] = entry.ID
	}
	if _, exists := data["ChannelType"]; !exists && entry.Type != "" {
		data["ChannelType"] = entry.Type
	}
	problem.Data = data
	return problem
}

// deferredKey 用消息键与路径标识一条延迟问题
// 整渠道展开与单叶子展开会产出同一个键同一条路径的重复条目, 这个标识用来识别它们
//
// deferredKey identifies one deferred problem by message key and path
// A whole-channel expansion and a per-leaf expansion can produce the same key and path twice, and
// this identity spots that
type deferredKey struct {
	id   string
	path string
}

// deferredSeen 收集已经出现过的延迟问题标识, 供追加时查重
//
// deferredSeen collects the identities of problems that already appeared, for a duplicate check
// when appending
func deferredSeen(problems []apperr.Problem) map[deferredKey]struct{} {
	seen := make(map[deferredKey]struct{}, len(problems))
	for _, problem := range problems {
		path, _ := problem.Data["Path"].(string)
		seen[deferredKey{id: problem.ID, path: path}] = struct{}{}
	}
	return seen
}

// addDeferred 在标识未出现过时追加一条延迟问题并登记它, 返回是否新增
//
// addDeferred appends one deferred problem when its identity is new, registers it, and reports
// whether it was added
func addDeferred(problems *[]apperr.Problem, seen map[deferredKey]struct{}, problem apperr.Problem) bool {
	path, _ := problem.Data["Path"].(string)
	key := deferredKey{id: problem.ID, path: path}
	if _, dup := seen[key]; dup {
		return false
	}
	seen[key] = struct{}{}
	*problems = append(*problems, problem)
	return true
}

// normalizeStringAt 校验一个值是 JSON 字符串并原样返回
//
// normalizeStringAt checks that a value is a JSON string and returns it unchanged
func normalizeStringAt(path, field string, raw json.RawMessage) (json.RawMessage, error) {
	if _, err := stringField(path, field, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// normalizeIntAt 把一个 JSON 整数或十进制字符串统一写成 JSON 数字
//
// normalizeIntAt writes a JSON integer or a decimal string uniformly as a JSON number
func normalizeIntAt(path, field string, raw json.RawMessage) (json.RawMessage, error) {
	value, err := intField(path, field, raw)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(itoa(value)), nil
}

// normalizeBoolAt 把一个 JSON 布尔或字符串写回 JSON 布尔
//
// normalizeBoolAt writes a JSON boolean or a string back as a JSON boolean
func normalizeBoolAt(path, field string, raw json.RawMessage) (json.RawMessage, error) {
	value, err := boolField(path, field, raw)
	if err != nil {
		return nil, err
	}
	if value {
		return json.RawMessage("true"), nil
	}
	return json.RawMessage("false"), nil
}

// normalizeDurationAt 校验时长并保留字符串原文
//
// normalizeDurationAt checks a duration and keeps the original string
func normalizeDurationAt(path, field string, raw json.RawMessage) (json.RawMessage, error) {
	if _, err := durationField(path, field, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// expandTopFields 展开顶层除 channels 外的字段
// 顶层字符串与渠道是否禁用无关, 因此这里的问题一律直接失败
//
// expandTopFields expands the top-level fields other than channels
// Top-level strings are independent of a channel being disabled, so a problem here always fails
// the load
func expandTopFields(source string, top map[string]json.RawMessage, lookup envLookup) error {
	for _, key := range sortedKeys(top) {
		if key == "channels" {
			continue
		}
		expanded, err := expandJSON(source, key, top[key], lookup)
		if err != nil {
			return err
		}
		top[key] = expanded
	}
	return nil
}

// expandChannels 按启用状态展开每个渠道
// 启用渠道展开失败直接返回; 禁用渠道展开失败时保留原文并把问题追加到 Deferred
//
// expandChannels expands every channel according to its enabled state
// A failed expansion of an enabled channel is returned, while a failed expansion of a disabled
// channel keeps the original raw value and appends the problem to Deferred
func expandChannels(source string, channels []json.RawMessage, entries []channelEntry, lookup envLookup) ([]json.RawMessage, []apperr.Problem, error) {
	expanded := make([]json.RawMessage, len(channels))
	var deferred []apperr.Problem

	for i, raw := range channels {
		entry := entries[i]
		value, err := expandJSON(source, entry.Path, raw, lookup)
		if err == nil {
			if entry.Enabled {
				expanded[i] = value
			} else {
				// 禁用渠道即使展开成功也保留原文, 让处理推迟到 check --all
				// A disabled channel keeps its original text even when expansion succeeds, which
				// leaves the work to check --all
				expanded[i] = raw
			}
			continue
		}
		if entry.Enabled {
			return nil, nil, err
		}
		expanded[i] = raw
		var problem *apperr.Problem
		if errors.As(err, &problem) {
			// 整渠道展开的问题先于标量处理进入 Deferred, 同样要带上渠道归属,
			// 否则 check --all 的 ID 列只能退回配置路径
			// A whole-channel expansion problem lands in Deferred before the scalar pass runs,
			// and it needs the owning channel too, otherwise the ID column of check --all has to
			// fall back to the configuration path
			deferred = append(deferred, withDeferredIdentity(*problem, entry))
		}
	}
	return expanded, deferred, nil
}

// decodeRoot 严格解码 Root, 拒绝未知字段
// 标量已经在前面逐字段校验, 因此这里的失败只作为兜底路径
//
// decodeRoot strictly decodes Root and rejects unknown fields
// Scalars have already been checked field by field, so a failure here is a fallback path only
func decodeRoot(source string, data []byte) (*Root, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var root Root
	if err := dec.Decode(&root); err != nil {
		return nil, mapDecodeError(source, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, newProblem("config.error.json", map[string]any{
			"Source": source,
			"Detail": "trailing content after the configuration object",
		}, err)
	}
	return &root, nil
}

// mapDecodeError 把严格解码错误转换成 Problem
// 未知字段与类型错误各走对应的键, 其余情况按 JSON 结构错误处理
//
// mapDecodeError turns a strict decoding error into a Problem
// An unknown field and a type error take their own keys, while every other case is treated as a
// JSON structure error
func mapDecodeError(source string, err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return typeMismatchProblem("", typeErr.Field, typeErr.Type.String())
	}
	if strings.HasPrefix(err.Error(), "json: unknown field ") {
		return unknownFieldProblem("", unknownFieldName(err), append(topLevelFields, channelFields...))
	}
	return newProblem("config.error.json", map[string]any{
		"Source": source,
		"Detail": err.Error(),
	}, err)
}

// unknownFieldName 从不允许字段的错误文本里取出字段名
//
// unknownFieldName extracts the field name from a disallowed-field error text
func unknownFieldName(err error) string {
	name := strings.TrimPrefix(err.Error(), "json: unknown field ")
	return strings.Trim(name, `"`)
}

// applyDefaults 填充顶层缺省值
// 只填缺省字段, 显式写出的值保持原样, 让后续范围校验能报出这条显式配置
//
// applyDefaults fills the top-level defaults
// Only absent fields are filled, and an explicit value stays as written so later range
// validation can report that configuration
func applyDefaults(root *Root, top map[string]json.RawMessage) {
	if root.Version == 0 {
		root.Version = supportedVersion
	}
	if _, ok := top["timeout"]; !ok {
		root.Timeout = settings.Duration(defaultTimeout)
	}
	if _, ok := top["retryDelay"]; !ok {
		root.RetryDelay = settings.Duration(defaultRetryDelay)
	}
}

// validateRoot 校验顶层数值与每个渠道的公共字段
// 校验顺序固定为顶层在前, 渠道按配置顺序在后, 便于调用方按同一顺序报告首条错误
//
// validateRoot validates the top-level numbers and the common fields of every channel
// The order is top level first and channels in configuration order, which keeps the first
// reported error in one stable order
func validateRoot(root *Root) error {
	if err := validateVersion(root.Version); err != nil {
		return err
	}
	if root.Timeout.Duration() <= 0 {
		return newProblem("config.error.timeout", map[string]any{
			"Path":  "timeout",
			"Value": root.Timeout.String(),
		}, nil)
	}
	if root.Retries < 0 {
		return newProblem("config.error.retries", map[string]any{
			"Path":  "retries",
			"Value": itoa(root.Retries),
		}, nil)
	}
	if root.RetryDelay.Duration() < 0 {
		return newProblem("config.error.retry_delay", map[string]any{
			"Path":  "retryDelay",
			"Value": root.RetryDelay.String(),
		}, nil)
	}
	if err := validateProxy(root.Proxy); err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(root.Channels))
	for i := range root.Channels {
		channel := &root.Channels[i]
		path := channelPath(i)
		if err := validateChannelID(path, channel.ID); err != nil {
			return err
		}
		if _, dup := seen[channel.ID]; dup {
			return newProblem("config.error.id_duplicate", map[string]any{"ID": channel.ID}, nil)
		}
		seen[channel.ID] = struct{}{}
		if err := validateChannelType(path, channel.Type); err != nil {
			return err
		}
		if err := validateChannelRanges(path, channel); err != nil {
			return err
		}
		if !channel.IsEnabled() {
			continue
		}
		if len(bytes.TrimSpace(channel.Settings)) == 0 {
			return newProblem("config.error.settings_missing", map[string]any{
				"Path": fieldPath(path, "settings"),
			}, nil)
		}
		if err := validateSettingsObject(fieldPath(path, "settings"), channel.Settings); err != nil {
			return err
		}
	}
	return nil
}

// validateChannelRanges 校验一个渠道覆盖值的范围
// 未写字段留下的 nil 指针不参与校验, 沿用顶层默认值
//
// validateChannelRanges checks the ranges of one channel's overrides
// A nil pointer from an absent field takes no part, which keeps the top-level default in force
func validateChannelRanges(path string, channel *Channel) error {
	if channel.Timeout != nil && channel.Timeout.Duration() <= 0 {
		return newProblem("config.error.timeout", map[string]any{
			"Path":  fieldPath(path, "timeout"),
			"Value": channel.Timeout.String(),
		}, nil)
	}
	if channel.Retries != nil && *channel.Retries < 0 {
		return newProblem("config.error.retries", map[string]any{
			"Path":  fieldPath(path, "retries"),
			"Value": itoa(*channel.Retries),
		}, nil)
	}
	if channel.RetryDelay != nil && channel.RetryDelay.Duration() < 0 {
		return newProblem("config.error.retry_delay", map[string]any{
			"Path":  fieldPath(path, "retryDelay"),
			"Value": channel.RetryDelay.String(),
		}, nil)
	}
	return nil
}
