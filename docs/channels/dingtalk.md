# dingtalk

[**English**](dingtalk.md) | [**简体中文**](dingtalk_zh-CN.md)

Pushes to a DingTalk group robot, with optional signing and the text/markdown message types

## Minimal configuration

```json
{
  "id": "dd",
  "type": "dingtalk",
  "settings": { "url": "https://oapi.dingtalk.com/robot/send?access_token=exampleToken" }
}
```

## Fields

| Field | Type | Required | Default | Meaning |
| --- | --- | --- | --- | --- |
| `url` | string | yes | — | Full robot address, which has to carry a non-empty `access_token`, supporting templates |
| `secret` | string | no | — | Signing secret starting with `SEC`; an empty value means no signing |
| `msgType` | string | no | `text` | `text` or `markdown`, compared without regard to case |
| `atAll` | bool | no | `false` | Mention everyone; it applies to text alone and markdown refuses it |

## Title and body

`--title` and `--body` **enter the message automatically**, with no variable to reference in `settings`

The `text` type joins the title and the body into one `content` with a blank line between, using whichever side is non-empty when only one is

The `markdown` type puts the title into the `title` field and the body into the `text` field; an empty title falls back to the default text and an empty body reports an error

Both types are **truncated to 4000 bytes**, with a warning when cut

## Signing

When `secret` starts with `SEC`, the program appends `timestamp` and `sign` to the URL following the DingTalk signing rule, taking the time from the system clock at send time

The preview and the errors carry no `access_token`, `timestamp` or `sign`; a server that echoes `access_token` in the response body is masked as well

## How to call it

### Inline form

```bash
taskpush send --type dingtalk \
  --set url='https://oapi.dingtalk.com/robot/send?access_token=exampleToken' \
  --set secret='exampleSecret' \
  --title "Build done" --body "commit abc123"
```

```bash
taskpush send --type dingtalk \
  --set url='https://oapi.dingtalk.com/robot/send?access_token=${AccessTokenInEnv}' \
  --set secret='${SecretInEnv}' \
  --title "Build done" --body "commit abc123"
```

`access_token` and `secret` are two different credentials with a variable of their own each, and an unsigned robot omits `--set secret`

### Configuration file form

Add one entry to `channels` in `taskpush.json`, with the fields inside `settings`:

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
taskpush send --title "Build done" --body "commit abc123"
```

An unsigned robot omits the `secret` field and exports `AccessTokenInEnv` alone

The configuration references secrets with `${VAR}`, injected by the runtime, so the file itself is safe to commit; an unset variable without a default fails during loading, and `${AccessTokenInEnv:-default}` allows a missing value

For `${VAR}` to work the file must carry the literal `${AccessTokenInEnv}`, untouched by editor variable substitution or CI template interpolation

## Notes

- `access_token` has to sit inside `url`, and a missing one reports an error
- `msgType` also recognises an uppercase `TEXT`, compared in lowercase internally
- Combining `atAll` with markdown reports an error rather than ignoring it silently
