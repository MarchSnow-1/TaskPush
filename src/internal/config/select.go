package config

import (
	"strings"
)

// ---------------------------------------------------------------------------
// 目标选择: ids 为空时取全部启用渠道, 否则按配置顺序挑出请求的启用渠道
// 返回顺序始终按配置顺序, 让调度与输出不受命令行参数顺序影响
//
// Target selection: an empty ids list takes every enabled channel, while a non-empty one picks
// the requested enabled channels
// The result is always in configuration order, which keeps scheduling and output independent of
// the order of the command-line arguments
// ---------------------------------------------------------------------------

// Select 按 ids 选择启用的渠道
// ids 为空返回全部启用渠道; 非空时去重, 未知 ID 与禁用 ID 各自报错, 重复项只保留一次
//
// Select picks enabled channels by ids
// An empty ids list returns every enabled channel, while a non-empty one is deduplicated,
// reports an unknown or disabled ID, and keeps each requested channel once
func (r *Root) Select(ids []string) ([]Channel, error) {
	selected := map[string]bool{}
	wanted := make([]string, 0, len(ids))
	known := map[string]bool{}
	for _, channel := range r.Channels {
		known[channel.ID] = true
	}

	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			return nil, newProblem("config.error.empty_target", nil, nil)
		}
		if _, dup := selected[id]; dup {
			continue
		}
		selected[id] = true
		wanted = append(wanted, id)
	}

	if len(wanted) == 0 {
		return r.enabledChannels()
	}

	for _, id := range wanted {
		if !known[id] {
			return nil, newProblem("config.error.unknown_target", map[string]any{
				"ID":        id,
				"Available": strings.Join(r.availableIDs(), ", "),
			}, nil)
		}
	}

	out := make([]Channel, 0, len(wanted))
	for _, channel := range r.Channels {
		if !selected[channel.ID] {
			continue
		}
		if !channel.IsEnabled() {
			return nil, newProblem("config.error.disabled_target", map[string]any{"ID": channel.ID}, nil)
		}
		out = append(out, channel)
	}
	if len(out) == 0 {
		return nil, newProblem("config.error.no_channels", nil, nil)
	}
	return out, nil
}

// enabledChannels 返回按配置顺序排列的全部启用渠道
// 一个都没有时返回 config.error.no_channels
//
// enabledChannels returns every enabled channel in configuration order
// With none at all it returns config.error.no_channels
func (r *Root) enabledChannels() ([]Channel, error) {
	out := make([]Channel, 0, len(r.Channels))
	for _, channel := range r.Channels {
		if channel.IsEnabled() {
			out = append(out, channel)
		}
	}
	if len(out) == 0 {
		return nil, newProblem("config.error.no_channels", nil, nil)
	}
	return out, nil
}

// availableIDs 返回全部渠道 ID, 含禁用渠道, 供未知目标错误展示
//
// availableIDs returns every channel ID including disabled ones, for the unknown-target error
func (r *Root) availableIDs() []string {
	out := make([]string, 0, len(r.Channels))
	for _, channel := range r.Channels {
		out = append(out, channel.ID)
	}
	return out
}
