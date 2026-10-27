<div align="center">
  <h1 align="center">
    <img src="resources/icons/appIcon.svg" width="128" alt="icon"/>
    <br/>
    TaskPush
  </h1>

  <p>
    使用 <a href="https://go.dev">Go</a> 编写的轻量级多渠道消息推送 CLI
  </p>

<!-- Badges -->

![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20macOS%20%7C%20Linux-blue?style=for-the-badge)
![Go](https://img.shields.io/badge/Go-1.27.3-00ADD8?style=for-the-badge&logo=go&logoColor=white)
![GitHub Release](https://img.shields.io/github/v/release/MarchSnow-1/TaskPush?style=for-the-badge)
![GitHub Repo stars](https://img.shields.io/github/stars/MarchSnow-1/TaskPush?style=for-the-badge)
![GitHub Last Commit](https://img.shields.io/github/last-commit/MarchSnow-1/TaskPush?style=for-the-badge)
[![Total Download](https://img.shields.io/github/downloads/MarchSnow-1/TaskPush/total?style=for-the-badge)](https://github.com/MarchSnow-1/TaskPush/releases)

[**English**](README.md) | [**简体中文**](README_zh-CN.md)

</div>

## 简介

TaskPush 是一个纯 Go 编写的多渠道消息推送 CLI，使用一条命令即可把通知推到多个渠道，适合 CI、脚本和定时任务

## 安装

从 [Releases](https://github.com/MarchSnow-1/TaskPush/releases) 下载对应平台的压缩包，解压即用

从源码构建：

```bash
git clone https://github.com/MarchSnow-1/TaskPush.git
cd TaskPush/src
go build ./cmd/taskpush
```

## 文档

- [完整使用指南](docs/usage_zh-CN.md): 从第一条通知到高级用法
- [渠道类型索引](docs/types_zh-CN.md): 各个渠道的配置指南
- [示例配置](docs/taskpush.example.json): 各渠道的示例配置
- [JSON Schema](docs/taskpush.schema.json): 编辑器补全与校验

## 许可证

[Apache-2.0](LICENSE)