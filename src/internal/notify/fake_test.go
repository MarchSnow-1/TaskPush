package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
	"github.com/MarchSnow-1/TaskPush/internal/config"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// ---------------------------------------------------------------------------
// 测试用假渠道: 经 channel.Register 注册一次, 行为由 settings JSON 控制
// Send/Prepare 的调用次数与观察到的时间等字段记在包级记录表里, 供断言并发与重试语义
// 注册只发生一次 (sync.Once), 因为 channel.Register 对重复 type 直接 panic
//
// The fake channel used by the tests: registered once through channel.Register, with behaviour
// driven by its settings JSON
// Send and Prepare call counts, plus the observed message fields, are kept in a package-level
// record table so concurrency and retry semantics can be asserted
// Registration happens once (sync.Once), since channel.Register panics on a duplicate type
// ---------------------------------------------------------------------------

// fakeType 是测试渠道的注册类型名, 采用通用占位名
//
// fakeType is the registered type name of the test channel, using a generic placeholder
const fakeType = "example"

// fakeSettings 控制假渠道的行为
// FailTimes 表示前若干次 Send 返回可重试错误, Permanent 表示 Send 返回永久错误,
// SendDelayMs 放大单次发送窗口以便观察并发上限
//
// fakeSettings drives the behaviour of the fake channel
// FailTimes makes the first few Send calls return a retryable error, Permanent makes Send return a
// permanent error, and SendDelayMs widens one send so the concurrency cap becomes observable
type fakeSettings struct {
	FailTimes     int    `json:"failTimes"`
	Permanent     bool   `json:"permanent"`
	PrepareFail   bool   `json:"prepareFail"`
	SendDelayMs   int    `json:"sendDelayMs"`
	PrepareWarn   bool   `json:"prepareWarn"`
	SendWarn      bool   `json:"sendWarn"`
	PreviewID     string `json:"previewID"`
	SettingsError bool   `json:"settingsError"`
	// ProbeURL 让 Send 用调度层给的 client 向该地址发一次 GET, 供 UA 断言观察真实请求
	//
	// ProbeURL makes Send fire one GET at the address through the client from the scheduling layer,
	// which lets a UA assertion observe a real request
	ProbeURL string `json:"probeURL"`
	// Deadline 让 Send 返回包着 context.DeadlineExceeded 的错误, 父 context 不取消
	//
	// Deadline makes Send return an error wrapping context.DeadlineExceeded without cancelling the
	// parent context
	Deadline bool `json:"deadline"`
}

// fakeRecord 保存一个渠道实例的观察结果
// Translate 保存 Prepare 收到的译文查表函数, 为 nil 说明调度层没有下发
//
// fakeRecord holds the observations for one channel instance
// Translate keeps the translation lookup Prepare received, and nil means the scheduling layer
// passed none
type fakeRecord struct {
	ID         string
	Title      string
	Body       string
	Mode       channel.RenderMode
	Vars       map[string]any
	Translate  func(key string) string
	UserAgent  string
	PrepareCnt int
	SendCnt    int
}

// fakeState 汇总所有假渠道的观察结果与并发峰值
//
// fakeState gathers the observations of every fake channel together with the concurrency peak
type fakeState struct {
	mu           sync.Mutex
	records      map[string]*fakeRecord
	current      int
	maxCurrent   int
	registerOnce sync.Once
}

var fake = &fakeState{records: map[string]*fakeRecord{}}

// registerFake 注册测试工厂, 重复调用只注册一次
//
// registerFake registers the test factory and registers it once however often it is called
func registerFake() {
	fake.registerOnce.Do(func() {
		channel.Register(fakeFactory{})
		channel.Register(renderFactory{})
	})
}

// fakeFactory 实现 channel.Factory
//
// fakeFactory implements channel.Factory
type fakeFactory struct{}

func (fakeFactory) Type() string     { return fakeType }
func (fakeFactory) Describe() string { return "channel.example.desc" }
func (fakeFactory) Fields() []channel.Field {
	return nil
}

// Create 严格解码 settings 并构造假渠道, 未知字段按错误返回
//
// Create decodes settings strictly and builds the fake channel, reporting an unknown field as an
// error
func (fakeFactory) Create(id string, raw json.RawMessage) (channel.Channel, error) {
	settings := fakeSettings{}
	if len(raw) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&settings); err != nil {
			return nil, &apperr.Problem{
				ID:    "channel.error.invalid_settings",
				Data:  map[string]any{"Field": "", "Detail": err.Error()},
				Cause: err,
			}
		}
	}
	if settings.SettingsError {
		return nil, apperr.Permanent(&apperr.Problem{ID: "channel.example.error.invalid_settings"})
	}
	return &fakeChannel{id: id, settings: settings}, nil
}

// fakeChannel 实现 channel.Channel
//
// fakeChannel implements channel.Channel
type fakeChannel struct {
	id       string
	settings fakeSettings
}

