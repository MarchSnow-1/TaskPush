package cli

import (
	"os"
	"path/filepath"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// ---------------------------------------------------------------------------
// init 命令: 在当前工作目录写一份最小配置, 只含占位值, 不含任何密钥
// 目标文件已存在时不覆盖, 只有 --force 才重写
//
// The init command: write a minimal configuration with placeholder values only and no secret into
// the current working directory
// An existing target is left alone unless --force is given
// ---------------------------------------------------------------------------

// initFileName 是 init 写出的配置文件名
//
// initFileName is the configuration file name init writes
const initFileName = "taskpush.json"

// initDocument 是 init 写出的配置内容
// 只含版本, 超时, 重试, 重试间隔, 代理与空 channels, 不预置渠道
// 因此生成的配置可被 check 直接加载, 也不会引用任何环境变量或密钥
//
// initDocument is the content init writes
// It carries the version, timeout, retries, retry delay, proxy and an empty channels array, with no
// channel preset
// The generated configuration therefore loads under check straight away and references no
// environment variable or secret
var initDocument = []byte(`{
  "version": 1,
  "timeout": "15s",
  "retries": 0,
  "retryDelay": "1s",
  "proxy": "",
  "channels": []
}
`)

// runInit 写出最小配置
// 目标存在且没有 --force 时输出 INFO 并退出 0, 不视为错误
//
// runInit writes the minimal configuration
// An existing target without --force earns an INFO and exits 0 rather than counting as a failure
func runInit(parsed *parsedArgs, p *printer) invocation {
	cwd, err := workingDir()
	if err != nil {
		return invocation{code: exitFailure, err: err}
	}
	path := filepath.Join(cwd, initFileName)
	if _, statErr := os.Stat(path); statErr == nil && !parsed.Force {
		p.info("cli.info.init_exists", map[string]any{"Path": path})
		return invocation{code: exitOK}
	}
	if err := os.WriteFile(path, initDocument, 0o600); err != nil {
		return invocation{
			code: exitFailure,
			err: &apperr.Problem{
				ID:    "cli.error.init_write",
				Data:  map[string]any{"Path": path, "Detail": err.Error()},
				Cause: err,
			},
		}
	}
	p.info("cli.info.init_created", map[string]any{"Path": path})
	return invocation{code: exitOK}
}
