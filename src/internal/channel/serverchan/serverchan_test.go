package serverchan

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// newTestChannel 用占位配置构造一个 serverchan 渠道
// 测试数据统一使用 example 形态的通用占位值
//
// newTestChannel builds a serverchan channel from placeholder settings
// Test data uses the generic example form throughout
func newTestChannel(t *testing.T, settingsJSON string) *serverchanChannel {
	t.Helper()
	created, err := factory{}.Create("sc", json.RawMessage(settingsJSON))
	if err != nil {
		t.Fatalf("Create(%s): %v", settingsJSON, err)
	}
	ch, ok := created.(*serverchanChannel)
	if !ok {
		t.Fatalf("Create returned %T, want *serverchanChannel", created)
	}
	return ch
}

// prepare 调用 Prepare 并把返回值收敛为具体的 Prepared 类型
//
// prepare calls Prepare and narrows the result to the concrete Prepared type
func prepare(t *testing.T, ch *serverchanChannel, msg channel.Message) (*preparedChannel, []apperr.Problem) {
	t.Helper()
	got, warnings, err := ch.Prepare(context.Background(), msg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	prepared, ok := got.(*preparedChannel)
	if !ok {
		t.Fatalf("Prepare returned %T, want *preparedChannel", got)
	}
	return prepared, warnings
}

// TestFactory_Fields 覆盖字段元数据的完整性
//
// TestFactory_Fields covers the completeness of the field metadata
func TestFactory_Fields(t *testing.T) {
	f := factory{}
	if got := f.Type(); got != "serverchan" {
		t.Fatalf("Type() = %q", got)
	}
	if got := f.Describe(); got != keyDesc {
		t.Fatalf("Describe() = %q, want %q", got, keyDesc)
	}

	want := []channel.Field{
		{Name: "token", Type: "string", Required: true, DescriptionKey: keyFieldToken},
		{Name: "tags", Type: "list<string>", Required: false, DescriptionKey: keyFieldTags},
		{Name: "short", Type: "string", Required: false, DescriptionKey: keyFieldShort},
		{Name: "channels", Type: "list<string>", Required: false, DescriptionKey: keyFieldChannels},
		{Name: "openids", Type: "list<string>", Required: false, DescriptionKey: keyFieldOpenIDs},
	}
	if got := f.Fields(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Fields() = %#v, want %#v", got, want)
	}
	for _, field := range f.Fields() {
		if field.DescriptionKey == "" || !strings.HasPrefix(field.DescriptionKey, "channel.serverchan.field.") {
			t.Errorf("field %q has an unusable description key %q", field.Name, field.DescriptionKey)
		}
		if field.Required && field.Name != "token" {
			t.Errorf("field %q should not be required", field.Name)
		}
	}
}

// TestCreate_Strict 覆盖缺必填, 未知键与各字段类型错误
//
// TestCreate_Strict covers a missing required field, unknown keys and type errors per field
func TestCreate_Strict(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{name: "missing token", raw: `{}`},
		{name: "blank token", raw: `{"token":"   "}`},
		{name: "unknown key", raw: `{"token":"example-token","nope":1}`},
		{name: "not an object", raw: `[]`},
		{name: "tags wrong type", raw: `{"token":"example-token","tags":1}`},
		{name: "tags element wrong type", raw: `{"token":"example-token","tags":["a",1]}`},
		{name: "channels wrong type", raw: `{"token":"example-token","channels":true}`},
		{name: "openids wrong type", raw: `{"token":"example-token","openids":{"a":1}}`},
		{name: "short wrong type", raw: `{"token":"example-token","short":42}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			created, err := factory{}.Create("sc", json.RawMessage(tc.raw))
			if err == nil {
				t.Fatalf("Create(%s) succeeded with %#v", tc.raw, created)
			}
			if !apperr.IsPermanent(err) {
				t.Fatalf("Create(%s) error is not permanent: %v", tc.raw, err)
			}
		})
	}
}

// TestCreate_AcceptsStringLists 覆盖数组与逗号串两种列表写法
//
// TestCreate_AcceptsStringLists covers the array and comma-string spellings of a list field
func TestCreate_AcceptsStringLists(t *testing.T) {
	ch := newTestChannel(t, `{"token":"example-token","tags":["a","b"],"channels":"c1,c2","openids":"o1、o2"}`)
	if got := ch.s.Tags.Strings(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("tags = %v", got)
	}
	if got := ch.s.Channels.Strings(); !reflect.DeepEqual(got, []string{"c1", "c2"}) {
		t.Fatalf("channels = %v", got)
	}
	if got := ch.s.OpenIDs.Strings(); !reflect.DeepEqual(got, []string{"o1", "o2"}) {
		t.Fatalf("openids = %v", got)
	}
}

// TestPrepare_Routes 覆盖 Turbo 与 SC3 的端点, 模式与字段差异
//
// TestPrepare_Routes covers the endpoints, modes and field differences of Turbo and SC3
func TestPrepare_Routes(t *testing.T) {
	t.Run("turbo", func(t *testing.T) {
		ch := newTestChannel(t, `{"token":"example-token"}`)
		prepared, warnings := prepare(t, ch, channel.Message{Title: "Build done", Body: "commit abc123"})
		if len(warnings) != 0 {
			t.Fatalf("warnings = %v", warnings)
		}
		want := "https://sctapi.ftqq.com/example-token.send"
		if prepared.endpoint != want {
			t.Fatalf("endpoint = %q, want %q", prepared.endpoint, want)
		}
		if prepared.mode != modeTurbo {
			t.Fatalf("mode = %q, want %q", prepared.mode, modeTurbo)
		}
		if prepared.payload.Title != "Build done" || prepared.payload.Desp != "commit abc123" {
			t.Fatalf("payload = %#v", prepared.payload)
		}
		if prepared.payload.Tags != "" || prepared.payload.Channel != "" || prepared.payload.OpenID != "" {
			t.Fatalf("turbo payload carried sc3-only fields: %#v", prepared.payload)
		}
	})

	t.Run("sc3", func(t *testing.T) {
		const token = "sctp123tEXAMPLEKEY"
		ch := newTestChannel(t, `{
			"token":"`+token+`",
			"tags":["ci","build"],
			"channels":["c1","c2"],
			"openids":["o1","o2"],
			"short":"build summary"
		}`)
		prepared, warnings := prepare(t, ch, channel.Message{Title: "Build done"})
		if len(warnings) != 0 {
			t.Fatalf("warnings = %v", warnings)
		}
		want := "https://123.push.ft07.com/send/" + token + ".send"
		if prepared.endpoint != want {
			t.Fatalf("endpoint = %q, want %q", prepared.endpoint, want)
		}
		if prepared.mode != modeSC3 {
			t.Fatalf("mode = %q, want %q", prepared.mode, modeSC3)
		}
		if prepared.payload.Tags != "ci|build" {
			t.Fatalf("tags = %q", prepared.payload.Tags)
		}
		if prepared.payload.Channel != "c1|c2" {
			t.Fatalf("channel = %q", prepared.payload.Channel)
		}
		if prepared.payload.OpenID != "o1|o2" {
			t.Fatalf("openid = %q", prepared.payload.OpenID)
		}
		if prepared.payload.Short != "build summary" {
			t.Fatalf("short = %q", prepared.payload.Short)
		}
		if prepared.payload.Desp != "" {
			t.Fatalf("desp = %q, want empty", prepared.payload.Desp)
		}
	})
}

// TestPrepare_TurboDropsSC3Fields 覆盖 Turbo 路由不发送 SC3 扩展字段, 但保留 short
// spec 第 2 节把 tags/channels/openids 标为 SC3/SCT 字段, 只有 short 未限定路由
//
// TestPrepare_TurboDropsSC3Fields covers a Turbo route sending none of the SC3 extension fields
// while keeping short
// Spec section 2 labels tags, channels and openids as SC3/SCT fields, and only short is not tied to
// a route
func TestPrepare_TurboDropsSC3Fields(t *testing.T) {
	ch := newTestChannel(t, `{
		"token":"example-token",
		"tags":["ci"],
		"channels":["c1"],
		"openids":["o1"],
		"short":"summary"
	}`)
	prepared, warnings := prepare(t, ch, channel.Message{Title: "Build done"})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if prepared.payload.Tags != "" || prepared.payload.Channel != "" || prepared.payload.OpenID != "" {
		t.Fatalf("turbo payload carried sc3 fields: %#v", prepared.payload)
	}
	if prepared.payload.Short != "summary" {
		t.Fatalf("short = %q, want it kept on the turbo route", prepared.payload.Short)
	}
}

// TestPrepare_SC3KeepsSC3Fields 覆盖 SC3 路由发送 tags/channels/openids
//
// TestPrepare_SC3KeepsSC3Fields covers an SC3 route sending tags, channels and openids
func TestPrepare_SC3KeepsSC3Fields(t *testing.T) {
	ch := newTestChannel(t, `{
		"token":"sctp123tEXAMPLEKEY",
		"tags":["ci","build"],
		"channels":["c1","c2"],
		"openids":["o1","o2"],
		"short":"summary"
	}`)
	prepared, warnings := prepare(t, ch, channel.Message{Title: "Build done"})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if prepared.payload.Tags != "ci|build" || prepared.payload.Channel != "c1|c2" || prepared.payload.OpenID != "o1|o2" {
		t.Fatalf("sc3 payload = %#v", prepared.payload)
	}
	if prepared.payload.Short != "summary" {
		t.Fatalf("short = %q", prepared.payload.Short)
	}
}

// TestPrepare_InvalidSC3Key 覆盖 sctp 开头但形态不符的 key
// 错误数据只带掩码, 不含完整 SendKey
//
// TestPrepare_InvalidSC3Key covers a key starting with sctp but shaped differently
// Error data carries a mask only, never the complete SendKey
func TestPrepare_InvalidSC3Key(t *testing.T) {
	const token = "sctpBADKEY"
	ch := newTestChannel(t, `{"token":"`+token+`"}`)
	got, _, err := ch.Prepare(context.Background(), channel.Message{Title: "Build done"})
	if err == nil {
		t.Fatalf("Prepare succeeded with %#v", got)
	}
	if !apperr.IsPermanent(err) {
		t.Fatalf("error is not permanent: %v", err)
	}
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("error = %T, want *apperr.Problem", err)
	}
	if prob.ID != keyErrorInvalidSC3 {
		t.Fatalf("ID = %q, want %q", prob.ID, keyErrorInvalidSC3)
	}
	masked, _ := prob.Data["TokenMasked"].(string)
	if masked == "" || strings.Contains(masked, token) {
		t.Fatalf("TokenMasked = %q, want a mask without the full token", masked)
	}
}

// TestPrepare_EmptyTitle 覆盖标题必填与正文可空
// 判空按渲染结果原样判断, 只拒字面空串; 空白标题按原样发送, 因为产品规格禁止自动改写内容
//
// TestPrepare_EmptyTitle covers the required title and the optional body
// Emptiness is judged on the rendered text as it arrived, so only a literally empty title is
// rejected; a whitespace-only title goes out as written, since the product spec forbids an
// automatic content rewrite
func TestPrepare_EmptyTitle(t *testing.T) {
	ch := newTestChannel(t, `{"token":"example-token"}`)
	_, _, err := ch.Prepare(context.Background(), channel.Message{Title: "", Body: "body"})
	if err == nil {
		t.Fatal("Prepare accepted an empty title")
	}
	var prob *apperr.Problem
	if !errors.As(err, &prob) || prob.ID != keyErrorEmptyTitle {
		t.Fatalf("error = %v, want %q", err, keyErrorEmptyTitle)
	}
	if !apperr.IsPermanent(err) {
		t.Fatalf("empty title error is not permanent: %v", err)
	}

	prepared, _ := prepare(t, ch, channel.Message{Title: "   ", Body: "body"})
	if prepared.payload.Title != "   " {
		t.Fatalf("title = %q, want the whitespace kept", prepared.payload.Title)
	}

	prepared, _ = prepare(t, ch, channel.Message{Title: "Build done"})
	if prepared.payload.Desp != "" {
		t.Fatalf("desp = %q, want empty", prepared.payload.Desp)
	}
	prepared, _ = prepare(t, ch, channel.Message{Title: "Build done", Body: "  body  "})
	if prepared.payload.Desp != "  body  " {
		t.Fatalf("desp = %q, want the body untrimmed", prepared.payload.Desp)
	}
}

// TestPrepare_TurboTitleTruncation 覆盖 Turbo 按 32 rune 截断与 SC3 不截断
//
// TestPrepare_TurboTitleTruncation covers the 32-rune Turbo cut and the absence of one on SC3
func TestPrepare_TurboTitleTruncation(t *testing.T) {
	long := strings.Repeat("界", turboTitleLimit+5)

	t.Run("turbo", func(t *testing.T) {
		ch := newTestChannel(t, `{"token":"example-token"}`)
		prepared, warnings := prepare(t, ch, channel.Message{Title: long})
		if got := []rune(prepared.payload.Title); len(got) != turboTitleLimit {
			t.Fatalf("title runes = %d, want %d", len(got), turboTitleLimit)
		}
		if prepared.payload.Title != string([]rune(long)[:turboTitleLimit]) {
			t.Fatalf("title = %q", prepared.payload.Title)
		}
		if len(warnings) != 1 || warnings[0].ID != keyWarnTitleCut {
			t.Fatalf("warnings = %v, want one %q", warnings, keyWarnTitleCut)
		}
		if got := warnings[0].Data["Limit"]; got != turboTitleLimit {
			t.Fatalf("warning Limit = %#v, want %d", got, turboTitleLimit)
		}
	})

	t.Run("sc3", func(t *testing.T) {
		ch := newTestChannel(t, `{"token":"sctp123tEXAMPLEKEY"}`)
		prepared, warnings := prepare(t, ch, channel.Message{Title: long})
		if prepared.payload.Title != long {
			t.Fatalf("sc3 title was cut to %q", prepared.payload.Title)
		}
		if len(warnings) != 0 {
			t.Fatalf("warnings = %v, want none", warnings)
		}
	})
}

// TestPrepare_ListNormalization 覆盖列表逐项渲染, 分隔符归一与空项丢弃
//
// TestPrepare_ListNormalization covers per-item rendering, separator normalisation and dropped empties
func TestPrepare_ListNormalization(t *testing.T) {
	ch := newTestChannel(t, `{
		"token":"sctp123tEXAMPLEKEY",
		"tags":["  a  ","b、c","，","{{tags}}"],
		"channels":"c1，c2",
		"openids":["o1|o2","","o3"]
	}`)
	vars := template.NewVars()
	vars.Set("tags", "d,e")
	prepared, warnings := prepare(t, ch, channel.Message{Title: "Build done", Vars: vars})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if prepared.payload.Tags != "a|b|c|d|e" {
		t.Fatalf("tags = %q", prepared.payload.Tags)
	}
	if prepared.payload.Channel != "c1|c2" {
		t.Fatalf("channel = %q", prepared.payload.Channel)
	}
	if prepared.payload.OpenID != "o1|o2|o3" {
		t.Fatalf("openid = %q", prepared.payload.OpenID)
	}
}

// TestPrepare_TemplateFields 覆盖 token/short/列表字段的模板渲染
// 严格模式未解析变量返回错误, check 模式保留字面并告警
//
// TestPrepare_TemplateFields covers template rendering of token, short and the list fields
// An unresolved variable is an error under strict mode and stays literal with a warning under check
func TestPrepare_TemplateFields(t *testing.T) {
	ch := newTestChannel(t, `{
		"token":"{{token}}",
		"short":"build {{id}}",
		"tags":["{{tags}}"],
		"channels":"{{channels}}",
		"openids":["{{openid}}"]
	}`)

	vars := template.NewVars()
	vars.Set("token", "sctp123tEXAMPLEKEY")
	vars.Set("id", "abc123")
	vars.Set("tags", "ci,build")
	vars.Set("channels", "c1,c2")
	vars.Set("openid", "o1")
	prepared, warnings := prepare(t, ch, channel.Message{Title: "Build done", Vars: vars})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if !strings.Contains(prepared.endpoint, "sctp123tEXAMPLEKEY") {
		t.Fatalf("endpoint = %q", prepared.endpoint)
	}
	if prepared.payload.Short != "build abc123" {
		t.Fatalf("short = %q", prepared.payload.Short)
	}
	if prepared.payload.Tags != "ci|build" || prepared.payload.Channel != "c1|c2" || prepared.payload.OpenID != "o1" {
		t.Fatalf("payload = %#v", prepared.payload)
	}

	_, _, err := ch.Prepare(context.Background(), channel.Message{Title: "Build done"})
	if err == nil {
		t.Fatal("strict mode accepted unresolved template variables")
	}
	if !apperr.IsPermanent(err) {
		t.Fatalf("template error is not permanent: %v", err)
	}

	check, warnings, err := ch.Prepare(context.Background(), channel.Message{
		Title: "Build done",
		Mode:  channel.RenderCheck,
	})
	if err != nil {
		t.Fatalf("check mode: %v", err)
	}
	if check == nil {
		t.Fatal("check mode returned no Prepared")
	}
	if len(warnings) == 0 || warnings[0].ID != "template.warn.unresolved" {
		t.Fatalf("warnings = %v, want template.warn.unresolved", warnings)
	}
}

// TestSend_Success 覆盖 code=0 的成功路径与请求体
//
// TestSend_Success covers the code=0 success path and the request body
func TestSend_Success(t *testing.T) {
	var gotPath string
	var gotBody sendPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("Content-Type = %q", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		_, _ = w.Write([]byte(`{"code":0,"message":"ok"}`))
	}))
	defer server.Close()

	original := serverchanEndpoint
	serverchanEndpoint = func(token string) (string, string, error) {
		endpoint, mode, err := original(token)
		if err != nil {
			return "", "", err
		}
		parsed, err := urlReplaceHost(endpoint, server.URL)
		if err != nil {
			return "", "", err
		}
		return parsed, mode, nil
	}
	defer func() { serverchanEndpoint = original }()

	ch := newTestChannel(t, `{"token":"example-token"}`)
	prepared, _ := prepare(t, ch, channel.Message{Title: "Build done", Body: "commit abc123"})
	if err := prepared.Send(context.Background(), server.Client()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != "/example-token.send" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody.Title != "Build done" || gotBody.Desp != "commit abc123" {
		t.Fatalf("request body = %#v", gotBody)
	}
}

// TestSend_WireBodyRoutesSC3Fields 覆盖线路上的请求体: Turbo 不带 SC3 扩展字段, SC3 带
// 断言原始 JSON 键集合, 因为 struct 断言看不到 omitempty 与路由裁剪在序列化后的效果
//
// TestSend_WireBodyRoutesSC3Fields covers the request body on the wire: Turbo carries no SC3
// extension field while SC3 carries them
// The raw JSON key set is asserted, since a struct assertion does not show what omitempty and the
// route gate produce after serialisation
func TestSend_WireBodyRoutesSC3Fields(t *testing.T) {
	cases := []struct {
		name       string
		token      string
		wantSC3    bool
		wantKeys   []string
		rejectKeys []string
	}{
		{
			name:       "turbo",
			token:      "example-token",
			wantKeys:   []string{"desp", "short", "title"},
			rejectKeys: []string{"tags", "channel", "openid"},
		},
		{
			name:       "sc3",
			token:      "sctp123tEXAMPLEKEY",
			wantSC3:    true,
			wantKeys:   []string{"channel", "desp", "openid", "short", "tags", "title"},
			rejectKeys: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotBody map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
					t.Errorf("decode request body: %v", err)
				}
				_, _ = w.Write([]byte(`{"code":0}`))
			}))
			defer server.Close()

			original := serverchanEndpoint
			serverchanEndpoint = func(token string) (string, string, error) {
				endpoint, mode, err := original(token)
				if err != nil {
					return "", "", err
				}
				rewritten, err := urlReplaceHost(endpoint, server.URL)
				if err != nil {
					return "", "", err
				}
				return rewritten, mode, nil
			}
			defer func() { serverchanEndpoint = original }()

			ch := newTestChannel(t, `{
				"token":"`+tc.token+`",
				"tags":["ci","build"],
				"channels":["c1"],
				"openids":["o1"],
				"short":"summary"
			}`)
			prepared, _ := prepare(t, ch, channel.Message{Title: "Build done", Body: "commit abc123"})
			if err := prepared.Send(context.Background(), server.Client()); err != nil {
				t.Fatalf("Send: %v", err)
			}

			keys := make([]string, 0, len(gotBody))
			for key := range gotBody {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			if !reflect.DeepEqual(keys, tc.wantKeys) {
				t.Fatalf("request body keys = %v, want %v (body %v)", keys, tc.wantKeys, gotBody)
			}
			for _, key := range tc.rejectKeys {
				if _, ok := gotBody[key]; ok {
					t.Fatalf("turbo body carried %q: %v", key, gotBody)
				}
			}
			if tc.wantSC3 {
				if gotBody["tags"] != "ci|build" || gotBody["channel"] != "c1" || gotBody["openid"] != "o1" {
					t.Fatalf("sc3 extension fields = %v", gotBody)
				}
			}
		})
	}
}

// TestSend_APIError 覆盖业务码非 0 的永久与瞬时分类
// 瞬时用例取 45009, 它属于 common 的共享瞬态码表; 该表按数字全局生效, 本用例只锁定 ServerChan
// 沿用 common.VerifyCode 的分类结果
//
// TestSend_APIError covers the permanent and transient classes of a non-zero business code
// The transient case uses 45009, which the shared transient table of common holds; that table keys
// on the number alone, and this test only pins the classification result ServerChan inherits from
// common.VerifyCode
func TestSend_APIError(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		permanent bool
	}{
		{name: "permanent", body: `{"code":40001,"message":"bad key"}`, permanent: true},
		{name: "transient", body: `{"code":45009,"message":"too many requests"}`, permanent: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			prepared, restore := prepareWithServer(t, server, `{"token":"example-token"}`)
			defer restore()

			err := prepared.Send(context.Background(), server.Client())
			if err == nil {
				t.Fatal("Send accepted a non-zero business code")
			}
			if apperr.IsPermanent(err) != tc.permanent {
				t.Fatalf("IsPermanent = %v, want %v (err %v)", apperr.IsPermanent(err), tc.permanent, err)
			}
			var prob *apperr.Problem
			if !errors.As(err, &prob) || prob.ID != keyErrorAPICode {
				t.Fatalf("error = %v, want %q", err, keyErrorAPICode)
			}
		})
	}
}

// TestSend_HTTPStatus 覆盖 HTTP 4xx 的永久分类与 5xx 的瞬时分类
//
// TestSend_HTTPStatus covers the permanent class of HTTP 4xx and the transient class of 5xx
func TestSend_HTTPStatus(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		permanent bool
	}{
		{name: "4xx", status: http.StatusBadRequest, permanent: true},
		{name: "5xx", status: http.StatusInternalServerError, permanent: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("example failure"))
			}))
			defer server.Close()

			prepared, restore := prepareWithServer(t, server, `{"token":"example-token"}`)
			defer restore()

			err := prepared.Send(context.Background(), server.Client())
			if err == nil {
				t.Fatalf("Send accepted status %d", tc.status)
			}
			if apperr.IsPermanent(err) != tc.permanent {
				t.Fatalf("IsPermanent = %v, want %v (err %v)", apperr.IsPermanent(err), tc.permanent, err)
			}
			var prob *apperr.Problem
			if !errors.As(err, &prob) || prob.ID != "common.error.http_status" {
				t.Fatalf("error = %v, want common.error.http_status", err)
			}
		})
	}
}

// TestSend_BadResponse 覆盖坏 JSON 与空 body 的协议错误
//
// TestSend_BadResponse covers the protocol errors of invalid JSON and an empty body
func TestSend_BadResponse(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "bad json", body: `{"code":`},
		{name: "empty body", body: ``},
		{name: "missing code", body: `{"message":"ok"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			prepared, restore := prepareWithServer(t, server, `{"token":"example-token"}`)
			defer restore()

			err := prepared.Send(context.Background(), server.Client())
			if err == nil {
				t.Fatal("Send accepted a malformed response")
			}
			if apperr.IsPermanent(err) {
				t.Fatalf("protocol error is permanent: %v", err)
			}
			var prob *apperr.Problem
			if !errors.As(err, &prob) || prob.ID != keyErrorBadResponse {
				t.Fatalf("error = %v, want %q", err, keyErrorBadResponse)
			}
		})
	}
}

// TestPreview 覆盖 Turbo/SC3 预览且不含 token
//
// TestPreview covers the Turbo and SC3 previews and the absence of a token
func TestPreview(t *testing.T) {
	const sc3Token = "sctp123tEXAMPLEKEY"
	cases := []struct {
		name  string
		token string
		mode  string
	}{
		{name: "turbo", token: "example-token", mode: modeTurbo},
		{name: "sc3", token: sc3Token, mode: modeSC3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := newTestChannel(t, `{"token":"`+tc.token+`"}`)
			prepared, _ := prepare(t, ch, channel.Message{Title: "Build done"})
			preview := prepared.Preview()
			if preview.ID != keyPreviewMessage {
				t.Fatalf("preview ID = %q", preview.ID)
			}
			if got := preview.Data["Mode"]; got != tc.mode {
				t.Fatalf("Mode = %#v, want %q", got, tc.mode)
			}
			encoded, err := json.Marshal(preview)
			if err != nil {
				t.Fatalf("marshal preview: %v", err)
			}
			if strings.Contains(string(encoded), tc.token) {
				t.Fatalf("preview leaked the token: %s", encoded)
			}
		})
	}
}

