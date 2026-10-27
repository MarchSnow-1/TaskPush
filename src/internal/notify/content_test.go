package notify

import (
	"errors"
	"strings"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// TestRenderContentDetailCarriesFlag 覆盖标题/正文渲染失败时 Detail 带上来源 flag
// 验收场景要求未解析变量错误里能看到 --body, 外层文案只有 {{.Detail}} 占位符
//
// TestRenderContentDetailCarriesFlag covers a failed title or body render keeping the source flag in
// Detail
// The acceptance scenario requires --body in an unresolved-variable error, and the outer copy has
// only the {{.Detail}} placeholder
func TestRenderContentDetailCarriesFlag(t *testing.T) {
	vars := template.NewVars()
	_, err := renderContent("{{unknown}}", vars, keyErrorBody, "--body")
	if err == nil {
		t.Fatal("renderContent accepted an unresolved variable")
	}

	var problem *apperr.Problem
	if !errors.As(err, &problem) {
		t.Fatalf("error = %T, want *apperr.Problem", err)
	}
	if problem.ID != keyErrorBody {
		t.Fatalf("Problem.ID = %q, want %q", problem.ID, keyErrorBody)
	}
	detail, _ := problem.Data["Detail"].(string)
	if !strings.Contains(detail, "--body") {
		t.Fatalf("Detail = %q, want it to name --body", detail)
	}
	if !strings.Contains(detail, "unknown") {
		t.Fatalf("Detail = %q, want it to name the unresolved variable", detail)
	}
}
