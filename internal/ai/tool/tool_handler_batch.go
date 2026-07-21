package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
)

// batchCommandItem 是 LLM 提交的批量项：对 N 个资产并发跑同一类命令。
type batchCommandItem struct {
	Asset   string `json:"asset"`
	Type    string `json:"type"`
	Command string `json:"command"`
}

// batchResultItem 是单条命令的执行结果（聚合后整体返回给 LLM）。
type batchResultItem struct {
	AssetID   int64  `json:"asset_id"`
	AssetName string `json:"asset_name"`
	Type      string `json:"type"`
	Command   string `json:"command"`
	ExitCode  int    `json:"exit_code"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr,omitempty"`
	Error     string `json:"error,omitempty"`
}

// handleBatchCommand 并发执行多条命令并聚合返回。
// 流程：解析 → 策略预检 → 聚合 needConfirm 一次审批 → max 10 并发执行。
// 与 opsctl batch 的审批/并发流程保持一致；桌面 AI 工具当前派发 exec/sql/redis。
func handleBatchCommand(ctx context.Context, args map[string]any) (string, error) {
	commandsRaw, ok := args["commands"]
	if !ok {
		return "", fmt.Errorf("missing required parameter: commands")
	}

	commandsJSON, err := json.Marshal(commandsRaw)
	if err != nil {
		return "", fmt.Errorf("invalid commands parameter: %w", err)
	}
	var commands []batchCommandItem
	// LLM 偶尔会把 commands 当字符串 JSON 传，兜底再 unmarshal 一层。
	if err := json.Unmarshal(commandsJSON, &commands); err != nil {
		var str string
		if jerr := json.Unmarshal(commandsJSON, &str); jerr == nil {
			if uerr := json.Unmarshal([]byte(str), &commands); uerr != nil {
				return "", fmt.Errorf("invalid commands format: %w", uerr)
			}
		} else {
			return "", fmt.Errorf("invalid commands format: %w", err)
		}
	}

	if len(commands) == 0 {
		return "No commands to execute.", nil
	}

	for i := range commands {
		if commands[i].Type == "" {
			commands[i].Type = "exec"
		}
	}

	checker := permission.GetPolicyChecker(ctx)

	type resolvedCmd struct {
		item      batchCommandItem
		assetID   int64
		assetName string
		decision  string // "allow" / "deny" / "needConfirm"
		denyMsg   string
	}
	resolved := make([]resolvedCmd, 0, len(commands))

	for _, cmd := range commands {
		assetID, assetName, resolveErr := resolveAssetForBatch(ctx, cmd.Asset)
		if resolveErr != nil {
			resolved = append(resolved, resolvedCmd{
				item: cmd, decision: "deny", denyMsg: fmt.Sprintf("asset not found: %s", cmd.Asset),
			})
			continue
		}

		decision := "allow"
		denyMsg := ""
		if checker != nil {
			result := permission.CheckPermission(ctx, batchApprovalAssetType(cmd.Type), assetID, cmd.Command)
			switch result.Decision {
			case aictx.Deny:
				decision = "deny"
				denyMsg = result.Message
			case aictx.NeedConfirm:
				decision = "needConfirm"
			case aictx.Allow:
				decision = "allow"
			}
		}

		resolved = append(resolved, resolvedCmd{
			item: cmd, assetID: assetID, assetName: assetName,
			decision: decision, denyMsg: denyMsg,
		})
	}

	// 聚合 needConfirm，一次性弹审批。
	if checker != nil && checker.ConfirmFunc() != nil {
		var needConfirmItems []permission.ApprovalItem
		var needConfirmIndices []int
		for i, r := range resolved {
			if r.decision == "needConfirm" {
				needConfirmItems = append(needConfirmItems, permission.ApprovalItem{
					Type:      r.item.Type,
					AssetID:   r.assetID,
					AssetName: r.assetName,
					Command:   r.item.Command,
				})
				needConfirmIndices = append(needConfirmIndices, i)
			}
		}
		if len(needConfirmItems) > 0 {
			resp := checker.ConfirmFunc()(ctx, "batch", needConfirmItems)
			for _, idx := range needConfirmIndices {
				if resp.Decision == "deny" {
					resolved[idx].decision = "deny"
					resolved[idx].denyMsg = "user denied batch execution"
				} else {
					resolved[idx].decision = "allow"
				}
			}
		}
	}

	var approved, denied []resolvedCmd
	for _, r := range resolved {
		if r.decision == "allow" {
			approved = append(approved, r)
		} else {
			denied = append(denied, r)
		}
	}

	const maxConcurrency = 10
	sem := make(chan struct{}, maxConcurrency)
	var mu sync.Mutex
	results := make([]batchResultItem, 0, len(commands))

	for _, r := range denied {
		results = append(results, batchResultItem{
			AssetID: r.assetID, AssetName: r.assetName,
			Type: r.item.Type, Command: r.item.Command,
			ExitCode: -1, Error: fmt.Sprintf("denied: %s", r.denyMsg),
		})
	}

	var wg sync.WaitGroup
	for _, r := range approved {
		wg.Add(1)
		go func(r resolvedCmd) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			result := executeBatchItem(ctx, r.item, r.assetID, r.assetName)
			mu.Lock()
			results = append(results, result)
			mu.Unlock()
		}(r)
	}
	wg.Wait()

	output, err := json.MarshalIndent(map[string]any{"results": results}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// executeBatchItem 把单条命令派发到对应资产类型的纯执行体（helper.Exec*OnAsset）。
//
// 直接调纯执行体，不再经过按类型区分的旧工具 handler（run_command / exec_sql /
// exec_redis，已随统一 exec 下线）：那些 handler 内部各自还做一次
// CheckForAsset，而 handleBatchCommand 上面已经对每一项做过策略预检、并把所有
// needConfirm 聚合成**一次**审批弹窗。走 handler 就等于把批准过的命令再检查一遍，
// 用户会为同一条命令被弹第二次框——kafka 侧删旧工具时修掉的正是这个形状
// （见 internal/ai/tool/tool_handlers_unified_kafka_test.go）。
func executeBatchItem(ctx context.Context, item batchCommandItem, assetID int64, assetName string) batchResultItem {
	result := batchResultItem{
		AssetID: assetID, AssetName: assetName,
		Type: item.Type, Command: item.Command,
	}

	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		result.ExitCode = -1
		result.Error = fmt.Sprintf("asset not found: %v", err)
		return result
	}

	var output string
	switch item.Type {
	case "exec":
		output, err = helper.ExecCommandOnAsset(ctx, asset, item.Command, "")
	case "sql":
		if !asset.IsDatabase() {
			result.ExitCode = -1
			result.Error = "asset is not database type"
			return result
		}
		output, err = helper.ExecSQLOnAsset(ctx, asset, item.Command, "")
	case "redis":
		if !asset.IsRedis() {
			result.ExitCode = -1
			result.Error = "asset is not Redis type"
			return result
		}
		output, err = helper.ExecRedisOnAsset(ctx, asset, item.Command, "")
	default:
		result.ExitCode = -1
		result.Error = fmt.Sprintf("unknown type: %s", item.Type)
		return result
	}

	if err != nil {
		result.ExitCode = -1
		result.Error = err.Error()
		return result
	}
	result.ExitCode = 0
	result.Stdout = output
	return result
}

