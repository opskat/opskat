// Package jevreview 是用 TypeSafe Jev 模型审核一条命令的纯逻辑：题目、发给模型的上下文、
// 按阈值判定、失败原因归类。不依赖数据库和宿主，内置审核（command_review_svc）和
// 命令审核扩展（extensions/command-review，编译成 WASM）共用这一份。
package jevreview

import (
	"context"
	"errors"
	"sort"

	"github.com/opskat/opskat/internal/pkg/typesafe"
)

// 题目 ID 只给代码用，不会发给模型；题目的完整含义写在 instructions 里。
// 审核未通过时，未通过的是这些 ID。
const (
	QuestionDestructive = "destructive"
	QuestionDisruptive  = "disruptive"
	QuestionRemoteCode  = "remote_code"
)

// 设置没填时的默认值：TypeSafe 官方地址上的 Jev 具体版本。
const (
	DefaultBaseURL   = typesafe.DefaultBaseURL
	DefaultModel     = "jev-1.13.0"
	DefaultThreshold = 0.5
)

// QuestionsVersion 在题目文字或通过标准变化时递增，让旧的缓存失效。
const QuestionsVersion = "3"

// 调用模型失败的原因，与宿主的审核失败原因同名。
const (
	ReasonTimeout       = "timeout"
	ReasonInvalidAPIKey = "invalid_api_key"
	ReasonUnavailable   = "unavailable"
)

// Questions 只问明确的危险：破坏数据、中断服务、下载并执行代码。只读的查看类命令
// （状态、日志、配置、进程、容器、定时任务、网络状态）在每道题的"否"里都写明了，
// 这类命令不应被拦下来。"破坏数据"只算不可恢复的修改，容易撤销的小改动也写成"否"。
var Questions = map[string]typesafe.Question{
	QuestionDestructive: {
		Type:         typesafe.QuestionNoul,
		Instructions: "Would running `command` on a `asset_type` asset clearly destroy data or make an irreversible change, such as deleting files or directories, wiping or formatting disks, dropping or truncating databases, tables, or collections, bulk-deleting keys, records, or storage objects, or deleting users, credentials, or keys?",
		Criteria: map[string]string{
			"true":  "The command clearly deletes, wipes, overwrites, or irreversibly changes data, files, databases, users, credentials, or keys.",
			"false": "The command only reads, lists, searches, or shows status, logs, configuration, processes, containers, scheduled jobs, or network state, or it makes a small change that is easy to undo.",
		},
	},
	QuestionDisruptive: {
		Type:         typesafe.QuestionNoul,
		Instructions: "Would running `command` on a `asset_type` asset clearly interrupt a running system, such as stopping, restarting, killing, or disabling a service, container, process, or the host, or changing firewall, routing, or network settings so that the host or its services become unreachable?",
		Criteria: map[string]string{
			"true":  "The command stops, restarts, kills, or disables something that is running, or cuts off network access to the host or its services.",
			"false": "The command only reads, lists, searches, or shows status (such as service or VPN status), logs, configuration, processes, containers, scheduled jobs (such as timers), or network state, and leaves services, processes, the host, and the network running as they are.",
		},
	},
	QuestionRemoteCode: {
		Type:         typesafe.QuestionNoul,
		Instructions: "Does `command` download code or a script from the network and run it, for example by piping `curl` or `wget` output into a shell?",
		Criteria: map[string]string{
			"true":  "The command fetches code or a script from the network and runs it.",
			"false": "The command does not run code fetched from the network: it only reads, lists, searches, or shows status, logs, configuration, processes, containers, scheduled jobs, or network state, or it downloads a file without running it, or queries a remote service.",
		},
	},
}

// State 是发给模型的上下文。
type State struct {
	AssetType string `json:"asset_type"`
	Command   string `json:"command"`
}

// Request 是审核一条命令的请求。
func Request(model, assetType, command string) typesafe.Request {
	return typesafe.Request{Model: model, State: State{AssetType: assetType, Command: command}, Questions: Questions}
}

// Failed 按阈值判断：任意一题"是"的概率达到阈值即不通过，返回未通过的题目（排好序）。
func Failed(scores map[string]float64, threshold float64) []string {
	var failed []string
	for id, p := range scores {
		if p >= threshold {
			failed = append(failed, id)
		}
	}
	sort.Strings(failed)
	return failed
}

// FailReason 把调用模型的错误归成失败原因。
func FailReason(err error) string {
	var apiErr *typesafe.APIError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return ReasonTimeout
	case errors.As(err, &apiErr) && apiErr.IsAuth():
		return ReasonInvalidAPIKey
	default:
		return ReasonUnavailable
	}
}

// Retryable 判断一次调用失败后值不值得再试：这次调用超时或网络出错时重试。服务端明确返回的
// 错误（API key 无效、请求有误等）重试也不会成功，429 / 529 已经在客户端里退避重试过；
// 调用方已经取消时也不再试。
func Retryable(ctx context.Context, err error) bool {
	var apiErr *typesafe.APIError
	return ctx.Err() == nil && !errors.As(err, &apiErr)
}
