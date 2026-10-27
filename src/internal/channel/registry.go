package channel

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// ---------------------------------------------------------------------------
// 渠道注册表: 各渠道包在 init() 里注册工厂, 装配包以空白导入触发注册
// 注册顺序被保留, 让 types 与错误里的可用列表在不同运行间保持一致
//
// The channel registry: each channel package registers its factory in init() and the assembly
// package triggers the registration through blank imports
// Registration order is preserved, which keeps the types output and the available list in an
// error consistent between runs
// ---------------------------------------------------------------------------

var (
	// registryMu 保护 registry 与 order 两个变量
	// registryMu guards both registry and order
	registryMu sync.RWMutex
	// registry 以 type 为键保存工厂
	// registry holds the factories keyed by type
	registry = map[string]Factory{}
	// order 按注册顺序保存 type, 供 RegisteredTypes 与可用列表使用
	// order holds the types in registration order for RegisteredTypes and the available list
	order []string
)

// Register 注册一个渠道工厂, 重复 type 直接 panic
// 渠道在 init() 中调用它, 重复注册说明装配重复, 属于启动期配置错误而非运行期可恢复错误
//
// Register registers a channel factory and panics on a duplicate type
// A channel calls it from init(), and a duplicate registration means the assembly is repeated,
// which is a startup configuration error rather than a recoverable runtime one
func Register(f Factory) {
	if f == nil {
		panic("channel: Register called with a nil factory")
	}
	typ := f.Type()
	if typ == "" {
		panic("channel: Register called with an empty type")
	}

	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[typ]; dup {
		panic(fmt.Sprintf("channel: duplicate factory registration for type %q", typ))
	}
	registry[typ] = f
	order = append(order, typ)
}

// RegisteredTypes 返回按注册顺序排列的 type 副本
// 返回副本让调用方无法改写注册表的内部顺序
//
// RegisteredTypes returns a copy of the types in registration order
// The copy keeps a caller from rewriting the internal order of the registry
func RegisteredTypes() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, len(order))
	copy(out, order)
	return out
}

// IsRegistered 判断 type 是否已注册
//
// IsRegistered reports whether a type is registered
func IsRegistered(typ string) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, ok := registry[typ]
	return ok
}

// FactoryByType 返回 type 对应的工厂, 供 types 命令读取字段元数据
// 与 Create 不同, 它只读注册表, 不创建渠道实例, 也不触发工厂的静态校验
// 未注册时返回 nil 与 false
//
// FactoryByType returns the factory registered for a type, which the types command reads field
// metadata from
// Unlike Create, it only reads the registry: it creates no channel instance and runs no static
// validation inside a factory
// An unregistered type returns nil and false
func FactoryByType(typ string) (Factory, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	f, ok := registry[typ]
	return f, ok
}

// Create 用已注册的工厂按 type 创建渠道
// 未知 type 返回 *apperr.Problem, Data 带 Type 与逗号分隔的 Available
// 工厂自身的错误原样返回, 由工厂决定是否包成 Permanent
//
// Create builds a channel through the factory registered for a type
// An unknown type returns an *apperr.Problem whose Data carries Type and a comma-separated
// Available list
// An error from the factory itself comes back as it was, leaving the factory to decide
// whether to wrap it as Permanent
func Create(id, typ string, settings json.RawMessage) (Channel, error) {
	registryMu.RLock()
	f, ok := registry[typ]
	registryMu.RUnlock()
	if !ok {
		return nil, &apperr.Problem{
			ID: "channel.error.unknown_type",
			Data: map[string]any{
				"Type":      typ,
				"Available": strings.Join(RegisteredTypes(), ", "),
			},
		}
	}
	return f.Create(id, settings)
}