// TestCatalog_Consistency 覆盖 en/zh-CN 的键集合与占位符集合一致
// 测试直接读目录文件, 渠道包按契约不导入 i18n
//
// TestCatalog_Consistency covers the matching key and placeholder sets of en and zh-CN
// The test reads the catalog files directly, since a channel package does not import i18n under the
// contract
func TestCatalog_Consistency(t *testing.T) {
	en := readCatalog(t, filepath.Join("..", "..", "i18n", "catalog", "en", "channel_serverchan.json"))
	zh := readCatalog(t, filepath.Join("..", "..", "i18n", "catalog", "zh-CN", "channel_serverchan.json"))

	wantKeys := []string{
		keyDesc,
		keyFieldToken,
		keyFieldTags,
		keyFieldShort,
		keyFieldChannels,
		keyFieldOpenIDs,
		keyErrorMissingToken,
		keyErrorInvalidSC3,
		keyErrorEmptyTitle,
		keyErrorAPICode,
		keyErrorBadResponse,
		keyWarnTitleCut,
		keyPreviewMessage,
	}
	sort.Strings(wantKeys)
	if got := sortedKeys(en); !reflect.DeepEqual(got, wantKeys) {
		t.Fatalf("en keys = %v, want %v", got, wantKeys)
	}
	if got := sortedKeys(zh); !reflect.DeepEqual(got, wantKeys) {
		t.Fatalf("zh-CN keys = %v, want %v", got, wantKeys)
	}
	for _, key := range wantKeys {
		if strings.TrimSpace(en[key]) == "" || strings.TrimSpace(zh[key]) == "" {
			t.Errorf("key %q has empty copy in one language", key)
		}
		if got, want := placeholders(en[key]), placeholders(zh[key]); !reflect.DeepEqual(got, want) {
			t.Errorf("key %q: en placeholders %v, zh-CN placeholders %v", key, got, want)
		}
	}
}

