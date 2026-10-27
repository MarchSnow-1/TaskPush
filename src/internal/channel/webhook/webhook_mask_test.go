package webhook

/*
webhook 渠道在状态码错误上遮蔽服务端回显凭据的测试
common.Check2xx 的 Body 摘要只按 scheme://... 形态打码, 裸 token 会原样留下, 因此这里让
本地服务端把收到的 header 与 query 凭据回显进 400 响应体, 断言 Data["Body"] 里不再有明文

Tests for masking the credentials a server echoes back on a status-code error in the webhook channel
The Body summary of common.Check2xx masks the scheme://... form alone, which leaves a bare token as it
is, so the local server echoes the header and query credentials it received into a 400 body and these
tests assert that no cleartext is left in Data["Body"]
*/

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
)

// echoStatusServer 起一个把收到的 header 与 query 值回显进响应体的服务端
// 回显文本里既有裸 token, 也有拼接成 URL 形态的值, 两条打码路径因此都被覆盖
//
// echoStatusServer starts a server that echoes the header and query values it received into the body
// The echoed text holds both a bare token and a value joined into the URL form, so both masking paths
// are covered
func echoStatusServer(t *testing.T, status int, echo func(*http.Request) string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(echo(r)))
	}))
}

// sendPrepared 用给定 settings 文本准备并发往服务端, 返回 Send 的错误
//
// sendPrepared prepares the given settings text, sends it to the server and returns the Send error
func sendPrepared(t *testing.T, settingsText string, server *httptest.Server) error {
	t.Helper()
	ch, err := (factory{}).Create("example", json.RawMessage(settingsText))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	prepared, _, err := ch.Prepare(context.Background(), channel.Message{})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return prepared.Send(context.Background(), server.Client())
}

// TestSend_MasksEchoedHeaderToken 验证服务端回显的 header token 不出现在 Data["Body"]
// 遮蔽由 maskStatusError 完成, 因为 common.Check2xx 的摘要只按 URL 形态打码
//
// TestSend_MasksEchoedHeaderToken checks that a header token echoed by the server does not appear in
// Data["Body"]
// maskStatusError does the masking, since the common.Check2xx summary only masks the URL form
func TestSend_MasksEchoedHeaderToken(t *testing.T) {
	const token = "example-header-token"
	server := echoStatusServer(t, http.StatusBadRequest, func(r *http.Request) string {
		return "authorization rejected: " + r.Header.Get("Authorization")
	})
	defer server.Close()

	settingsText := fmt.Sprintf(
		`{"url":%q,"headers":{"Authorization":%q},"body":"abc"}`,
		server.URL+"/hook", token)
	err := sendPrepared(t, settingsText, server)
	if err == nil {
		t.Fatal("Send returned nil error, want a status-code failure")
	}

	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("error = %T, want *apperr.Problem", err)
	}
	if prob.ID != "common.error.http_status" {
		t.Fatalf("ID = %q, want common.error.http_status", prob.ID)
	}
	body, _ := prob.Data["Body"].(string)
	if strings.Contains(body, token) {
		t.Fatalf("Data[Body] = %q, want the echoed token masked", body)
	}
	if !strings.Contains(body, "***") {
		t.Fatalf("Data[Body] = %q, want it to hold the mask", body)
	}
}

// TestSend_KeepsStatusAndPermanence 验证重包后 Status 仍在 Data, 且 4xx 保持不可重试
//
// TestSend_KeepsStatusAndPermanence checks that Status stays in Data after the re-wrap and that a 4xx
// stays non-retryable
func TestSend_KeepsStatusAndPermanence(t *testing.T) {
	server := echoStatusServer(t, http.StatusBadRequest, func(r *http.Request) string {
		return "nope"
	})
	defer server.Close()

	err := sendPrepared(t, fmt.Sprintf(`{"url":%q,"body":"abc"}`, server.URL+"/hook"), server)
	if err == nil {
		t.Fatal("Send returned nil error, want a status-code failure")
	}
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("error = %T, want *apperr.Problem", err)
	}
	if got := prob.Data["Status"]; got != http.StatusBadRequest {
		t.Fatalf("Data[Status] = %v, want 400", got)
	}
	if !apperr.IsPermanent(err) {
		t.Fatal("a 400 was not marked permanent")
	}
}

