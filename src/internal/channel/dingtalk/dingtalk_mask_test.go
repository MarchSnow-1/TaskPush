package dingtalk

/*
dingtalk 渠道在状态码错误上遮蔽 access_token 的测试
access_token 在请求 URL 的 query 里, 服务端会把它原样回显进错误响应体, 因此非 2xx 的摘要必须
同时遮蔽 secret 与 access_token

Tests for masking the access_token on a status-code error in the DingTalk channel
The access_token sits in the query of the request URL, which a server echoes as it arrived inside an
error body, so the non-2xx summary has to mask the secret and the access_token together
*/

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// TestSend_StatusErrorMasksEchoedAccessToken 覆盖 400 响应体回显 access_token 时的遮蔽
// maskServerText 同时替换 secret 与 access_token, 回显值不留在 Data["Body"]
//
// TestSend_StatusErrorMasksEchoedAccessToken covers masking when a 400 body echoes the access_token
// maskServerText replaces the secret and the access_token together, so the echoed value does not
// stay in Data["Body"]
func TestSend_StatusErrorMasksEchoedAccessToken(t *testing.T) {
	server := newServer(t, responseStub{
		status: http.StatusBadRequest,
		body:   "rejected access_token=" + exampleToken + " for this robot",
	})
	prepared := prepareAgainst(t, server, settingsFor(server, `{"url":"TESTSERVER"}`), "", "example body")

	err := prepared.Send(context.Background(), server.Client())
	prob := mustProblem(t, err)
	if prob.ID != "common.error.http_status" {
		t.Fatalf("ID = %q, want common.error.http_status", prob.ID)
	}
	body, _ := prob.Data["Body"].(string)
	if strings.Contains(body, exampleToken) {
		t.Fatalf("Data[Body] = %q, want the echoed access_token masked", body)
	}
	if !strings.Contains(body, "***") {
		t.Fatalf("Data[Body] = %q, want it to hold the mask", body)
	}
	if !apperr.IsPermanent(err) {
		t.Fatal("a 400 was not marked permanent")
	}
	if prob.Cause != nil {
		t.Fatalf("Cause = %v, want nil", prob.Cause)
	}
}

// TestSend_StatusErrorMasksEchoedSecret 覆盖同一路径上 secret 仍然被遮蔽, 两个值都要挡
//
// TestSend_StatusErrorMasksEchoedSecret covers the secret still being masked on the same path, since
// both values need hiding
func TestSend_StatusErrorMasksEchoedSecret(t *testing.T) {
	const secret = "SECechosecret"
	server := newServer(t, responseStub{
		status: http.StatusForbidden,
		body:   "bad sign for " + secret + " and " + exampleToken,
	})
	prepared := prepareAgainst(t, server, settingsFor(server, `{"url":"TESTSERVER","secret":"`+secret+`"}`), "", "example body")

	err := prepared.Send(context.Background(), server.Client())
	prob := mustProblem(t, err)
	body, _ := prob.Data["Body"].(string)
	for _, value := range []string{secret, exampleToken} {
		if strings.Contains(body, value) {
			t.Fatalf("Data[Body] = %q, want every echoed credential masked", body)
		}
	}
}

// TestPrepareStoresAccessToken 验证 Prepare 把 access_token 存进 preparedChannel
// maskServerText 依赖这个字段, 字段为空时遮蔽不会生效
//
// TestPrepareStoresAccessToken checks that Prepare keeps the access_token in preparedChannel
// maskServerText depends on that field, and an empty one would leave the masking with no effect
func TestPrepareStoresAccessToken(t *testing.T) {
	server := newServer(t, responseStub{body: `{"errcode":0}`})
	prepared := prepareAgainst(t, server, settingsFor(server, `{"url":"TESTSERVER"}`), "", "example body")
	if prepared.accessToken != exampleToken {
		t.Fatalf("accessToken = %q, want %q", prepared.accessToken, exampleToken)
	}
}

// TestAccessTokenOf 覆盖取值与两种取不到的情形: 参数缺失与地址无法解析
//
// TestAccessTokenOf covers the value together with the two ways of finding none: a missing parameter
// and an unparseable address
func TestAccessTokenOf(t *testing.T) {
	if got := accessTokenOf("https://example.com/robot/send?access_token=" + exampleToken); got != exampleToken {
		t.Errorf("accessTokenOf = %q, want %q", got, exampleToken)
	}
	if got := accessTokenOf("https://example.com/robot/send"); got != "" {
		t.Errorf("accessTokenOf = %q, want an empty string", got)
	}
	if got := accessTokenOf("://bad address"); got != "" {
		t.Errorf("accessTokenOf = %q, want an empty string", got)
	}
}

// TestMaskServerTextMasksBothValues 直接覆盖遮蔽函数, 确认 secret 与 access_token 都被替换
//
// TestMaskServerTextMasksBothValues covers the masking function directly, confirming that both the
// secret and the access_token are replaced
func TestMaskServerTextMasksBothValues(t *testing.T) {
	const secret = "SECechosecret"
	p := &preparedChannel{secret: secret, accessToken: exampleToken}
	in := "secret " + secret + " token " + exampleToken
	got := p.maskServerText(in)
	if strings.Contains(got, secret) || strings.Contains(got, exampleToken) {
		t.Fatalf("maskServerText = %q, want both credentials masked", got)
	}
	if !strings.Contains(got, "***") {
		t.Fatalf("maskServerText = %q, want it to hold the mask", got)
	}
}
