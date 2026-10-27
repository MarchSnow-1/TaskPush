package config

import (
	"strings"
	"testing"
)

// TestSelect_Empty 覆盖空 ids 返回全部启用渠道并按配置顺序排列
//
// TestSelect_Empty covers an empty ids list returning every enabled channel in configuration
// order
func TestSelect_Empty(t *testing.T) {
	root := loadJSON(t, `{"channels":[
		{"id":"first","type":"example_alpha","settings":{}},
		{"id":"disabled","type":"example_alpha","enabled":false},
		{"id":"second","type":"example_beta","settings":{}}
	]}`)
	selected, err := root.Select(nil)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(selected) != 2 || selected[0].ID != "first" || selected[1].ID != "second" {
		t.Fatalf("selected = %+v, want first and second", selected)
	}
}

// TestSelect_Subset 覆盖子集选择按配置顺序而不是 ids 顺序
//
// TestSelect_Subset covers a subset coming back in configuration order rather than ids order
func TestSelect_Subset(t *testing.T) {
	root := loadJSON(t, `{"channels":[
		{"id":"first","type":"example_alpha","settings":{}},
		{"id":"second","type":"example_beta","settings":{}},
		{"id":"third","type":"example_alpha","settings":{}}
	]}`)
	selected, err := root.Select([]string{"third", "first"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(selected) != 2 || selected[0].ID != "first" || selected[1].ID != "third" {
		t.Fatalf("selected = %+v, want configuration order", selected)
	}
}

// TestSelect_Dedup 覆盖 ids 去重与首尾空白
//
// TestSelect_Dedup covers deduplication and surrounding whitespace in ids
func TestSelect_Dedup(t *testing.T) {
	root := loadJSON(t, `{"channels":[{"id":"first","type":"example_alpha","settings":{}}]}`)
	selected, err := root.Select([]string{" first ", "first", "first"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(selected) != 1 || selected[0].ID != "first" {
		t.Fatalf("selected = %+v", selected)
	}
}

// TestSelect_Errors 覆盖空目标, 未知目标, 禁用目标与无启用渠道
//
// TestSelect_Errors covers an empty target, an unknown target, a disabled target and no enabled
// channel
func TestSelect_Errors(t *testing.T) {
	root := loadJSON(t, `{"channels":[
		{"id":"first","type":"example_alpha","settings":{}},
		{"id":"disabled","type":"example_alpha","enabled":false}
	]}`)

	if _, err := root.Select([]string{"   "}); err == nil {
		t.Fatal("Select with a blank id returned no error")
	} else {
		assertProblemID(t, err, "config.error.empty_target")
	}

	_, err := root.Select([]string{"missing"})
	assertProblemID(t, err, "config.error.unknown_target")
	if !strings.Contains(err.Error(), "unknown_target") {
		t.Fatalf("error = %v", err)
	}

	root2 := loadJSON(t, `{"channels":[{"id":"disabled","type":"example_alpha","enabled":false}]}`)
	_, err = root2.Select([]string{"disabled"})
	assertProblemID(t, err, "config.error.disabled_target")

	_, err = root2.Select(nil)
	assertProblemID(t, err, "config.error.no_channels")
}

// TestSelect_Available 覆盖未知目标错误里的可用列表包含禁用渠道
//
// TestSelect_Available covers the available list of an unknown-target error including disabled
// channels
func TestSelect_Available(t *testing.T) {
	root := loadJSON(t, `{"channels":[
		{"id":"first","type":"example_alpha","settings":{}},
		{"id":"disabled","type":"example_alpha","enabled":false}
	]}`)
	_, err := root.Select([]string{"missing"})
	problem := asProblemForTest(t, err, "config.error.unknown_target")
	available, _ := problem.Data["Available"].(string)
	if !strings.Contains(available, "first") || !strings.Contains(available, "disabled") {
		t.Fatalf("Available = %q, want both ids", available)
	}
}
