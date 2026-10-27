# serverchan

[**English**](serverchan.md) | [**简体中文**](serverchan_zh-CN.md)

Pushes to ServerChan (Server酱), where the prefix of the SendKey decides between SC3 and Turbo and the program picks the route on its own

## Minimal configuration

```json
{
  "id": "sc",
  "type": "serverchan",
  "settings": { "token": "sctp123456texample" }
}
```

## Fields

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `token` | string | yes | SendKey, one starting with `sctp` takes SC3 and any other takes Turbo |
| `tags` | list\<string\> | no | SC3 tags; either an array or a comma-separated string, joined with a pipe |
| `short` | string | no | Short card description, supporting templates |
| `channels` | list\<string\> | no | SC3 message channels; either an array or a comma-separated string, joined with a pipe |
| `openids` | list\<string\> | no | SC3 openids receiving a copy; either an array or a comma-separated string |

## Choosing the endpoint

A key shaped like `sctp<uid>t...` takes **SC3**, with the endpoint `https://<uid>.push.ft07.com/send/<token>.send`

Any other key takes **Turbo**, with the endpoint `https://sctapi.ftqq.com/<token>.send`

You do not choose between the two endpoints, the program dispatches by the key prefix

## Title and body

`--title` and `--body` **enter the request automatically**, with no variable to reference in `settings`

The title goes into the `title` field and the body into the `desp` field, so the two travel separately

**The title is required**, a title that is an empty string reports an error, and a body may be omitted while still sending the title

**A Turbo title is truncated to 32 runes** with a warning; an SC3 title follows the platform rule and is not truncated

## How to call it

### Inline form

```bash
taskpush send --type serverchan \
  --set token=sctp123456texample \
  --title "Build done" --body "commit abc123"
```

```bash
taskpush send --type serverchan \
  --set token='${TokenInEnv}' \
  --title "Build done" --body "commit abc123"
```

### Configuration file form

Add one entry to `channels` in `taskpush.json`, with the fields inside `settings`:

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
taskpush send --title "Build done" --body "commit abc123"
```

The configuration references secrets with `${VAR}`, injected by the runtime, so the file itself is safe to commit; an unset variable without a default fails during loading, and `${TokenInEnv:-default}` allows a missing value

For `${VAR}` to work the file must carry the literal `${TokenInEnv}`, untouched by editor variable substitution or CI template interpolation

## Notes

- The token goes into the URL and the request body only, so it never shows up in the preview; in error data it appears in masked form only
- `tags`, `channels` and `openids` take effect on SC3 alone
