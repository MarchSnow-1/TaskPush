package config

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/settings"
)

// boolPtr 返回 bool 的指针, 便于构造 Enabled 的三态
//
// boolPtr returns a pointer to a bool, which makes the three states of Enabled easy to build
func boolPtr(v bool) *bool { return &v }

// durationPtr 返回 Duration 的指针
//
// durationPtr returns a pointer to a Duration
func durationPtr(d settings.Duration) *settings.Duration { return &d }

// intPtr 返回 int 的指针
//
// intPtr returns a pointer to an int
func intPtr(v int) *int { return &v }

// TestChannel_IsEnabled 覆盖 Enabled 的三态: nil 视为启用, true 启用, false 禁用
//
// TestChannel_IsEnabled covers the three states of Enabled: nil counts as enabled, true as
// enabled and false as disabled
func TestChannel_IsEnabled(t *testing.T) {
	cases := []struct {
		name    string
		enabled *bool
		want    bool
	}{
		{name: "nil is enabled", enabled: nil, want: true},
		{name: "explicit true", enabled: boolPtr(true), want: true},
		{name: "explicit false", enabled: boolPtr(false), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Channel{ID: "example", Type: "example", Enabled: tc.enabled}
			if got := c.IsEnabled(); got != tc.want {
				t.Fatalf("IsEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRoot_Defaults 覆盖全局默认值的字段映射
//
// TestRoot_Defaults covers the field mapping of the global defaults
func TestRoot_Defaults(t *testing.T) {
	r := &Root{
		Timeout:    settings.Duration(15 * time.Second),
		Retries:    3,
		RetryDelay: settings.Duration(time.Second),
		Proxy:      "http://127.0.0.1:8080",
	}
	got := r.Defaults()
	if got.Timeout != 15*time.Second {
		t.Fatalf("Timeout = %v, want 15s", got.Timeout)
	}
	if got.Retries != 3 {
		t.Fatalf("Retries = %d, want 3", got.Retries)
	}
	if got.RetryDelay != time.Second {
		t.Fatalf("RetryDelay = %v, want 1s", got.RetryDelay)
	}
	if got.Proxy != "http://127.0.0.1:8080" {
		t.Fatalf("Proxy = %q", got.Proxy)
	}

	// 未加载的零值 Root 返回零值 Defaults, 缺省填充由加载逻辑负责
	// A zero Root before loading returns zero Defaults, leaving the default filling to the
	// loading logic
	var zero Root
	if got := zero.Defaults(); got != (Defaults{}) {
		t.Fatalf("zero Root Defaults() = %+v, want the zero Defaults", got)
	}
}

// TestChannel_Effective 覆盖非 nil 字段覆盖全局默认值与 nil 字段保留全局值
//
// TestChannel_Effective covers non-nil fields overriding the global defaults while nil fields
// keep them
func TestChannel_Effective(t *testing.T) {
	base := Defaults{
		Timeout:    15 * time.Second,
		Retries:    3,
		RetryDelay: time.Second,
		Proxy:      "http://127.0.0.1:8080",
	}

	t.Run("all overridden", func(t *testing.T) {
		c := Channel{
			Timeout:    durationPtr(settings.Duration(5 * time.Second)),
			Retries:    intPtr(0),
			RetryDelay: durationPtr(0),
		}
		got, err := c.Effective(base)
		if err != nil {
			t.Fatalf("Effective: %v", err)
		}
		if got.Timeout != 5*time.Second || got.Retries != 0 || got.RetryDelay != 0 {
			t.Fatalf("Effective = %+v, want the channel values", got)
		}
		if got.Proxy != base.Proxy {
			t.Fatalf("Proxy = %q, want the global value", got.Proxy)
		}
	})

	t.Run("none overridden", func(t *testing.T) {
		c := Channel{}
		got, err := c.Effective(base)
		if err != nil {
			t.Fatalf("Effective: %v", err)
		}
		if got != base {
			t.Fatalf("Effective = %+v, want the global defaults unchanged", got)
		}
	})

	t.Run("partial override", func(t *testing.T) {
		c := Channel{Retries: intPtr(7)}
		got, err := c.Effective(base)
		if err != nil {
			t.Fatalf("Effective: %v", err)
		}
		if got.Retries != 7 || got.Timeout != base.Timeout || got.RetryDelay != base.RetryDelay {
			t.Fatalf("Effective = %+v, want only retries replaced", got)
		}
	})
}

// TestChannel_Effective_Validation 覆盖覆盖后的三项校验
//
// TestChannel_Effective_Validation covers the three validation rules applied after overriding
func TestChannel_Effective_Validation(t *testing.T) {
	valid := Defaults{Timeout: 15 * time.Second, Retries: 3, RetryDelay: time.Second}
	cases := []struct {
		name    string
		c       Channel
		base    Defaults
		wantErr string
	}{
		{
			name:    "zero timeout from base",
			c:       Channel{},
			base:    Defaults{Retries: 3, RetryDelay: time.Second},
			wantErr: "timeout",
		},
		{
			name:    "negative timeout from channel",
			c:       Channel{Timeout: durationPtr(settings.Duration(-time.Second))},
			base:    valid,
			wantErr: "timeout",
		},
		{
			name:    "zero timeout from channel",
			c:       Channel{Timeout: durationPtr(0)},
			base:    valid,
			wantErr: "timeout",
		},
		{
			name:    "negative retries",
			c:       Channel{Retries: intPtr(-1)},
			base:    valid,
			wantErr: "retries",
		},
		{
			name:    "negative retry delay",
			c:       Channel{RetryDelay: durationPtr(settings.Duration(-time.Second))},
			base:    valid,
			wantErr: "retryDelay",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.c.Effective(tc.base)
			if err == nil {
				t.Fatalf("Effective = %+v, want an error mentioning %q", got, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want it to mention %q", err, tc.wantErr)
			}
			if got != (Defaults{}) {
				t.Fatalf("Effective returned %+v alongside an error, want the zero Defaults", got)
			}
		})
	}

	// 边界值本身合法: timeout 恰好为正, retries 与 retryDelay 恰好为 0
	// The boundary values are valid themselves: a positive timeout, and retries and retryDelay at 0
	c := Channel{
		Timeout:    durationPtr(settings.Duration(time.Nanosecond)),
		Retries:    intPtr(0),
		RetryDelay: durationPtr(0),
	}
	if _, err := c.Effective(valid); err != nil {
		t.Fatalf("Effective at the boundary values = %v, want nil", err)
	}
}

// TestSource_Label 覆盖三种来源标签的文本
//
// TestSource_Label covers the label text of the three source kinds
func TestSource_Label(t *testing.T) {
	cases := []struct {
		name string
		src  Source
		want string
	}{
		{name: "file", src: Source{Kind: SourceFile, Path: "/example/taskpush.json"}, want: "file:/example/taskpush.json"},
		{name: "stdin", src: Source{Kind: SourceStdin}, want: "stdin"},
		{name: "env", src: Source{Kind: SourceEnv, Path: "TASKPUSH_CONFIG_JSON"}, want: "env:TASKPUSH_CONFIG_JSON"},
		// SourceKind 零值即 SourceFile, 未显式设置时按文件来源标注
		// The zero value of SourceKind is SourceFile, so an unset kind reads as a file source
		{name: "zero kind", src: Source{Path: "/example/taskpush.json"}, want: "file:/example/taskpush.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.src.Label(); got != tc.want {
				t.Fatalf("Label() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRoot_JSONShape 覆盖 Root 与 Channel 的 JSON 标签, 以及 Deferred 不参与序列化
//
// TestRoot_JSONShape covers the JSON tags of Root and Channel along with Deferred staying out
// of serialization
func TestRoot_JSONShape(t *testing.T) {
	raw := `{
		"version": 1,
		"$schema": "https://example.com/schema.json",
		"timeout": "30s",
		"retries": 2,
		"retryDelay": "500ms",
		"proxy": "http://127.0.0.1:8080",
		"channels": [
			{"id": "example", "type": "example_type", "enabled": false, "timeout": "5s", "retries": 1, "retryDelay": "100ms", "settings": {"url": "https://example.com"}}
		]
	}`
	var r Root
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatalf("Unmarshal Root: %v", err)
	}
	if r.Version != 1 || r.Schema != "https://example.com/schema.json" {
		t.Fatalf("Version/Schema = %d/%q", r.Version, r.Schema)
	}
	if r.Timeout.Duration() != 30*time.Second || r.RetryDelay.Duration() != 500*time.Millisecond {
		t.Fatalf("durations = %v/%v", r.Timeout.Duration(), r.RetryDelay.Duration())
	}
	if len(r.Channels) != 1 {
		t.Fatalf("Channels length = %d, want 1", len(r.Channels))
	}
	c := r.Channels[0]
	if c.ID != "example" || c.Type != "example_type" || c.IsEnabled() {
		t.Fatalf("channel = %+v", c)
	}
	if c.Timeout == nil || c.Timeout.Duration() != 5*time.Second {
		t.Fatalf("channel timeout = %v", c.Timeout)
	}
	if c.Retries == nil || *c.Retries != 1 {
		t.Fatalf("channel retries = %v", c.Retries)
	}
	if string(c.Settings) != `{"url": "https://example.com"}` {
		t.Fatalf("Settings = %s, want the raw bytes as configured", c.Settings)
	}
}

// TestRoot_DeferredNotSerialized 覆盖 Deferred 的 json:"-" 标签
//
// TestRoot_DeferredNotSerialized covers the json:"-" tag on Deferred
func TestRoot_DeferredNotSerialized(t *testing.T) {
	r := Root{
		Version:  1,
		Deferred: []apperr.Problem{{ID: "config.error.env_unset"}},
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "Deferred") || strings.Contains(string(data), "env_unset") {
		t.Fatalf("encoded Root = %s, want Deferred left out", data)
	}
}
