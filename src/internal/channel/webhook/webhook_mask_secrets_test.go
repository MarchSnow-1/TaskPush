package webhook

/*
maskSecrets 的测试: 该字段的语义是「每一项都必须是渲染后完整 URL 的子串」
匹配成功的片段打成 ***, 一个都匹配不上时整条地址不显示并追加告警
这条规则让用户能声明凭据片段, 同时保证猜不准时不把凭据显示出来

Tests for maskSecrets, whose rule is "every entry has to be a substring of the rendered full URL"
A matching fragment becomes ***, and when nothing matches the whole address is hidden with a warning
The rule lets a user declare the credential fragment while an uncertain guess shows no credential
*/

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
)

// prepareWithMask 用给定的 url 与 maskSecrets 做一次 Prepare, 返回 Prepared 与警告
//
// prepareWithMask runs one Prepare with the given url and maskSecrets, returning the Prepared and
// the warnings
func prepareWithMask(t *testing.T, url, maskSecrets string) (channel.Prepared, []apperr.Problem) {
	t.Helper()
	raw := `{"url":"` + url + `","maskSecrets":` + maskSecrets + `}`
	ch, err := (factory{}).Create("example", []byte(raw))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	prepared, warns, err := ch.Prepare(context.Background(), channel.Message{})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return prepared, warns
}

// previewURLText 取出预览里的地址文本
//
// previewURLText reads the address text out of a preview
func previewURLText(prepared channel.Prepared) string {
	urlText, _ := prepared.Preview().Data["URL"].(string)
	return urlText
}

// TestMaskSecretsReplacesDeclaredFragment 覆盖声明的片段被替换, 其余路径保持可读
//
// TestMaskSecretsReplacesDeclaredFragment covers the declared fragment being replaced while the rest
// of the path stays readable
func TestMaskSecretsReplacesDeclaredFragment(t *testing.T) {
	prepared, warns := prepareWithMask(t,
		"https://hooks.example.com/services/T1/B2/example-secret",
		`["example-secret"]`)

	got := previewURLText(prepared)
	if strings.Contains(got, "example-secret") {
		t.Fatalf("preview URL = %q, want the declared fragment masked", got)
	}
	if !strings.Contains(got, "services/T1/B2") {
		t.Fatalf("preview URL = %q, want the harmless path kept", got)
	}
	for _, warning := range warns {
		if warning.ID == keyWarnMaskMiss {
			t.Fatalf("warnings = %v, want no mask-miss warning for a matching fragment", warns)
		}
	}
}

// TestMaskSecretsMatchesWholeURLNotJustPath 覆盖片段可以落在 query 或主机部分
// 规则要求它是完整 URL 的子串, 而不是仅限于 path
//
// TestMaskSecretsMatchesWholeURLNotJustPath covers a fragment landing in the query or the host
// The rule asks for a substring of the full URL rather than a path segment only
func TestMaskSecretsMatchesWholeURLNotJustPath(t *testing.T) {
	prepared, _ := prepareWithMask(t,
		"https://example.com/hook?signature=example-signature",
		`["signature=example-signature"]`)

	got := previewURLText(prepared)
	if strings.Contains(got, "example-signature") {
		t.Fatalf("preview URL = %q, want the fragment in the query masked", got)
	}
}

// TestMaskSecretsAllMissHidesWholeURL 覆盖一条都匹配不上时整条地址不显示并告警
// 此时无法判断凭据位置, 显示完整地址就有泄漏风险
//
// TestMaskSecretsAllMissHidesWholeURL covers hiding the whole address with a warning when nothing
// matches
// The credential position is then unknown, so showing the full address risks a leak
func TestMaskSecretsAllMissHidesWholeURL(t *testing.T) {
	prepared, warns := prepareWithMask(t,
		"https://hooks.example.com/services/T1/B2/example-secret",
		`["a-fragment-that-is-absent"]`)

	got := previewURLText(prepared)
	if !strings.Contains(got, "<redacted-url>") {
		t.Fatalf("preview URL = %q, want the whole address hidden", got)
	}
	if strings.Contains(got, "hooks.example.com") || strings.Contains(got, "example-secret") {
		t.Fatalf("preview URL = %q, want no part of the address left", got)
	}

	missed := 0
	allMissed := 0
	for _, warning := range warns {
		switch warning.ID {
		case keyWarnMaskMiss:
			missed++
			if warning.Data["Path"] != "settings.maskSecrets" {
				t.Fatalf("warning Path = %v, want settings.maskSecrets", warning.Data["Path"])
			}
		case keyWarnMaskAllMiss:
			allMissed++
		}
	}
	if missed != 1 || allMissed != 1 {
		t.Fatalf("warnings = %v, want one per-entry miss and one all-miss", warns)
	}
}

