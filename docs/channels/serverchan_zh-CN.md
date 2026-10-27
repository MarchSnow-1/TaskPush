# serverchan

[**English**](serverchan.md) | [**简体中文**](serverchan_zh-CN.md)

推送到 ServerChan (Server酱), SendKey 的前缀决定走 SC3 还是 Turbo, 程序自动判定

## 最小配置

```json
{
  "id": "sc",
  "type": "serverchan",
  "settings": { "token": "sctp123456texample" }
}
```

## 字段

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `token` | string | 是 | SendKey, `sctp` 开头走 SC3, 其余走 Turbo |
| `tags` | list\<string\> | 否 | SC3 标签; 数组或逗号分隔字符串均可, 以管道符拼接 |
| `short` | string | 否 | 短卡片描述, 支持模板 |
| `channels` | list\<string\> | 否 | SC3 消息通道; 数组或逗号分隔字符串均可, 以管道符拼接 |
| `openids` | list\<string\> | 否 | SC3 接收消息副本的 openid; 数组或逗号分隔字符串均可 |

## 端点选择

key 形如 `sctp<uid>t...` 时走 **SC3**, 端点为 `https://<uid>.push.ft07.com/send/<token>.send`

其他任何 key 走 **Turbo**, 端点为 `https://sctapi.ftqq.com/<token>.send`

两个端点不用你选, 程序按 key 前缀自动分派

## 标题与正文

`--title` 与 `--body` **自动进入请求**, 不需要在 `settings` 里引用任何变量

标题进 `title` 字段, 正文进 `desp` 字段, 两者分开传递

**标题必填**, 标题为空字符串时报错, 正文可以只写标题不发正文

**Turbo 标题按 32 个 rune 截断**并发告警; SC3 标题按平台规则不截断

## 调用方法

### 内联写法

```bash
taskpush send --type serverchan \
  --set token=sctp123456texample \
  --title "构建完成" --body "commit abc123"
```

```bash
taskpush send --type serverchan \
  --set token='${TokenInEnv}' \
  --title "构建完成" --body "commit abc123"
```

### 配置文件写法

在 `taskpush.json` 的 `channels` 里加一条, 字段写进 `settings`:

```json
{
  "version": 1,
  "channels": [
    {
      "id": "sc",
      "type": "serverchan",
      "settings": { "token": "${TokenInEnv}" }
    }
  ]
}
```

```bash
export TokenInEnv=sctp123456texample
taskpush send --title "构建完成" --body "commit abc123"
```

配置里用 `${VAR}` 引用密钥, 由运行环境注入, 文件本身可以安全入库, 变量未设置且没有默认值时加载即报错, 写成 `${TokenInEnv:-默认值}` 可允许缺省

注意 `${VAR}` 要生效, 文件里必须写字面量 `${TokenInEnv}`, 不能提前被编辑器的变量替换或 CI 的模板插值改掉

## 注意

- token 只进入 URL 与请求体, 不出现在预览里; 错误数据里也只以掩码形式出现
- `tags` / `channels` / `openids` 三个字段只对 SC3 生效
