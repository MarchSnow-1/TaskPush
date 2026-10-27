# dingtalk

[**English**](dingtalk.md) | [**简体中文**](dingtalk_zh-CN.md)

推送到钉钉群机器人, 支持加签与 text/markdown 两种消息类型

## 最小配置

```json
{
  "id": "dd",
  "type": "dingtalk",
  "settings": { "url": "https://oapi.dingtalk.com/robot/send?access_token=exampleToken" }
}
```

## 字段

| 字段 | 类型 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- | --- |
| `url` | string | 是 | — | 完整机器人地址, 必须带非空 `access_token`, 支持模板 |
| `secret` | string | 否 | — | 加签密钥, `SEC` 开头; 留空则不加签 |
| `msgType` | string | 否 | `text` | `text` 或 `markdown`, 比较时忽略大小写 |
| `atAll` | bool | 否 | `false` | @所有人; 只对 text 生效, markdown 会拒绝 |

## 标题与正文

`--title` 与 `--body` **自动进入消息**, 不需要在 `settings` 里引用任何变量

`text` 类型: 标题与正文拼成一段 `content`, 中间空行分隔, 只有一方非空时用非空那个

`markdown` 类型: 标题进 `title` 字段, 正文进 `text` 字段; 标题为空时自动退回默认文本, 正文为空报错

两种类型都按 **4000 字节截断**, 截断时告警

## 加签

`secret` 以 `SEC` 开头时, 程序按钉钉加签规则在 URL 上追加 `timestamp` 与 `sign`, 时间取自发送时系统时钟

预览与错误不带 `access_token`、`timestamp`、`sign`; 服务端把 `access_token` 回显在响应体里时也会被打码

## 调用方法

### 内联写法

```bash
taskpush send --type dingtalk \
  --set url='https://oapi.dingtalk.com/robot/send?access_token=exampleToken' \
  --set secret='exampleSecret' \
  --title "构建完成" --body "commit abc123"
```

```bash
taskpush send --type dingtalk \
  --set url='https://oapi.dingtalk.com/robot/send?access_token=${AccessTokenInEnv}' \
  --set secret='${SecretInEnv}' \
  --title "构建完成" --body "commit abc123"
```

`access_token` 与 `secret` 是两个不同的凭证, 各用各的变量名, 不加签时省略 `--set secret`

### 配置文件写法

在 `taskpush.json` 的 `channels` 里加一条, 字段写进 `settings`:

```json
{
  "version": 1,
  "channels": [
    {
      "id": "dd",
      "type": "dingtalk",
      "settings": {
        "url": "https://oapi.dingtalk.com/robot/send?access_token=${AccessTokenInEnv}",
        "secret": "${SecretInEnv}"
      }
    }
  ]
}
```

```bash
export AccessTokenInEnv=exampleToken
export SecretInEnv=exampleSecret
taskpush send --title "构建完成" --body "commit abc123"
```

不加签的机器人省略 `secret` 字段即可, 只导出 `AccessTokenInEnv`

配置里用 `${VAR}` 引用密钥, 由运行环境注入, 文件本身可以安全入库, 变量未设置且没有默认值时加载即报错, 写成 `${AccessTokenInEnv:-默认值}` 可允许缺省

注意 `${VAR}` 要生效, 文件里必须写字面量 `${AccessTokenInEnv}`, 不能提前被编辑器的变量替换或 CI 的模板插值改掉

## 注意

- `access_token` 必须存在于 `url` 里, 缺失报错
- `msgType` 写成大写 `TEXT` 也能识别, 内部转小写比较
- `atAll` 与 markdown 组合会报错, 而不是静默忽略