// resolveAssetForBatch 把 LLM 传入的 asset 标识解析成 (id, name)。
//
// 只认**数字形式的 id**：它把 assetRef 包成 {"id": ...} 交给 handleGetAsset，而
// handleGetAsset 只做 aictx.ArgInt64(args, "id") + `id == 0 就报错`
// （tool_handlers_asset.go:176-180），没有任何 name→id 查询。所以按名字指定资产
// （batch_command 的参数描述允许这么写）在这里必然失败，报 "missing required
// parameter: id"。行为由 TestResolveAssetForBatch_NameRefDoesNotResolve 钉住。
//
// 补 name 解析要改的是 get_asset 工具自己的契约，不是在这里绕一条私路——那样
// batch_command 与 get_asset 对"什么是合法的资产标识"就有了两套答案。
func resolveAssetForBatch(ctx context.Context, assetRef string) (int64, string, error) {
	out, err := handleGetAsset(ctx, map[string]any{"id": assetRef})
	if err != nil {
		return 0, "", err
	}
	var asset struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &asset); err != nil {
		return 0, "", fmt.Errorf("cannot resolve asset: %s", assetRef)
	}
	return asset.ID, asset.Name, nil
}

// batchApprovalAssetType 把 batch 的 type 映射成 permission.CheckPermission 期望的资产类型字符串。
// permission.CheckPermission 内部据此选择对应的策略组（exec→ssh、sql→database、redis→redis）。
func batchApprovalAssetType(batchType string) string {
	switch batchType {
	case "sql":
		return asset_entity.AssetTypeDatabase
	case "redis":
		return asset_entity.AssetTypeRedis
	default:
		return asset_entity.AssetTypeSSH
	}
}
