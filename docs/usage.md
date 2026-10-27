# TaskPush Usage Guide

[**English**](usage.md) | [**简体中文**](usage_zh-CN.md)

Understand TaskPush step by step, from the first message to the advanced options

## 1. Getting started

### 1.1 Send the first notification

Without writing any file, hand the address straight to `--webhook`:

```bash
taskpush send --webhook https://example.com/hook --title "Build done" --body "commit abc123"
```

On success it prints one `[OK]` line and exits 0:

```text
[OK] inline (webhook): sent (attempts: 1)
```

Using `--webhook` builds an inline channel. For this flag alone, and only when no `body` is given, it sends plain text with the default `POST`: the title, a blank line, then the body

Using the generic `--type` route to build a channel behaves the same as creating one in the configuration file: with no body given the body stays empty, and the `title + blank line + body` default is not applied the way the `--webhook` shortcut applies it

### 1.2 Simulate a send to test the configuration

Add `--dry-run` to walk the whole send path, but only render and validate: no real request goes out

```bash
taskpush send --webhook https://example.com/hook --title "Build done" --body "commit abc123" --dry-run
```

```text
[OK] inline (webhook): POST https://example.com/hook (text, 14 bytes)
[INFO] dry run, nothing was sent
```

Addresses and credentials in the preview are masked, and the byte count reflects the actual body length

## 2. Configuration file

A single notification needs no configuration, but several channels, the shared settings or per-channel fields all call for a file

The `init` command writes a minimal configuration into the current directory:

```bash
taskpush init
```

```json
{
  "version": 1,
  "timeout": "15s",
  "retries": 0,
  "retryDelay": "1s",
  "proxy": "",
  "channels": []
}
```

An existing file is left alone (it reports `already exists`); add `--force` to overwrite it

With an empty `channels` there is nothing for `send` to deliver to, so put at least one channel in it:

```json
{
  "version": 1,
  "timeout": "15s",
  "retries": 0,
  "retryDelay": "1s",
  "proxy": "",
  "channels": [
    { "id": "ops", "type": "webhook", "settings": { "url": "https://example.com/hook" } }
  ]
}
```

The file is named `taskpush.json`. A copy in the working directory is read automatically, and an explicit path also works:

```bash
taskpush send --config path/to/taskpush.json --title "Build done"
```

The path accepts both relative and absolute forms, and a relative path resolves against the current working directory

The program looks for configuration in this order and stops at the first hit:

1. The file named by `--config PATH`, or `--config -` to read standard input
2. The content of the `TASKPUSH_CONFIG_JSON` environment variable
3. `taskpush.json` in the current directory

When both `--config` and `TASKPUSH_CONFIG_JSON` are present, `--config` wins without an error; all three sources cap their content at 1MB

The standard library parses the file, so comments and trailing commas are not accepted

A bad field produces an explicit error naming its path; after editing, run `taskpush check` once

## 3. Validating the configuration

Once the file is written, validating it is worth the one command

The `check` command parses the configuration, validates the fields and renders the templates:

```bash
taskpush check
```

```text
ID   TYPE     STATUS  DETAIL
ops  webhook  [OK]    POST https://example.com/hook (none, 0 bytes)
phone bark    [OK]    bark: 1 device
```

An explicit path works the same way:

```bash
taskpush check --config path/to/taskpush.json
```

Any error exits 1

Warnings alone exit 0

The difference from `send` with `--dry-run` is that `check` only validates the configuration

By default a channel marked as disabled in the configuration is not validated

Passing `--all` validates every channel the file defines, disabled ones included

## 4. Inspecting channel fields

The `types` command lists every channel type

```bash
taskpush types
```

`types <TYPE>` shows the fields of one type:

```bash
taskpush types webhook
```

```text
FIELD             TYPE        REQUIRED  DEFAULT  DESCRIPTION
url               string      true               target URL, supporting templates
method            string      false     POST     HTTP method, uppercase, default POST
...
```

Every channel carries its own `settings` object holding its detailed fields:

```json
{ "id": "tg", "type": "telegram", "settings": { "token": "example:token", "chatIds": ["111111"] } }
```

