package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/config"
)

// ---------------------------------------------------------------------------
// 调度入参的关键字测试: Version 与 Translate 经 Options/CheckOptions 下发
// 本包把版本号与译文查表函数都放在入参里, 因此这两个字段必须一路传到 NewClient 与 Message
//
// Tests for the scheduling arguments: Version and Translate travel through Options and CheckOptions
// This package keeps both the version and the translation lookup in the arguments, so the two fields
// have to reach NewClient and Message
// ---------------------------------------------------------------------------

// TestSend_CarriesVersionIntoUserAgent 覆盖 Options.Version 经 Send 传到 NewClient 的 UA
// 假渠道用调度层给的 client 向 httptest 服务器发一次 GET, 由服务器记录真实请求头
//
// TestSend_CarriesVersionIntoUserAgent covers Options.Version reaching the NewClient User-Agent
// through Send
// The fake channel fires one GET at a httptest server through the client from the scheduling layer,
// and the server records the real request header
func TestSend_CarriesVersionIntoUserAgent(t *testing.T) {
	cases := []struct {
		name    string
		version string
		want    string
	}{
		{name: "version from options", version: "9.9.9", want: "taskpush/9.9.9"},
		{name: "padded version is trimmed", version: "  1.2.3  ", want: "taskpush/1.2.3"},
		{name: "empty version falls back to dev", version: "", want: "taskpush/dev"},
		{name: "blank version falls back to dev", version: "   ", want: "taskpush/dev"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("User-Agent")
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()

			resetFake()
			spec := testChannel("example", fakeSettings{ProbeURL: server.URL})
			results, err := Send(context.Background(), defaultTestDefaults(), []config.Channel{spec}, Options{Version: tc.version})
			if err != nil {
				t.Fatalf("Send: %v", err)
			}
			if results[0].Error != nil {
				t.Fatalf("channel error = %v", results[0].Error)
			}
			if got != tc.want {
				t.Errorf("User-Agent = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSend_CarriesTranslateIntoMessage 覆盖 Options.Translate 填进 Message.Translate
// nil 时不代填任何函数, 渠道因此回退到自己的语言无关常量
//
// TestSend_CarriesTranslateIntoMessage covers Options.Translate reaching Message.Translate
// A nil lookup leaves the field nil, which makes a channel fall back to its own language-neutral
// constant
func TestSend_CarriesTranslateIntoMessage(t *testing.T) {
	t.Run("lookup from options", func(t *testing.T) {
		resetFake()
		lookup := func(string) string { return "example-copy" }
		results, err := Send(context.Background(), defaultTestDefaults(), []config.Channel{testChannel("example", fakeSettings{})}, Options{Translate: lookup})
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if results[0].Error != nil {
			t.Fatalf("channel error = %v", results[0].Error)
		}
		got := recordOf("example").Translate
		if got == nil {
			t.Fatal("Message.Translate is nil, want the lookup from Options")
		}
		if text := got("channel.example.some_key"); text != "example-copy" {
			t.Errorf("Translate(key) = %q, want %q", text, "example-copy")
		}
	})

	t.Run("nil lookup stays nil", func(t *testing.T) {
		resetFake()
		results, err := Send(context.Background(), defaultTestDefaults(), []config.Channel{testChannel("example", fakeSettings{})}, Options{})
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if results[0].Error != nil {
			t.Fatalf("channel error = %v", results[0].Error)
		}
		if got := recordOf("example").Translate; got != nil {
			t.Error("Message.Translate is non-nil, want nil when Options carries no lookup")
		}
	})
}

// TestCheck_CarriesTranslateIntoMessage 覆盖 CheckOptions.Translate 填进 Message.Translate
// check 不发送, 因此没有版本字段可传, 这里只断言译文查表函数
//
// TestCheck_CarriesTranslateIntoMessage covers CheckOptions.Translate reaching Message.Translate
// check sends nothing and therefore carries no version, so only the translation lookup is asserted
func TestCheck_CarriesTranslateIntoMessage(t *testing.T) {
	t.Run("lookup from check options", func(t *testing.T) {
		resetFake()
		lookup := func(string) string { return "example-copy" }
		results, err := Check(context.Background(), defaultTestDefaults(), []config.Channel{testChannel("example", fakeSettings{})}, CheckOptions{Translate: lookup})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if results[0].Error != nil {
			t.Fatalf("channel error = %v", results[0].Error)
		}
		got := recordOf("example").Translate
		if got == nil {
			t.Fatal("Message.Translate is nil, want the lookup from CheckOptions")
		}
		if text := got("channel.example.some_key"); text != "example-copy" {
			t.Errorf("Translate(key) = %q, want %q", text, "example-copy")
		}
	})

	t.Run("nil lookup stays nil", func(t *testing.T) {
		resetFake()
		results, err := Check(context.Background(), defaultTestDefaults(), []config.Channel{testChannel("example", fakeSettings{})}, CheckOptions{})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if results[0].Error != nil {
			t.Fatalf("channel error = %v", results[0].Error)
		}
		if got := recordOf("example").Translate; got != nil {
			t.Error("Message.Translate is non-nil, want nil when CheckOptions carries no lookup")
		}
	})
}