func (c *fakeChannel) ID() string   { return c.id }
func (c *fakeChannel) Type() string { return fakeType }

// Prepare 记录消息内容与渲染模式, 并按 settings 返回警告或错误
//
// Prepare records the message content and render mode and returns a warning or an error according
// to its settings
func (c *fakeChannel) Prepare(_ context.Context, msg channel.Message) (channel.Prepared, []apperr.Problem, error) {
	fake.mu.Lock()
	record := fake.records[c.id]
	if record == nil {
		record = &fakeRecord{ID: c.id}
		fake.records[c.id] = record
	}
	record.PrepareCnt++
	record.Title = msg.Title
	record.Body = msg.Body
	record.Mode = msg.Mode
	record.Translate = msg.Translate
	record.Translate = msg.Translate
	record.Vars = map[string]any{}
	for _, name := range []string{"time", "date", "datetime", "elapsed", "elapsed_ms", "elapsed_s", "fromFile", "fromCLI", "title", "body"} {
		if val, ok := msg.Vars.Resolve(name); ok {
			record.Vars[name] = val
		}
	}
	fake.mu.Unlock()

	if c.settings.PrepareFail {
		return nil, nil, apperr.Permanent(&apperr.Problem{ID: "channel.example.error.prepare"})
	}
	var warns []apperr.Problem
	if c.settings.PrepareWarn {
		warns = append(warns, apperr.Problem{ID: "channel.example.warn.prepare"})
	}
	return &fakePrepared{channel: c}, warns, nil
}

// fakePrepared 实现 channel.Prepared, 并提供可选的 Warnings 方法
//
// fakePrepared implements channel.Prepared and offers the optional Warnings method
type fakePrepared struct {
	channel *fakeChannel
}

// Warnings 返回 Send 阶段的警告, 供调度层的可选接口断言读取
//
// Warnings returns the warnings found while sending, which the scheduler reads through the optional
// interface assertion
func (p *fakePrepared) Warnings() []apperr.Problem {
	if !p.channel.settings.SendWarn {
		return nil
	}
	return []apperr.Problem{{ID: "channel.example.warn.send"}}
}

// Preview 返回脱敏预览, 标识按 settings 覆盖以便区分渠道
//
// Preview returns the redacted preview, with the identifier overridden by settings so channels can
// be told apart
func (p *fakePrepared) Preview() apperr.Problem {
	id := p.channel.settings.PreviewID
	if id == "" {
		id = "channel.example.preview"
	}
	return apperr.Problem{ID: id, Data: map[string]any{"Targets": 1}}
}

// Send 记录一次尝试, 按 settings 返回瞬时错误或永久错误
// 并发计数在这里进出, 峰值留给并发上限断言
// 需要时用收到的 client 发一次 GET, 让 UA 经真实的 transport 链暴露出来
//
// Send records one attempt and returns a transient or a permanent error according to its settings
// The concurrency counter moves in and out here, and the peak is kept for the concurrency-cap
// assertion
// When asked, it fires one GET through the client it received, which exposes the User-Agent through
// the real transport chain
func (p *fakePrepared) Send(ctx context.Context, client *http.Client) error {
	fake.mu.Lock()
	fake.current++
	if fake.current > fake.maxCurrent {
		fake.maxCurrent = fake.current
	}
	record := fake.records[p.channel.id]
	if record == nil {
		record = &fakeRecord{ID: p.channel.id}
		fake.records[p.channel.id] = record
	}
	record.SendCnt++
	count := record.SendCnt
	fake.mu.Unlock()

	if p.channel.settings.ProbeURL != "" {
		// 这次 GET 的意义是让 client 真正发出请求, UA 由测试侧的服务器记录
		//
		// The point of this GET is to make the client send a real request, and the test-side server
		// records the User-Agent
		if resp, err := client.Get(p.channel.settings.ProbeURL); err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}

	delay := time.Duration(p.channel.settings.SendDelayMs) * time.Millisecond
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			decCurrent()
			return ctx.Err()
		case <-timer.C:
		}
	}
	decCurrent()

	if p.channel.settings.Deadline {
		// 模拟 http.Client.Timeout 一类的渠道自身超时: 错误链包着 context.DeadlineExceeded,
		// 但父 context 仍然有效, 因此这只应留在该渠道的 Result.Error 里
		//
		// Stands in for a channel's own timeout such as one from http.Client.Timeout: the error chain
		// wraps context.DeadlineExceeded while the parent context stays alive, so this has to stay
		// inside that channel's Result.Error
		return fmt.Errorf("channel send timed out: %w", context.DeadlineExceeded)
	}
	if p.channel.settings.Permanent {
		return apperr.Permanent(&apperr.Problem{ID: "channel.example.error.permanent"})
	}
	if count <= p.channel.settings.FailTimes {
		return &apperr.Problem{ID: "channel.example.error.transient"}
	}
	return nil
}

