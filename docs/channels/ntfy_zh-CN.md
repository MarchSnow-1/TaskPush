# ntfy

[**English**](ntfy.md) | [**简体中文**](ntfy_zh-CN.md)

把消息以纯文本 POST 到 ntfy 服务端 (自建或官方) 的 topic 地址

## 最小配置

```json
{
  "id": "ntfy",
  "type": "ntfy",
  "settings": { "url": "https://ntfy.example.com/mytopic" }
}
```

## 字段

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `url` | string | 是 | 完整 topic 地址, 最后一段路径即 topic |
| `token` | string | 否 | Bearer token, 走 `Authorization` 头; 渲染后为空时不发送 |
| `priority` | int 或 string | 否 | 消息优先级, `1` 到 `5` 或 `min` / `low` / `default` / `high` / `max` |
| `tags` | list\<string\> | 否 | 标签, 走 `Tags` 头; 数组或逗号分隔字符串均可 |

## 标题与正文

`--title` 与 `--body` **自动进入请求**, 不需要在 `settings` 里引用任何变量

**正文走请求体, 标题走 `Title` 头**, 两者分开传递

正文为空时自动退回用标题当正文, 两者都空报错

正文按 **UTF-8 字节截断到 4096**, 截断时告警

## 优先级

`priority` 接受数字或枚举名, 数字限 1 到 5:

| 数字 | 枚举 |
| --- | --- |
| 1 | `min` |
| 2 | `low` |
| 3 | `default` |
| 4 | `high` |
| 5 | `max` |

枚举名比较时忽略大小写, 只在配置了 priority 时才发送 `Priority` 头

## 调用方法

### 内联写法

```bash
taskpush send --type ntfy \
  --set url='https://ntfy.example.com/mytopic' \
  --set priority=high \
  --title "构建完成" --body "commit abc123"
```

```bash
taskpush send --type ntfy \
  --set url='https://ntfy.example.com/mytopic' \
  --set token='${TokenInEnv}' \
  --set priority=high \
  --title "构建完成" --body "commit abc123"
```

### 配置文件写法

在 `taskpush.json` 的 `channels` 里加一条, 字段写进 `settings`:

```json
{
  "version": 1,
  "channels": [
    {
      "id": "ntfy",
      "type": "ntfy",
      "settings": { "url": "https://ntfy.example.com/mytopic", "token": "${TokenInEnv}" }
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

## 注意

- 完整地址与 token 都只用于发送; 预览与错误里的地址先经打码, token 一律不进错误数据
- header 值里的控制字符会被校验并拒绝, 模板渲染出换行的 tag 会在这一步暴露
