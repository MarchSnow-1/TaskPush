// Package all 以空白导入装配九个内置渠道, 让它们的 init() 把工厂注册进 channel 注册表
// 只由 cmd/taskpush 导入; 单独导入本包不会产生其它副作用
//
// Package all assembles the nine built-in channels through blank imports, so their init
// functions register the factories in the channel registry
// Only cmd/taskpush imports it, and importing the package on its own has no other side effect
package all

import (
	_ "github.com/MarchSnow-1/TaskPush/internal/channel/bark"
	_ "github.com/MarchSnow-1/TaskPush/internal/channel/dingtalk"
	_ "github.com/MarchSnow-1/TaskPush/internal/channel/discord"
	_ "github.com/MarchSnow-1/TaskPush/internal/channel/gotify"
	_ "github.com/MarchSnow-1/TaskPush/internal/channel/ntfy"
	_ "github.com/MarchSnow-1/TaskPush/internal/channel/serverchan"
	_ "github.com/MarchSnow-1/TaskPush/internal/channel/telegram"
	_ "github.com/MarchSnow-1/TaskPush/internal/channel/webhook"
	_ "github.com/MarchSnow-1/TaskPush/internal/channel/wecom"
)