// decCurrent 归还在途发送计数
//
// decCurrent returns the in-flight send counter
func decCurrent() {
	fake.mu.Lock()
	fake.current--
	fake.mu.Unlock()
}

// resetFake 清空观察结果与并发峰值, 每个测试开始前调用
//
// resetFake clears the observations and the concurrency peak, called at the start of each test
func resetFake() {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.records = map[string]*fakeRecord{}
	fake.current = 0
	fake.maxCurrent = 0
}

// recordOf 返回某个渠道的观察结果副本, 不存在时返回零值
//
// recordOf returns a copy of one channel's observations, or the zero value when it is absent
func recordOf(id string) fakeRecord {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if record := fake.records[id]; record != nil {
		return *record
	}
	return fakeRecord{}
}

// maxConcurrent 返回观察到的并发峰值
//
// maxConcurrent returns the observed concurrency peak
func maxConcurrent() int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.maxCurrent
}

// ---------------------------------------------------------------------------
// 真实渲染型假渠道: 它像真渠道那样把 common.TemplateMode(msg.Mode) 交给 template.RenderWithMode
// 这样严格/宽松的差异能端到端被观察到, 而不是只断言 Mode 字段
//
// The rendering fake: it hands common.TemplateMode(msg.Mode) to template.RenderWithMode the way a
// real channel does, so the strict and lenient difference becomes observable end to end rather than
// only through the Mode field
// ---------------------------------------------------------------------------

// renderType 是渲染型假渠道的注册类型名
//
// renderType is the registered type name of the rendering fake
const renderType = "example-render"

// renderSettings 控制渲染型假渠道的模板
//
// renderSettings drives the templates of the rendering fake
type renderSettings struct {
	Template string `json:"template"`
}

// renderFactory 实现 channel.Factory
//
// renderFactory implements channel.Factory
type renderFactory struct{}

func (renderFactory) Type() string     { return renderType }
func (renderFactory) Describe() string { return "channel.example.desc" }
func (renderFactory) Fields() []channel.Field {
	return nil
}

// Create 解码 settings 并构造渲染型假渠道
//
// Create decodes settings and builds the rendering fake
func (renderFactory) Create(id string, raw json.RawMessage) (channel.Channel, error) {
	settings := renderSettings{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return nil, &apperr.Problem{ID: "channel.error.invalid_settings", Cause: err}
		}
	}
	return &renderChannel{id: id, settings: settings}, nil
}

// renderChannel 实现 channel.Channel
//
// renderChannel implements channel.Channel
type renderChannel struct {
	id       string
	settings renderSettings
}

func (c *renderChannel) ID() string   { return c.id }
func (c *renderChannel) Type() string { return renderType }

// Prepare 按消息模式渲染模板, 未解析变量在宽松模式下退化成警告
//
// Prepare renders the template in the message mode, and an unresolved variable degrades into a
// warning under the lenient mode
func (c *renderChannel) Prepare(_ context.Context, msg channel.Message) (channel.Prepared, []apperr.Problem, error) {
	out, warns, err := template.RenderWithMode(c.settings.Template, msg.Vars, common.TemplateMode(msg.Mode))
	if err != nil {
		return nil, nil, err
	}

	fake.mu.Lock()
	record := fake.records[c.id]
	if record == nil {
		record = &fakeRecord{ID: c.id}
		fake.records[c.id] = record
	}
	record.Title = out
	record.PrepareCnt++
	fake.mu.Unlock()

	return &renderPrepared{channel: c}, warns, nil
}

// renderPrepared 实现 channel.Prepared
//
// renderPrepared implements channel.Prepared
type renderPrepared struct {
	channel *renderChannel
}

// Preview 返回渲染型假渠道的预览
//
// Preview returns the preview of the rendering fake
func (p *renderPrepared) Preview() apperr.Problem {
	return apperr.Problem{ID: "channel.example.preview", Data: map[string]any{"Targets": 1}}
}

// Send 记录一次尝试并成功, 因为该渠道只参与渲染断言
//
// Send records one attempt and succeeds, since this channel takes part in render assertions only
func (p *renderPrepared) Send(_ context.Context, _ *http.Client) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	record := fake.records[p.channel.id]
	if record == nil {
		record = &fakeRecord{ID: p.channel.id}
		fake.records[p.channel.id] = record
	}
	record.SendCnt++
	return nil
}

// testRenderChannel 构造一个渲染型假渠道条目
//
// testRenderChannel builds a rendering fake channel entry
func testRenderChannel(id, tpl string) config.Channel {
	raw, err := json.Marshal(renderSettings{Template: tpl})
	if err != nil {
		panic(err)
	}
	return config.Channel{ID: id, Type: renderType, Settings: raw}
}
