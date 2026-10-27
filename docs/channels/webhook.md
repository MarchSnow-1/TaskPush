# webhook

[**English**](webhook.md) | [**简体中文**](webhook_zh-CN.md)

Carries a message through a configurable HTTP request, presetting no platform semantics

## Title and body

`--title` and `--body` **do not enter the request automatically** in this channel; they exist only as the template variables `{{title}}` and `{{body}}` for `settings` to reference

That means a configuration file that omits `body` sends an empty body with neither the title nor the body in it; putting them into the request means referencing them yourself:

```json
{
  "id": "ops",
  "type": "webhook",
  "settings": {
    "url": "https://example.com/hook",
    "body": { "format": "text", "text": "{{title}}\n\n{{body}}" }
  }
}
```

The exception is the inline `--webhook URL`: with no body-related argument at all, that entry fills in the default body `{{title}}\n\n{{body}}`, so the title and the body do arrive

## Minimal configuration

```json
{
  "id": "ops",
  "type": "webhook",
  "settings": { "url": "https://example.com/hook" }
}
```

The request this configuration sends carries no body, because a body has to be defined explicitly; see "Body shapes" below

## Fields

| Field | Type | Required | Default | Meaning |
| --- | --- | --- | --- | --- |
| `url` | string | yes | — | Target address, supporting templates |
| `method` | string | no | `POST` | HTTP method, uppercase |
| `headers` | object | no | — | Request headers, both names and values supporting templates |
| `body` | object or string | no | — | Request body in either shape below |
| `body.format` | string | no | — | `json` / `text` / `form` |
| `body.json` | object | no | — | JSON body whose string leaves accept templates |
| `body.text` | string | no | — | Plain-text body template |
| `body.fields` | object | no | — | Form fields as name and value pairs |
| `body.contentType` | string | no | — | Explicit Content-Type overriding the format default |
| `maskSecrets` | stringlist | no | — | Address fragments to hide from the preview, each one a substring of the full URL |

## Body shapes

`body` has two forms with different rules

The **string form** goes out as `text/plain`, and the string is the content:

```json
{ "body": "Build done\n\ncommit abc123" }
```

The **object form** is dispatched by `body.format`, one of three, and the format and its matching field must both be given; leaving one out reports an error rather than falling back to another shape

```json
{ "body": { "format": "json", "json": { "title": "{{title}}", "text": "{{body}}" } } }
```

| format | Field it requires |
| --- | --- |
| `text` | `body.text` |
| `json` | `body.json` |
| `form` | `body.fields` |

Leaving `body` out entirely sends an empty body

## JSON type fidelity

String leaves inside `body.json` accept template variables and environment variables, and when a leaf holds exactly one `{{variable}}` that variable keeps its JSON type:

| Configuration | Variable | Sent |
| --- | --- | --- |
| `"n": "value={{n}}"` | `n=5` | `"value=5"` |
| `"n": "{{n}}"` | `n=5` | `5` |
| `"f": "{{flag}}"` | `flag=true` | `true` |
| `"x": "{{nothing}}"` | `null` | `null` |

Once text is mixed in, the leaf goes out as a string

## How to call it

### Inline form

```bash
taskpush send --type webhook \
  --set url=https://example.com/hook \
  --set headers.X-Token=exampleToken \
  --set body.format=json \
  --set body.json.msg='{{body}}' \
  --title "Build done" --body "abc123"
```

```bash
taskpush send --type webhook \
  --set url='${URLInEnv}' \
  --set headers.X-Token='${TokenInEnv}' \
  --set body.format=json \
  --set body.json.msg='{{body}}' \
  --title "Build done" --body "abc123"
```

`--set` writes into `settings` through a dot path, so `body.json.msg` lands on the `msg` key inside the `json` object inside `body`

`--payload '{"msg":"x"}'` is short for `--set body.format=json` plus the whole `body.json`

### Configuration file form

Add one entry to `channels` in `taskpush.json`, with the fields inside `settings`:

```json
{
  "version": 1,
  "channels": [
    {
      "id": "ops",
      "type": "webhook",
      "settings": { "url": "${URLInEnv}", "headers": { "X-Token": "${TokenInEnv}" } }
    }
  ]
}
```

```bash
export URLInEnv=https://example.com/hook
export TokenInEnv=exampleToken
taskpush send --title "Build done" --body "commit abc123"
```

The address and the header value are two independent variables with a name of their own each

The configuration references secrets with `${VAR}`, injected by the runtime, so the file itself is safe to commit; an unset variable without a default fails during loading, and `${URLInEnv:-default}` allows a missing value

For `${VAR}` to work the file must carry the literal `${URLInEnv}`, untouched by editor variable substitution or CI template interpolation

## maskSecrets

The preview masks credential-looking segments of an address, but it cannot know where a custom address keeps its secret, so `maskSecrets` names the fragments to hide:

```json
{
  "settings": {
    "url": "https://example.com/hook/abcdef1234567890",
    "maskSecrets": ["abcdef1234567890"]
  }
}
```

Each entry must be a substring of the full URL, otherwise it warns, and when only some entries match the unmatched content still follows the usual credential masking

## Notes

- This channel defines no business meaning beyond success: 2xx is success and anything else is classified by status code
- The difference from the `--webhook` shortcut: `--webhook URL` without a body sends `title + blank line + body` by default, while a webhook in the configuration file sends no body when `body` is left out
