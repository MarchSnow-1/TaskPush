# bark

[**English**](bark.md) | [**简体中文**](bark_zh-CN.md)

推送到 iOS 上的 Bark App, 支持单设备与多设备批量

## 最小配置

```json
{
  "id": "phone",
  "type": "bark",
  "settings": { "token": "exampleToken" }
}
```

## 字段

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `token` | string | 二选一 | 单设备 key, 与 `tokens` 互斥; 群组 key 可含斜杠 |
| `tokens` | list\<string\> | 二选一 | 多设备 key, 走批量推送, 与 `token` 互斥 |
| `server` | string | 否 | 服务端地址, 默认 `https://api.day.app` |
| `subtitle` | string | 否 | 副标题 |
| `group` | string | 否 | 通知分组名 |
| `level` | string | 否 | 中断级别, 如 `active` / `timeSensitive` / `passive` / `critical` |
| `sound` | string | 否 | 通知铃声名 |
| `icon` | string | 否 | 通知图标 URL |
| `image` | string | 否 | 通知图片 URL |
| `clickUrl` | string | 否 | 点击通知后打开的地址 |
| `copy` | string | 否 | 长按通知时复制的文本 |
| `badge` | int | 否 | 应用角标数字, 非负整数 |
| `ttl` | int | 否 | 消息存活秒数, 非负整数 |
| `autoCopy` | bool | 否 | 是否自动复制内容 |

## 请求形态

单设备走 `{server}/{token}`, 多设备走 `{server}/push`, 请求体都是 JSON, 标题与正文分别进 `title` 与 `body` 字段, 不拼接

## 标题与正文

`--title` 与 `--body` **自动进入请求**, 不需要在 `settings` 里引用任何变量

两者都**不做截断**, 长度只受 TaskPush 的 64KB 渲染上限约束, 这与其他渠道不同

两者渲染后都为空白时报错, 至少给一个

## 调用方法

### 内联写法

```bash
taskpush send --type bark --set token=exampleToken --title "构建完成" --body "commit abc123"
```

```bash
taskpush send --type bark --set token='${TokenInEnv}' --title "构建完成" --body "commit abc123"
```

### 配置文件写法

在 `taskpush.json` 的 `channels` 里加一条, 字段写进 `settings`:

```json
{
  "version": 1,
  "channels": [
    {
      "id": "phone",
      "type": "bark",
      "settings": { "token": "${TokenInEnv}" }
    }
  ]
}
```

```bash
export TokenInEnv=exampleToken
taskpush send --title "构建完成" --body "commit abc123"
```

配置里用 `${VAR}` 引用密钥, 由运行环境注入, 文件本身可以安全入库, 变量未设置且没有默认值时加载即报错, 写成 `${TokenInEnv:-默认值}` 可允许缺省

注意 `${VAR}` 要生效, 文件里必须写字面量 `${TokenInEnv}`, 不能提前被编辑器的变量替换或 CI 的模板插值改掉

## 批量推送与重试

多设备推送时逐设备校验响应里 `data[].code`, 已成功的设备记进本次发送状态, 重试只补失败的那几台

响应里没有 `device_key` 时无法收窄范围, 下一轮按配置全量重发, 并发出一条告警

## 注意

- `token` 与 `tokens` 必须给且只给一个, 两个都给或都不给都报错
- `server` 可指向自建 Bark 服务端
