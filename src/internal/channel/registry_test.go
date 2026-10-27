package channel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// stubFactory 是注册表测试用的工厂, 记录 Create 收到的参数并返回可配置的结果
//
// stubFactory is the factory used by the registry tests, recording the arguments Create
// received and returning a configurable outcome
type stubFactory struct {
	typ       string
	createErr error

	gotID       string
	gotSettings json.RawMessage
	calls       int
}

// Type 返回该工厂注册的 type
//
// Type returns the type this factory registers
func (f *stubFactory) Type() string { return f.typ }

// Describe 返回描述键
//
// Describe returns the description key
func (f *stubFactory) Describe() string { return "example.desc" }

// Fields 返回一份字段元数据, 供接口形态测试使用
//
// Fields returns one field metadata entry for the interface shape test
func (f *stubFactory) Fields() []Field {
	return []Field{{Name: "url", Type: "string", Required: true, DescriptionKey: "example.field.url.desc"}}
}

// Create 记录参数并返回结果, 用于断言 Create 是否原样转发
//
// Create records the arguments and returns the outcome, which lets a test assert that Create
// forwards both unchanged
func (f *stubFactory) Create(id string, settings json.RawMessage) (Channel, error) {
	f.calls++
	f.gotID = id
	f.gotSettings = settings
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &stubChannel{id: id, typ: f.typ}, nil
}

// stubChannel 是一个最小的渠道实现, 只满足接口
//
// stubChannel is a minimal channel implementation that satisfies the interfaces only
type stubChannel struct {
	id  string
	typ string
}

// ID 返回实例标识
//
// ID returns the instance identifier
func (c *stubChannel) ID() string { return c.id }

// Type 返回渠道类型
//
// Type returns the channel type
func (c *stubChannel) Type() string { return c.typ }

// Prepare 返回一个不做任何事的 Prepared, 让接口组合可编译
//
// Prepare returns a Prepared that does nothing, which keeps the interface combination
// compilable
func (c *stubChannel) Prepare(ctx context.Context, msg Message) (Prepared, []apperr.Problem, error) {
	return &stubPrepared{}, nil, nil
}

// 编译期断言: 桩实现保持与冻结契约的接口一致
// Compile-time assertions: the stubs stay identical to the frozen contract interfaces
var (
	_ Factory = (*stubFactory)(nil)
	_ Channel = (*stubChannel)(nil)
)

// stubPrepared 是最小的 Prepared 实现
//
// stubPrepared is a minimal Prepared implementation
type stubPrepared struct{}

// Send 不做任何网络操作并报告成功
//
// Send performs no network work and reports success
func (p *stubPrepared) Send(ctx context.Context, client *http.Client) error { return nil }

// Preview 返回一份预览 Problem, 供渲染链路测试使用
//
// Preview returns a preview Problem for the rendering-chain test
func (p *stubPrepared) Preview() apperr.Problem {
	return apperr.Problem{ID: "channel.example.preview.message", Data: map[string]any{"Targets": 1}}
}

var _ Prepared = (*stubPrepared)(nil)

// resetRegistry 清空注册表, 让每个测试从已知状态开始
// 注册表是包级状态, 测试之间会互相影响, 因此用 t.Cleanup 恢复原值
//
// resetRegistry empties the registry so each test starts from a known state
// The registry is package-level state that tests would otherwise share, so t.Cleanup restores
// the original value
func resetRegistry(t *testing.T) {
	t.Helper()
	registryMu.Lock()
	savedRegistry := registry
	savedOrder := order
	registry = map[string]Factory{}
	order = nil
	registryMu.Unlock()

	t.Cleanup(func() {
		registryMu.Lock()
		registry = savedRegistry
		order = savedOrder
		registryMu.Unlock()
	})
}

// TestRegister_AndRegisteredTypes 覆盖注册顺序在 RegisteredTypes 中的保留
//
// TestRegister_AndRegisteredTypes covers the registration order surviving into RegisteredTypes
func TestRegister_AndRegisteredTypes(t *testing.T) {
	resetRegistry(t)
	for _, typ := range []string{"example_a", "example_b", "example_c"} {
		Register(&stubFactory{typ: typ})
	}
	got := RegisteredTypes()
	want := []string{"example_a", "example_b", "example_c"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("RegisteredTypes() = %v, want %v", got, want)
	}

	got[0] = "example_mutated"
	if RegisteredTypes()[0] != "example_a" {
		t.Fatal("RegisteredTypes() exposed the internal order slice to callers")
	}
}