// TestSend_StatusErrorCauseIsNil 验证重包后的 Problem 不带内层 Problem
// 内层 Problem 进 Cause 会让渲染层把内部键追加到用户文案尾部, 因此这里按渲染层的规则模拟
// 一次追加: Cause 为 nil 时不产生任何追加
//
// TestSend_StatusErrorCauseIsNil checks that the re-wrapped Problem carries no inner Problem
// An inner Problem in Cause would make the rendering layer append an internal key to the user copy,
// so this test simulates that append following the rendering rule: a nil Cause produces no append
func TestSend_StatusErrorCauseIsNil(t *testing.T) {
	server := echoStatusServer(t, http.StatusInternalServerError, func(r *http.Request) string {
		return "boom"
	})
	defer server.Close()

	err := sendPrepared(t, fmt.Sprintf(`{"url":%q,"body":"abc"}`, server.URL+"/hook"), server)
	if err == nil {
		t.Fatal("Send returned nil error, want a status-code failure")
	}
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("error = %T, want *apperr.Problem", err)
	}
	if prob.Cause != nil {
		t.Fatalf("Cause = %v, want nil", prob.Cause)
	}
	// 渲染层在 Cause 非空时把 Cause.Error() 追加在文案之后, 内层 Problem 的文本就是内部键
	//
	// The rendering layer appends Cause.Error() after the copy when Cause is set, and the text of an
	// inner Problem is exactly that internal key
	const userCopy = "the server replied with a status"
	rendered := userCopy
	if prob.Cause != nil {
		rendered += ": " + common.SanitizeString(prob.Cause.Error())
	}
	if strings.Contains(rendered, "common.error.") {
		t.Fatalf("rendered = %q, want no internal key appended", rendered)
	}
}

// TestSend_MasksEchoedQuerySecret 验证地址敏感 query 参数值被回显时同样被遮蔽
//
// TestSend_MasksEchoedQuerySecret checks that a sensitive query parameter echoed back from the
// address gets masked as well
func TestSend_MasksEchoedQuerySecret(t *testing.T) {
	const token = "example-query-token"
	server := echoStatusServer(t, http.StatusForbidden, func(r *http.Request) string {
		return "rejected token=" + r.URL.Query().Get("token")
	})
	defer server.Close()

	settingsText := fmt.Sprintf(`{"url":%q,"body":"abc"}`, server.URL+"/hook?token="+token)
	err := sendPrepared(t, settingsText, server)
	if err == nil {
		t.Fatal("Send returned nil error, want a status-code failure")
	}
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("error = %T, want *apperr.Problem", err)
	}
	body, _ := prob.Data["Body"].(string)
	if strings.Contains(body, token) {
		t.Fatalf("Data[Body] = %q, want the echoed query value masked", body)
	}
}

// TestPrepareEndpointSecretsCoverSensitiveQuery 校验 endpointSecrets 覆盖的五个参数名与契约一致
// 集合漂移 (任一侧增删) 会让某个名字的取值不再被遮蔽, 该测试因此按契约逐个确认
// path 末段也进入 secrets, 所以这里断言的是「包含凭据」而不是「只含凭据」
//
// TestPrepareEndpointSecretsCoverSensitiveQuery checks that the five parameter names endpointSecrets
// covers match the contract
// A drift in either set would leave the value of some name unmasked, so this test confirms the
// contract list name by name
// The trailing path segment reaches secrets as well, so the assertion is "holds the credential"
// rather than "holds the credential alone"
func TestPrepareEndpointSecretsCoverSensitiveQuery(t *testing.T) {
	want := []string{"token", "key", "access_token", "sign", "secret"}
	if len(sensitiveQueryNames) != len(want) {
		t.Fatalf("sensitiveQueryNames = %v, want %v", sensitiveQueryNames, want)
	}
	for _, name := range want {
		raw := "https://example.com/hook?" + name + "=example-credential"
		got := endpointSecrets(raw)
		if !containsSecret(got, "example-credential") {
			t.Fatalf("%s: endpointSecrets = %v, want the credential value", name, got)
		}
		// MaskURL 必须把同一个参数打码, 否则两处判定已经不一致
		//
		// MaskURL has to mask the same parameter, otherwise the two decisions already disagree
		if masked := common.MaskURL(raw); strings.Contains(masked, "example-credential") {
			t.Fatalf("%s: MaskURL = %q, want the value masked", name, masked)
		}
	}
}

