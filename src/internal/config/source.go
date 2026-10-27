package config

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// ---------------------------------------------------------------------------
// 配置来源解析: --config 优先, 其次是 TASKPUSH_CONFIG_JSON, 最后是当前目录的 taskpush.json
// 三种来源在读取阶段就施加 1MB 上限, 避免解析器面对一个无界输入
//
// Resolving the configuration source: --config first, then TASKPUSH_CONFIG_JSON, and the
// taskpush.json of the current directory last
// All three sources get the 1MB ceiling at read time, which keeps the parser from facing an
// unbounded input
// ---------------------------------------------------------------------------

const (
	// configEnvVar 是承载整份配置 JSON 的环境变量名
	// configEnvVar is the environment variable holding the whole configuration JSON
	configEnvVar = "TASKPUSH_CONFIG_JSON"
	// defaultConfigFile 是当前工作目录下的默认配置文件名
	// defaultConfigFile is the default configuration file name inside the current working directory
	defaultConfigFile = "taskpush.json"
	// maxConfigSize 是文件, stdin 与环境变量三种来源的字节上限
	// maxConfigSize is the byte ceiling shared by the file, stdin and environment sources
	maxConfigSize = 1 << 20
)

// utf8BOM 是 UTF-8 文本可能带有的字节序标记
// 读取后先去掉它, 让后续 JSON 解析看到普通文本
//
// utf8BOM is the byte-order mark a UTF-8 text may carry
// It is removed after reading so the JSON parser sees ordinary text
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// SourceOptions 是来源解析的输入
// Path 来自 --config, CWD 用于解析相对路径, Stdin 供 "-" 使用, LookupEnv 注入环境变量查询
//
// SourceOptions is the input of source resolution
// Path comes from --config, CWD resolves a relative path, Stdin serves "-", and LookupEnv
// injects the environment lookup
type SourceOptions struct {
	Path      string
	CWD       string
	Stdin     io.Reader
	LookupEnv func(string) (string, bool)
}

// defaultLookupEnv 是进程环境查询, 供未注入查询函数的调用路径使用
//
// defaultLookupEnv is the process environment lookup used by a call path without an injected one
func defaultLookupEnv(name string) (string, bool) {
	return os.LookupEnv(name)
}

// ResolveSource 按优先级选出配置来源并读出内容
// --config 覆盖环境变量, 环境变量覆盖默认文件; 已设置但为空的环境变量报错而不回退到文件
// 读取失败或超过 1MB 返回 config.error.read, 空内容返回 config.error.empty
//
// ResolveSource picks the configuration source by priority and reads its content
// --config overrides the environment variable, and the environment variable overrides the
// default file, while an environment variable set to an empty value fails instead of falling
// back to the file
// A read failure or more than 1MB returns config.error.read, and empty content returns
// config.error.empty
func ResolveSource(opts SourceOptions) (Source, error) {
	lookup := opts.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}

	if opts.Path != "" {
		if opts.Path == "-" {
			return readSource(Source{Kind: SourceStdin, Path: "stdin"}, opts.Stdin)
		}
		path, err := resolvePath(opts.CWD, opts.Path)
		if err != nil {
			return Source{}, newProblem("config.error.read", map[string]any{
				"Source": "file:" + opts.Path,
				"Detail": err.Error(),
			}, err)
		}
		return readFileSource(path)
	}

	if raw, ok := lookup(configEnvVar); ok {
		source := Source{Kind: SourceEnv, Path: configEnvVar}
		if raw == "" {
			return Source{}, newProblem("config.error.empty", map[string]any{
				"Source": source.Label(),
			}, nil)
		}
		return readSource(source, bytes.NewReader([]byte(raw)))
	}

	cwd, err := resolveCWD(opts.CWD)
	if err != nil {
		return Source{}, newProblem("config.error.read", map[string]any{
			"Source": "file:" + defaultConfigFile,
			"Detail": err.Error(),
		}, err)
	}
	path := filepath.Join(cwd, defaultConfigFile)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Source{}, newProblem("config.error.source_missing", map[string]any{
				"Hint": "--webhook, --type/--set, --config or " + configEnvVar,
			}, nil)
		}
		return Source{}, newProblem("config.error.read", map[string]any{
			"Source": "file:" + path,
			"Detail": err.Error(),
		}, err)
	}
	return readFileSource(path)
}

// readFileSource 读取一个已确定绝对路径的配置文件
//
// readFileSource reads a configuration file whose absolute path is already known
func readFileSource(path string) (Source, error) {
	file, err := os.Open(path)
	if err != nil {
		return Source{}, newProblem("config.error.read", map[string]any{
			"Source": "file:" + path,
			"Detail": err.Error(),
		}, err)
	}
	defer file.Close()

	source := Source{Kind: SourceFile, Path: path}
	return readSource(source, file)
}

// readSource 从 reader 读取内容并施加统一的空内容与大小检查
// 读取上限多读一个字节, 因此恰好 1MB 可用而 1MB+1 失败, 且不会把超限内容全部读进内存
//
// readSource reads from a reader and applies the shared empty-content and size checks
// One byte beyond the ceiling is read, so exactly 1MB works while 1MB+1 fails, and an oversized
// input is not read into memory in full
func readSource(source Source, reader io.Reader) (Source, error) {
	if reader == nil {
		return Source{}, newProblem("config.error.read", map[string]any{
			"Source": source.Label(),
			"Detail": "the reader is nil",
		}, nil)
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxConfigSize+1))
	if err != nil {
		return Source{}, newProblem("config.error.read", map[string]any{
			"Source": source.Label(),
			"Detail": err.Error(),
		}, err)
	}
	if len(data) > maxConfigSize {
		return Source{}, newProblem("config.error.read", map[string]any{
			"Source": source.Label(),
			"Detail": "content exceeds the 1MB limit",
		}, nil)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return Source{}, newProblem("config.error.empty", map[string]any{
			"Source": source.Label(),
		}, nil)
	}
	source.Data = stripBOM(data)
	return source, nil
}

// stripBOM 去掉开头的 UTF-8 BOM, 其余字节原样保留
// 只处理文件最前面的三个字节, 正文里出现的同样字节序列不受影响
//
// stripBOM removes a leading UTF-8 BOM and keeps every other byte as it was
// Only the first three bytes of the file are handled, so the same byte sequence inside the
// content is left alone
func stripBOM(data []byte) []byte {
	return bytes.TrimPrefix(data, utf8BOM)
}

// resolvePath 把 --config 给出的相对路径按 CWD 解析成绝对路径
//
// resolvePath resolves a relative --config path against CWD into an absolute path
func resolvePath(cwd, path string) (string, error) {
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	base, err := resolveCWD(cwd)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, path), nil
}

// resolveCWD 返回用于解析相对路径的工作目录
// CWD 为空时回退到进程当前目录, 这样直接调用 ResolveSource 的调用方也能得到绝对路径
//
// resolveCWD returns the working directory used to resolve relative paths
// An empty CWD falls back to the process working directory, so a direct ResolveSource call also
// gets an absolute path
func resolveCWD(cwd string) (string, error) {
	if cwd != "" {
		return filepath.Abs(cwd)
	}
	return os.Getwd()
}