For the required fields of every channel, run `taskpush types <TYPE>`; each channel also has its own guide under [types.md](types.md), and `docs/taskpush.example.json` carries one filled-in sample per channel

## 5. Variables

### 5.1 Template variables

Both the title and the body accept template variables, written as `{{name}}`

The built-in variables are `{{time}}`, `{{date}}` and `{{datetime}}`, plus the special `{{title}}` and `{{body}}`:

```bash
taskpush send --webhook https://example.com/hook --title "{{date}} build done" --body "finished at {{datetime}}"
```

Here the title receives `{{date}} build done`, which is rendered once and comes out roughly as `2026-01-01 build done`

The rendered title and body then become template variables of their own, and a configuration file can reference them as `{{title}}` and `{{body}}`

For example:

```json
{
  "version": 1,
  "channels": [
    {
      "id": "ops",
      "type": "webhook",
      "settings": {
        "url": "https://example.com/hook",
        "method": "POST",
        "headers": {
          "Content-Type": "application/json",
          "X-Title": "{{title}}"
        },
        "body": {
          "format": "json",
          "json": {
            "title": "{{title}}",
            "text": "{{body}}",
            "sent_at": "{{datetime}}"
          }
        }
      }
    }
  ]
}
```

### 5.2 Custom template variables

Pass your own variables with `--var`, which may be repeated:

```bash
taskpush send --webhook https://example.com/hook \
  --title "{{project}} build done" --body "branch {{branch}}" \
  --var project=TaskPush --var branch=main
```

When there are too many to pass on the command line, put them in a JSON file and load it with `--var-file`:

```json
{ "varfile_project": "TaskPush", "varfile_branch": "main", "varfile_commit": "abc123" }
```

```bash
taskpush send --webhook https://example.com/hook --title "{{varfile_project}} build done" --body "{{varfile_commit}}" --var-file vars.json
```

The precedence is `--var` on the command line, then the `--var-file` contents, then the built-in variables

Rewriting the values inside the var file before pushing is a normal way to drive the content dynamically

### 5.3 Environment variables

Beyond custom template variables, TaskPush also reads environment variables

A `${VAR}` anywhere in a configuration value refers to the environment variable named `VAR`:

First create a `taskpush.json`:

```json
{
  "version": 1,
  "channels": [
    { "id": "ops", "type": "webhook", "settings": { "url": "${HOOK_URL}" } }
  ]
}
```

> [!WARNING]
> A value passed on the command line needs single quotes so the shell does not expand the variable first

Then run:

```bash
export HOOK_URL=https://example.com/hook
export REPO=TaskPush
taskpush send --title '${REPO} build done'
```

Here the title receives `${REPO} build done`; the environment reference is expanded first, and the result is then rendered as a template, coming out as `TaskPush build done`

The expanded title and body again become template variables, so a configuration file can reference them as `{{title}}` and `{{body}}`

Following the example above, `TaskPush build done` goes to the address stored in `HOOK_URL`

An unset variable without a default fails during loading:

```text
[ERROR] environment variable HOOK_URL is not set (source file:..., path channels[1].settings.url)
```

To allow a missing value, write `${VAR:-https://example.com/hook}`

A backslash-escaped `$${` outputs a literal `${`

## 6. Sending to several channels at once

Several channels can share one configuration file, and `send` delivers to every enabled channel among them by default:

```json
{
  "version": 1,
  "channels": [
    { "id": "ops", "type": "webhook", "settings": { "url": "https://example.com/hook" } },
    { "id": "phone", "type": "bark", "settings": { "token": "exampleToken" } }
  ]
}
```

Note: a `webhook` channel defined in the configuration file sends a body only when `settings.body` defines one; without it the body is empty

```bash
taskpush send --title "Build done" --body "commit abc123"
```

Each channel prints one result line, in the order the file lists them, and all successes exit 0

Any failure exits 1 while the remaining channels still send as usual

To reach only one channel or a few of them, use `--channel`, which may be repeated to name several at once:

```bash
taskpush send --channel ops --channel phone --title "notification for ops and phone"
```

```bash
taskpush send --channel ops --title "notification for ops only"
```

## 7. The webhook request body

With `--webhook`, the webhook request body is plain text by default

With `--type` passing the settings by hand, or with a `webhook` channel written into the configuration file, the request body is empty by default

To change the method, add request headers or switch the body format, adjust `settings`:

```json
{
  "id": "ops",
  "type": "webhook",
  "settings": {
    "url": "https://example.com/hook",
    "method": "PUT",
    "headers": { "X-Token": "${HOOK_TOKEN}" },
    "body": {
      "format": "json",
      "json": { "title": "{{title}}", "text": "{{body}}", "at": "{{datetime}}" }
    }
  }
}
```

`body.format` takes one of `text`, `json` or `form` as the sending format

Inside `json`, string leaves accept template variables and environment variables, while numeric and boolean leaves keep their original JSON type

```
On "keep their original JSON type":

when a leaf holds exactly one {{variable}} and that variable is a boolean,
TaskPush keeps the JSON type, so a true value goes out as true rather than the string "true"
```

The inline flags reach the same result:

```bash
taskpush send --type webhook \
  --set url=https://example.com/hook \
  --set headers.X-Token=exampleToken \
  --set body.format=json \
  --set body.json.msg='{{body}}' \
  --title "Build done" --body "abc123"
```

`--set` writes into `settings` through a dot path, so `body.json.msg` lands on the `msg` key inside the `json` object inside `body`

If that feels like too much, `--payload` takes a JSON document to define the payload directly

For instance `--payload '{"msg":"x"}'` is short for `--set body.format=json` plus the whole `body.json`

## 8. Retry and delivery

### Retrying on failure

By default the program does not retry: one push sends once, and a failure reports the error and exits 1

To turn retrying on, set `retries` in the configuration:

```json
{
  "version": 1,
  "timeout": "15s",
  "retries": 2,
  "retryDelay": "1s",
  "channels": [ ... ]
}
```

Once enabled, the program resends at most `retries` times after the first failure, so the worst case is `retries + 1` attempts in total

`retryDelay` is the fixed gap between two attempts

`timeout`, `retries` and `retryDelay` can also be written on a single channel to override the top level

When a channel leaves them out the program looks up to the top level, and when the top level leaves them out it falls back to the defaults

The precedence is channel settings, then top level, then defaults

Retrying only applies to errors that may heal themselves:

| Retried | Stops immediately |
| --- | --- |
| Network errors (connection refused, timeout, DNS failure) | 3xx (redirects are not followed, the response is judged as it arrives) |
| 408 / 425 / 429 | The remaining 4xx (400, 401, 403, 404 and so on) |
| Ordinary 5xx (500, 502, 503 and so on) | 501 / 505 / 511 |
| An empty or malformed JSON response body | A business code that marks a deterministic failure |

For anything in the right column, even a large `retries` still sends only once and performs no further retry

### Delivery guarantees

A notification may reach the other side more than once, and the precondition is that automatic retry is enabled

Suppose the request did arrive but the response was lost

Because the response was lost, TaskPush cannot tell whether the request reached the receiver

With automatic retry configured, that retry becomes a second delivery

So a downstream that triggers an action has to guarantee idempotence itself; turning retry off is the other option, at the cost of reliability

## 9. Timing

The `start` command records the current timestamp (it overwrites a local file), and `finish` after the work computes the elapsed time and sends:

```bash
taskpush start
# ... the work being timed ...
taskpush finish --title "Task finished" --body "elapsed {{elapsed}}"
```

`finish` adds the `{{elapsed}}`, `{{elapsed_ms}}` and `{{elapsed_s}}` template variables

Timing works through a state file holding the timestamp, read back at the end to compute the elapsed time

A successful send (a zero exit, meaning no channel failed) deletes the temporary state file, while a channel failure keeps it

`--keep-state` keeps the state file even after a successful send

The state file is `.taskpush-state.json` next to the configuration file in use, or in the current working directory when the configuration source is not a file; `--state-file` overrides the path