// TestRegister_DuplicatePanics 覆盖重复 type 注册触发 panic
//
// TestRegister_DuplicatePanics covers a duplicate type registration panicking
func TestRegister_DuplicatePanics(t *testing.T) {
	resetRegistry(t)
	Register(&stubFactory{typ: "example"})
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("a duplicate registration did not panic")
		}
		if !strings.Contains(recovered.(string), "example") {
			t.Fatalf("panic %v should name the duplicated type", recovered)
		}
	}()
	Register(&stubFactory{typ: "example"})
}

// TestRegister_NilAndEmpty 覆盖 nil 工厂与空 type 的注册守卫
//
// TestRegister_NilAndEmpty covers the registration guards for a nil factory and an empty type
func TestRegister_NilAndEmpty(t *testing.T) {
	cases := []struct {
		name string
		f    Factory
	}{
		{name: "nil factory", f: nil},
		{name: "empty type", f: &stubFactory{typ: ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetRegistry(t)
			defer func() {
				if recover() == nil {
					t.Fatalf("Register(%s) did not panic", tc.name)
				}
			}()
			Register(tc.f)
		})
	}
}

// TestIsRegistered 覆盖已注册与未注册的判定
//
// TestIsRegistered covers the verdict for a registered type and an unregistered one
func TestIsRegistered(t *testing.T) {
	resetRegistry(t)
	Register(&stubFactory{typ: "example"})
	if !IsRegistered("example") {
		t.Fatal("IsRegistered reported false for a registered type")
	}
	if IsRegistered("example_absent") {
		t.Fatal("IsRegistered reported true for an unregistered type")
	}
}

// TestFactoryByType 覆盖按 type 取工厂: 已注册返回同一工厂, 未注册返回 false
//
// TestFactoryByType covers the factory lookup by type: a registered type returns the same factory
// and an unregistered one returns false
func TestFactoryByType(t *testing.T) {
	resetRegistry(t)
	want := &stubFactory{typ: "example"}
	Register(want)

	got, ok := FactoryByType("example")
	if !ok || got != want {
		t.Fatalf("FactoryByType(example) = %v, %v; want the registered factory", got, ok)
	}
	if f, ok := FactoryByType("example_absent"); ok || f != nil {
		t.Fatalf("FactoryByType(absent) = %v, %v; want nil, false", f, ok)
	}
}

// TestCreate 覆盖已注册 type 的转发与工厂错误的原样返回
//
// TestCreate covers forwarding for a registered type and an error from the factory coming back
// as it was
func TestCreate(t *testing.T) {
	t.Run("known type", func(t *testing.T) {
		resetRegistry(t)
		f := &stubFactory{typ: "example"}
		Register(f)

		raw := json.RawMessage(`{"url":"https://example.com"}`)
		ch, err := Create("example-id", "example", raw)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if ch.ID() != "example-id" || ch.Type() != "example" {
			t.Fatalf("created channel = %s/%s", ch.ID(), ch.Type())
		}
		if f.calls != 1 || f.gotID != "example-id" || string(f.gotSettings) != string(raw) {
			t.Fatalf("factory received id=%q settings=%s calls=%d", f.gotID, f.gotSettings, f.calls)
		}
	})

	t.Run("factory error", func(t *testing.T) {
		resetRegistry(t)
		sentinel := apperr.Permanent(&apperr.Problem{ID: "channel.example.error.missing_url"})
		Register(&stubFactory{typ: "example", createErr: sentinel})

		ch, err := Create("example-id", "example", nil)
		if ch != nil {
			t.Fatal("Create returned a channel alongside an error")
		}
		if !errors.Is(err, sentinel) {
			t.Fatalf("Create error = %v, want the factory error unchanged", err)
		}
		if !apperr.IsPermanent(err) {
			t.Fatal("Create lost the permanent marker of the factory error")
		}
	})
}

// TestCreate_UnknownType 覆盖未知 type 的 Problem: ID, Type 与 Available
//
// TestCreate_UnknownType covers the Problem for an unknown type: its ID, Type and Available
func TestCreate_UnknownType(t *testing.T) {
	resetRegistry(t)
	Register(&stubFactory{typ: "example_a"})
	Register(&stubFactory{typ: "example_b"})

	ch, err := Create("example-id", "example_absent", nil)
	if ch != nil {
		t.Fatal("Create returned a channel for an unknown type")
	}
	var problem *apperr.Problem
	if !errors.As(err, &problem) {
		t.Fatalf("Create error = %T, want *apperr.Problem", err)
	}
	if problem.ID != "channel.error.unknown_type" {
		t.Fatalf("ID = %q", problem.ID)
	}
	if problem.Data["Type"] != "example_absent" {
		t.Fatalf("Data[Type] = %v", problem.Data["Type"])
	}
	if got := problem.Data["Available"]; got != "example_a, example_b" {
		t.Fatalf("Data[Available] = %v, want the registered types in order", got)
	}
}