// prepareWithServer 把端点注入到本地 httptest 服务后准备一次发送
//
// prepareWithServer injects the endpoint into a local httptest server and prepares one send
func prepareWithServer(t *testing.T, server *httptest.Server, settingsJSON string) (*preparedChannel, func()) {
	t.Helper()
	original := serverchanEndpoint
	serverchanEndpoint = func(token string) (string, string, error) {
		endpoint, mode, err := original(token)
		if err != nil {
			return "", "", err
		}
		rewritten, err := urlReplaceHost(endpoint, server.URL)
		if err != nil {
			return "", "", err
		}
		return rewritten, mode, nil
	}
	ch := newTestChannel(t, settingsJSON)
	prepared, _ := prepare(t, ch, channel.Message{Title: "Build done"})
	return prepared, func() { serverchanEndpoint = original }
}

// urlReplaceHost 把官方端点替换为本地测试服务的 scheme/host, 保留 path
//
// urlReplaceHost replaces the official endpoint scheme and host with those of the local test
// server, keeping the path
func urlReplaceHost(endpoint, local string) (string, error) {
	original, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	replacement, err := url.Parse(local)
	if err != nil {
		return "", err
	}
	return replacement.Scheme + "://" + replacement.Host + original.Path, nil
}

// readCatalog 读取一个 catalog JSON 文件
//
// readCatalog reads one catalog JSON file
func readCatalog(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var catalog map[string]string
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return catalog
}