## 10. Exit codes and output

| Exit code | Meaning |
| --- | --- |
| 0 | Success; `check` with warnings only also counts as success |
| 1 | A configuration, environment, channel, send or state error |
| 2 | A command-line usage error |
| 130 | Cancelled through Ctrl+C (SIGINT) or SIGTERM |

## 11. Language

```bash
taskpush --lang zh-CN list
```

`--lang` accepts `en` (the default) or `zh-CN`, and may sit before or after the command

The setting affects log output, help and similar text; it does not affect the notification content or the configuration semantics

## 12. Security

- Reference secrets with `${VAR}` in the configuration file, so the runtime injects them and a missing value reports an error instead of leaking into the file
- URLs and tokens in the preview and in errors are masked, and a log is still worth a look before it is shared
- The program does not follow HTTP redirects, so authentication headers do not leak through a redirect

## 13. CI examples

### 13.1 The shortest form

The shortest form uses environment variables only and writes no file:

```yaml
- run: taskpush send --webhook '${HOOK_URL}' --title "${{ github.ref_name }} build done" --body "$GITHUB_SHA"
  env:
    HOOK_URL: ${{ secrets.HOOK_URL }}
```

Here GitHub Actions reads `secrets.HOOK_URL` into the `HOOK_URL` environment variable, and TaskPush expands it

> [!WARNING]
> A value passed on the command line needs single quotes so the shell does not expand the variable first

### 13.2 Several channels

Several channels call for a configuration file in the working directory or at a given path, and secrets can keep coming from the environment:

```yaml
- run: taskpush send --title "Build done" --body "$GITHUB_SHA"
  env:
    HOOK_URL: ${{ secrets.HOOK_URL }}
    HOOK_TOKEN: ${{ secrets.HOOK_TOKEN }}
```

To keep the configuration out of the repository, put the JSON into the `TASKPUSH_CONFIG_JSON` environment variable and the program picks it up:

```yaml
- run: taskpush send --title "Build done" --body "$GITHUB_SHA"
  env:
    HOOK_URL: ${{ secrets.HOOK_URL }}
    HOOK_TOKEN: ${{ secrets.HOOK_TOKEN }}
    TASKPUSH_CONFIG_JSON: ${{ secrets.TASKPUSH_CONFIG_JSON }}
```

The configuration can also arrive through stdin:

```bash
echo "$CONFIG_JSON" | taskpush send --config - --title "Build done"
```

```yaml
- run: echo "$CONFIG_JSON" | taskpush send --config - --title "Build done" --body "$GITHUB_SHA"
  env:
    HOOK_URL: ${{ secrets.HOOK_URL }}
    HOOK_TOKEN: ${{ secrets.HOOK_TOKEN }}
    CONFIG_JSON: ${{ secrets.TASKPUSH_CONFIG_JSON }}
```

