package gotify

/*
Gotify 渠道的渲染层: 单字段渲染, /message 端点的拼接与主机名提取, 以及模板错误与告警的字段路径改写
出网发送在 channel.go, 打码与 Problem 构造在 mask.go, 配置结构在 settings.go

The rendering layer of the Gotify channel: rendering one field, joining the /message endpoint and
reading its host name back, and rewriting the field path of a template error or warning
Sending sits in channel.go, masking and Problem construction in mask.go, and the configuration shapes
in settings.go
*/

import (
	"errors"
	"net/url"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// messageEndpoint 在已规范化的地址上构造消息端点, 并返回该地址的主机名
// path 先去掉尾部斜杠: 空 path (根地址) 与 "/message/" 这类写法都归一到 "/message", 其余 path 追加
// 一段 "/message", query 与 fragment 由 url.URL 重组时原样带回
//
// messageEndpoint builds the message endpoint from an already normalised address and returns the host
// name of that address
// The trailing slashes of the path come off first, which turns an empty path (a root address) and a
// "/message/" spelling into "/message" alike while any other path gets a "/message" segment appended,
// and the query and fragment are carried back by url.URL when it reassembles the address
func messageEndpoint(normalized string) (string, string, error) {
	u, err := url.Parse(normalized)
	if err != nil {
		return "", "", err
	}

	path := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(path, messagePath) {
		path += messagePath
	}
	u.Path = path

	endpoint := u.String()
	if u.Hostname() == "" {
		// NormalizeURL 已确认主机非空, 这是取不到主机名时的兜底: 预览与错误改用掩码地址
		//
		// NormalizeURL already confirmed a non-empty host, so this is the fallback for a host name
		// that cannot be read: the preview and the errors use the masked address instead
		return endpoint, common.MaskURL(endpoint), nil
	}
	return endpoint, u.Hostname(), nil
}

// renderField 渲染一个 settings 字符串字段
// 模板错误与告警里的 Path 先被改写为该字段路径, 因为模板引擎只填占位路径
//
// renderField renders one settings string field
// The Path inside a template error or warning is rewritten to this field path first, because the
// template engine only fills a placeholder path
func renderField(value, field string, vars *template.Vars, mode template.Mode) (string, []apperr.Problem, error) {
	out, warns, err := template.RenderWithMode(value, vars, mode)
	if err != nil {
		rewritePath(err, field)
		return "", nil, err
	}
	for i := range warns {
		setPath(&warns[i], field)
	}
	return out, warns, nil
}

// rewritePath 把 err 链上的模板 Problem 的 Path 改写为字段路径
//
// rewritePath rewrites the Path of the template Problem on the err chain into a field path
func rewritePath(err error, field string) {
	var prob *apperr.Problem
	if errors.As(err, &prob) {
		setPath(prob, field)
	}
}

// setPath 覆写 Problem.Data 里的 Path, Data 为 nil 时先建表
//
// setPath overwrites Path inside Problem.Data, creating the map when it is nil
func setPath(prob *apperr.Problem, field string) {
	if prob == nil {
		return
	}
	if prob.Data == nil {
		prob.Data = map[string]any{}
	}
	prob.Data["Path"] = field
}
