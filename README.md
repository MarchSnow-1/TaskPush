<div align="center">
  <h1 align="center">
    <img src="resources/icons/appIcon.svg" width="128" alt="icon"/>
    <br/>
    TaskPush
  </h1>

  <p>
    A lightweight multi-channel push CLI tool written in <a href="https://go.dev">Go</a>
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

## Introduction

TaskPush is a multi-channel push CLI written in pure Go: one command delivers a notification to several channels, built for CI, scripts and scheduled jobs

## Install

Grab the archive for your platform from [Releases](https://github.com/MarchSnow-1/TaskPush/releases) and unpack it

Build from source:

```bash
git clone https://github.com/MarchSnow-1/TaskPush.git
cd TaskPush/src
go build ./cmd/taskpush
```

## Documentation

- [Usage guide](docs/usage.md): from the first notification to the advanced options
- [Channel index](docs/types.md): one configuration guide per channel
- [Example configuration](docs/taskpush.example.json): a filled-in sample per channel
- [JSON Schema](docs/taskpush.schema.json): editor completion and validation

## License

[Apache-2.0](LICENSE)
