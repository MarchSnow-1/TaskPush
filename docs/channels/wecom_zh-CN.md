# wecom

[**English**](wecom.md) | [**简体中文**](wecom_zh-CN.md)

推送到企业微信群机器人

## 最小配置

```json
{
  "id": "wecom",
  "type": "wecom",
  "settings": { "url": "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=exampleToken" }
}
```

## 字段

| 字段 | 类型 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- | --- |
| `url` | string | 是 | — | 群机器人完整地址, 必须带 `key` 查询参数 |
| `msgType` | string | 否 | `text` | `text` 或 `markdown` |

## 标题与正文

`--title` 与 `--body` **自动进入消息**, 不需要在 `settings` 里引用任何变量

`text` 类型: 标题与正文拼成一段 `content`, 中间空行分隔, 只有一方非空时用非空那个

`markdown` 类型: 内容同样拼接后进 `markdown` 字段

按 **UTF-8 字节**截断, 截断时告警:

| msgType | 上限 |
| --- | --- |
| `text` | 2048 字节 |
| `markdown` | 4096 字节 |

两种类型都要求拼接后内容非空, 两者全空报错

## 调用方法

### 内联写法

```bash
taskpush send --type wecom \
  --set url='https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=exampleToken' \
  --title "构建完成" --body "commit abc123"
```

```bash
taskpush send --type wecom \
  --set url='https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=${TokenInEnv}' \
  --title "构建完成" --body "commit abc123"
```

### 配置文件写法

在 `taskpush.json` 的 `channels` 里加一条, 字段写进 `settings`:

```json
{
  "version": 1,
  "channels": [
    {
      "id": "wecom",
      "type": "wecom",
      "settings": { "url": "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=${TokenInEnv}" }
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

- `url` 里的 `key` 只用于发送, 预览与错误数据都不带它
- 截断按 UTF-8 字节计, 中文字符一个占 3 字节, 实际能放的字数比上限数字少