## 14. Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| Reports that no configuration source was found | No inline flag, and none of `--config`, `TASKPUSH_CONFIG_JSON` or `taskpush.json` in the current directory |
| `${VAR}` reports an unset environment variable | The variable does not hold that value; check the name, or write `${VAR:-default}` when a missing value is acceptable |
| `check --all` reports an unset variable inside a disabled channel | `--all` validates disabled channels too; supply the variable or drop `--all` |
| An unknown field error | The configuration decodes strictly; remove the extra key or rename it correctly |
| `headers` reports a type error | It takes an object mapping only, for example `{"X-Token":"${HOOK_TOKEN}"}` |
| A string `body` in webhook goes out as plain text | The string form is fixed to `text/plain`; for JSON write `{"format":"json","json":{...}}` |
| A template variable is not substituted | `send` fails on an unresolved variable; check the key names given to `--var` and `--var-file` |
| A literal `{{` is wanted in the template | Write `\{{`, for example `--body '\{{not_a_var}}'` |
| The same notification arrives twice | See [section 8](#8-retry-and-delivery) |
| A comment in the configuration breaks parsing | The standard library parses it, so comments and trailing commas are not accepted |
| A local plain-text service cannot be reached | A host without a scheme gets `https://` unless it is a loopback address; write `http://` explicitly when plain text is wanted |

## 15. Commands and flags in full

```text
taskpush [global flags] <command> [command flags] [positional args]
```

The first positional argument is the command; flags may precede or follow it; every token after `--` is treated as a positional argument

| Command | Purpose | Sends HTTP | Touches state |
| --- | --- | --- | --- |
| `send` | Send one notification | Yes (no with `--dry-run`) | No |
| `start` | Record the timing start | No | Writes |
| `finish` | Read the start, compute the elapsed time and send | Yes (no with `--dry-run`) | Reads; deletes on success (`--keep-state` keeps it) |
| `check` | Validate the configuration or an inline channel | No | No |
| `list` | List the channels in the configuration | No | No |
| `types` | List the types; `types <TYPE>` shows the fields | No | No |
| `init` | Write a minimal `taskpush.json` | No | No |
| `version` | Show the version | No | No |
| `help` | Help; `help CMD` for one command | No | No |

Global flags:

| flag | Value | Default | Meaning |
| --- | --- | --- | --- |
| `--help` / `-h` | None | — | Show help; after a command it shows that command's help |
| `--version` | None | — | Show the version |
| `--lang` | `en` / `zh-CN` | `en` | Output language |

Command and flag pairs:

| Command | Flags |
| --- | --- |
| `send` | `--config` `--channel` `--title` `--title-file` `--body` `--body-file` `--var` `--var-file` `--dry-run` `--parallel` `--webhook` `--type` `--set` `--payload` |
| `finish` | Everything `send` takes, plus `--state-file` `--keep-state` |
| `start` | `--config` `--state-file` `--dry-run` |
| `check` | `--config` `--channel` `--all` `--var` `--var-file` `--webhook` `--type` `--set` `--payload` |
| `list` | `--config` |
| `types` | A positional `TYPE` (optional) |
| `init` | `--force` |
| `version` | None |
| `help` | A positional `COMMAND` (optional) |

Flag meanings:

| flag | Meaning |
| --- | --- |
| `--config PATH` | Configuration path; without it, `TASKPUSH_CONFIG_JSON` then `taskpush.json` in the current directory |
| `--channel ID` | Repeatable; send to or validate only the named channels |
| `--title TEXT` | Title template; mutually exclusive with `--title-file` |
| `--title-file PATH` | Read the title from a file; `-` reads stdin; mutually exclusive with `--title` |
| `--body TEXT` | Body template; mutually exclusive with `--body-file` |
| `--body-file PATH` | Read the body from a file; `-` reads stdin; mutually exclusive with `--body` |
| `--var KEY=VALUE` | Repeatable; add one template variable; overrides the same key from `--var-file`; a reserved name reports an error |
| `--var-file PATH` | A JSON object file; `-` reads stdin |
| `--dry-run` | Render and validate fully, then print the preview |
| `--parallel N` | Concurrency across channels; default 1; range 1-8 |
| `--state-file PATH` | Override the state file path |
| `--keep-state` | Keep the state file after a successful `finish` |
| `--all` | `check` validates disabled channels too |
| `--webhook URL` | Build an inline webhook channel |
| `--type TYPE` | Build an inline channel of the given type; `--set` supplies the required fields |
| `--set KEY=VALUE` | Repeatable; write into the inline channel's `settings`; supports nested paths |
| `--payload JSON` | Inline webhook only; sets `body.format=json` and `body.json` |
| `--force` | `init` overwrites an existing `taskpush.json` |

Parsing rules:

- Repeating a single-value flag is an error; `--var`, `--channel` and `--set` may repeat
- A boolean flag accepts only `--dry-run` and `--dry-run=true/false/1/0`; `--dry-run false` does not work
- A value starting with `--` must be written `--flag=--value` so it is not read as a flag
- At most one stdin consumer per call: only one of `--config -`, `--title-file -`, `--body-file -` and `--var-file -`
- `--title` excludes `--title-file`, and `--body` excludes `--body-file`
- `types` and `help` take at most one positional argument; no other command takes any

## 16. Configuration fields

Top level:

| Field | Type | Default | Constraint |
| --- | --- | --- | --- |
| `version` | int | `1` | Only 1 is supported |
| `$schema` | string | empty | Ignored at runtime; allowed at the top level |
| `timeout` | duration | `15s` | Must be greater than 0 |
| `retries` | int | `0` | At least 0; the retries beyond the first attempt |
| `retryDelay` | duration | `1s` | At least 0; the fixed retry interval |
| `proxy` | string | empty | Empty means direct; `env` reads the environment proxy; an http/https URL is an explicit proxy; socks5 is not supported |
| `channels` | array | required | One object per channel |

One channel:

| Field | Type | Default | Constraint |
| --- | --- | --- | --- |
| `id` | string | required | Non-empty and unique; `--channel` refers to it |
| `type` | string | required | One of the types `types` lists |
| `enabled` | bool | `true` | Only an explicit `false` disables it |
| `timeout` | duration | inherited | Greater than 0 |
| `retries` | int | inherited | At least 0 |
| `retryDelay` | duration | inherited | At least 0 |
| `settings` | object | required for an enabled channel | The channel's own fields, decoded strictly by its factory |

The standard library parses the file and rejects comments and trailing commas; an unknown field reports an error carrying the JSON path, for example `channels[2].settings`

## 17. Template syntax

| Form | Meaning |
| --- | --- |
| `{{name}}` | A variable; dot paths such as `{{a.b.0}}` work |
| `{{name\|default:text}}` | Used when the variable is missing or falsy |
| `{{if x}}A{{else}}B{{end}}` | A condition; `x` is a variable name or a dot path |
| `{{range items}}...{{end}}` | Iterate an array; `{{.}}` inside is the current item and `{{@index}}` counts from 0 |
| `\{{` | Output a literal `{{` |

Strict and lenient modes:

| Mode | Commands | Unresolved variable |
| --- | --- | --- |
| Strict | `send` / `finish` / `--dry-run` | Reports an error and exits 1 |
| Lenient | `check` | Keeps the literal fragment and warns, exiting 0 when everything else is valid |

`check` renders with placeholder content, so the lenient mode applies to template fields inside `settings`; `--title` and `--body` are rendered only under `send` and `finish`, where the mode is always strict

Built-in variables: `{{title}}`, `{{body}}`, `{{time}}`, `{{date}}` and `{{datetime}}`; `finish` adds `{{elapsed}}`, `{{elapsed_ms}}` and `{{elapsed_s}}`. These are reserved names and `--var` cannot override them

Rendered content is bounded by a 64KB output cap: the strict mode reports an error beyond it, and the lenient mode truncates with a warning

## 18. Development and extension

All code lives under `src/`, and import paths carry no `src` segment

| Directory | Contents |
| --- | --- |
| `src/internal/apperr` | Structured errors and the permanent marker |
| `src/internal/i18n` | Language catalogues and the translator |
| `src/internal/settings` | Configuration value types |
| `src/internal/template` | Template engine and variable model |
| `src/internal/channel/` | Channel contract, registry, shared helpers and the channel packages |
| `src/internal/state/` | The timing state file |
| `src/internal/config` | Configuration sources, `${VAR}` expansion and validation |
| `src/internal/notify` | Send scheduling, concurrency and retry |
| `src/internal/cli` | Command and flag tables, parsing, rendering and exit codes |

Dependencies flow one way: `channel/<type> → channel/common → channel → template`, `notify → config/channel/template`, and `cli → config/notify/state/channel/i18n`

### 18.1 Adding a channel

1. Create `src/internal/channel/<type>/<type>.go`, implement `channel.Channel` and `channel.Factory`, and call `channel.Register` in `init()`
2. Append one blank import to `src/internal/channel/all/all.go`
3. Add a `channel_<type>.json` under both `src/internal/i18n/catalog/en/` and `catalog/zh-CN/`, with matching keys and placeholders
4. Leave `notify` and `cli` untouched

`docs/taskpush.example.json` holds one sample per channel, and `docs/taskpush.schema.json` drives editor completion and validation