// containsSecret 报告列表里是否含某个值
//
// containsSecret reports whether the list holds one value
func containsSecret(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestPrepareEndpointSecretsUserinfo 验证 userinfo 的用户名与口令都进入 secrets
//
// TestPrepareEndpointSecretsUserinfo checks that both the userinfo name and password reach secrets
func TestPrepareEndpointSecretsUserinfo(t *testing.T) {
	got := endpointSecrets("https://example:example-password@example.com/hook")
	if !containsSecret(got, "example") || !containsSecret(got, "example-password") {
		t.Fatalf("endpointSecrets = %v, want the name and the password", got)
	}
}

// TestPrepareEndpointSecretsPathSegment 验证 path 末段进入 secrets 并被 Preview 遮蔽
// 这是 Slack 一类把凭据放在 path 末段的目标所需的遮蔽
//
// TestPrepareEndpointSecretsPathSegment checks that the trailing path segment reaches secrets and
// gets masked by Preview
// A target such as Slack carries its credential in that last segment
func TestPrepareEndpointSecretsPathSegment(t *testing.T) {
	raw := "https://hooks.example.com/services/T00000/B00000/example-secret-token-abc"
	got := endpointSecrets(raw)
	if !containsSecret(got, "example-secret-token-abc") {
		t.Fatalf("endpointSecrets = %v, want the trailing path segment", got)
	}
	ch, err := (factory{}).Create("example", json.RawMessage(`{"url":"`+raw+`"}`))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	prepared, _, err := ch.Prepare(context.Background(), channel.Message{})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	preview := prepared.Preview()
	urlText, _ := preview.Data["URL"].(string)
	if strings.Contains(urlText, "example-secret-token-abc") {
		t.Fatalf("Preview URL = %q, want the path credential masked", urlText)
	}

	// 动作名末段与短末段都不当凭据, 让 /notify 与 /hook 一类端点保持可读
	//
	// Neither an action-name tail nor a short tail counts as a credential, which keeps an endpoint
	// such as /notify or /hook readable
	for _, readable := range []string{
		"https://example.com/notify",
		"https://example.com/hook",
		"https://example.com/services/x",
	} {
		if got := endpointSecrets(readable); len(got) != 0 {
			t.Fatalf("%s: endpointSecrets = %v, want no path credential", readable, got)
		}
		if masked := common.MaskURLWithSecrets(readable, pathCredentialSegments(readable)...); strings.Contains(masked, "***") {
			t.Fatalf("%s: masked = %q, want the readable path kept", readable, masked)
		}
	}
}

// TestPrepareSecretsSkipEmptyValues 验证空 header 值, 空 query 值与根路径都不进入 secrets
//
// TestPrepareSecretsSkipEmptyValues checks that neither an empty header value, an empty query value
// nor a bare root path reaches secrets
func TestPrepareSecretsSkipEmptyValues(t *testing.T) {
	if got := headerSecrets(map[string]string{"X-Empty": "", "X-Value": "example"}); len(got) != 1 || got[0] != "example" {
		t.Fatalf("headerSecrets = %v, want the non-empty value alone", got)
	}
	if got := endpointSecrets("https://example.com/hook?token=&key="); len(got) != 0 {
		t.Fatalf("endpointSecrets = %v, want an empty result for empty query values and an action-name tail", got)
	}
	if got := endpointSecrets("https://example.com/"); len(got) != 0 {
		t.Fatalf("endpointSecrets = %v, want an empty result for a root path", got)
	}
}