// TestMaskSecretsPartialMissHidesWholeURL 覆盖部分失配时整条地址也不显示
// 任意一项对不上说明用户对地址形态的判断与实际不符, 剩余部分哪一段含凭据不可知,
// 因此宁可不显示, 只保留逐项告警与整条隐藏的说明
//
// TestMaskSecretsPartialMissHidesWholeURL covers the whole address being hidden on a partial miss too
// One absent entry means the user's reading of the address shape differs from the real one, which
// leaves the credential position unknown in the remaining part, so showing less wins and only the
// per-entry warning plus the explanation remain
func TestMaskSecretsPartialMissHidesWholeURL(t *testing.T) {
	prepared, warns := prepareWithMask(t,
		"https://example.com/services/T1/example-secret",
		`["example-secret","absent-fragment"]`)

	got := previewURLText(prepared)
	if !strings.Contains(got, "<redacted-url>") {
		t.Fatalf("preview URL = %q, want the whole address hidden on a partial miss", got)
	}
	if strings.Contains(got, "example.com") || strings.Contains(got, "example-secret") {
		t.Fatalf("preview URL = %q, want no part of the address left", got)
	}

	missed := 0
	allMissed := 0
	for _, warning := range warns {
		switch warning.ID {
		case keyWarnMaskMiss:
			missed++
		case keyWarnMaskAllMiss:
			allMissed++
		}
	}
	if missed != 1 {
		t.Fatalf("per-entry warnings = %d, want exactly one for the absent fragment", missed)
	}
	if allMissed != 1 {
		t.Fatalf("all-miss warnings = %d, want exactly one hiding the whole address", allMissed)
	}
}

// TestMaskSecretsEmptyEntriesRejected 覆盖空片段在 Create 阶段被拒
//
// TestMaskSecretsEmptyEntriesRejected covers an empty entry being refused at Create
func TestMaskSecretsEmptyEntriesRejected(t *testing.T) {
	raw := `{"url":"https://example.com/hook","maskSecrets":[""]}`
	_, err := (factory{}).Create("example", []byte(raw))
	if err == nil {
		t.Fatal("Create accepted an empty maskSecrets entry")
	}
	var prob *apperr.Problem
	if !apperr.IsPermanent(err) {
		t.Fatalf("error = %v, want a permanent error", err)
	}
	if !errors.As(err, &prob) || prob.ID != keyErrorMaskEmpty {
		t.Fatalf("error = %v, want Problem ID %s", err, keyErrorMaskEmpty)
	}
}

// TestMaskSecretsAbsentKeepsHeuristic 覆盖不写 maskSecrets 时改用路径启发式判断
// 长末段被打码, 短末段与动作名末段保持可读
//
// TestMaskSecretsAbsentKeepsHeuristic covers the path heuristic taking over when maskSecrets is absent
// A long trailing segment is masked, while a short one and an action-name one stay readable
func TestMaskSecretsAbsentKeepsHeuristic(t *testing.T) {
	long, _ := prepareWithMask(t, "https://hooks.example.com/services/T1/B2/example-secret-token-abc", `null`)
	if got := previewURLText(long); strings.Contains(got, "example-secret-token-abc") {
		t.Fatalf("preview URL = %q, want the long trailing segment masked by the heuristic", got)
	}

	short, _ := prepareWithMask(t, "https://example.com/notify", `null`)
	if got := previewURLText(short); got != "https://example.com/notify" {
		t.Fatalf("preview URL = %q, want the readable path kept", got)
	}
}