// sortedKeys 返回 catalog 的键并排序
//
// sortedKeys returns the catalog keys in sorted order
func sortedKeys(catalog map[string]string) []string {
	keys := make([]string, 0, len(catalog))
	for key := range catalog {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// placeholderPattern 匹配文案里的 {{.Name}} 占位符
//
// placeholderPattern matches a {{.Name}} placeholder inside copy
var placeholderPattern = regexp.MustCompile(`\{\{\.([A-Za-z0-9_]+)\}\}`)

// placeholders 返回文案里的占位符名, 排序去重
//
// placeholders returns the placeholder names of one message, sorted and deduplicated
func placeholders(text string) []string {
	seen := map[string]bool{}
	for _, match := range placeholderPattern.FindAllStringSubmatch(text, -1) {
		seen[match[1]] = true
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TestSend_DoesNotRetryItself 覆盖 Send 不自行 sleep 或重试: 每次调用只发一次请求
//
// TestSend_DoesNotRetryItself covers Send performing no wait or retry of its own: every call
// performs one request only
func TestSend_DoesNotRetryItself(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "example failure")
	}))
	defer server.Close()

	prepared, restore := prepareWithServer(t, server, `{"token":"example-token"}`)
	defer restore()
	_ = prepared.Send(context.Background(), server.Client())
	if calls != 1 {
		t.Fatalf("requests = %d, want 1", calls)
	}
}
