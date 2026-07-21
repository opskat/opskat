package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/assetref"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// batchCommandItem 是 LLM 提交的批量项：对 N 个资产并发跑同一类命令。
//
// Type 与 exec 的 type 参数同语义：可选的类型断言，不参与派发（派发永远由资产真实类型
// 决定），空值表示不声明，声明了就必须对得上（含协议别名，如 "sql"→database）。
type batchCommandItem struct {
	Asset   string `json:"asset"`
	Type    string `json:"type"`
	Command string `json:"command"`
}

// batchResultItem 是单条命令的执行结果（聚合后整体返回给 LLM）。
// Type 是资产的真实类型（asset.Type），不是调用方声明的类型断言——
// 断言从不参与派发，回显真实类型才能让模型看懂"为什么这条被按这个策略检查"。
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
// 流程：解析 → （可选类型断言 + 规范化）策略预检 → 聚合 needConfirm 一次审批 →
// max 10 并发执行。与 opsctl batch 的审批/并发流程保持一致；派发按资产真实类型走
// permission.ExecutorFor，覆盖面与统一 exec 工具一致（含 database/redis/mongodb/etcd/
// kafka/k8s）。
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

	// batch_exec 只对 AI 会话开放（不在 AllToolDefs 里，opsctl 走自己的 batch 子命令
	// 并在那边自行 CheckPermission），所以这里没有 WithPreapproved 那条豁免：checker
	// 缺失一定是接线漏了。从前它 nil 时每一项的 decision 都停在初值 "allow"，整批命令
	// 一条不查地打到所有资产上。
	checker, err := permission.RequireChecker(ctx)
	if err != nil {
		return "", err
	}

	type resolvedCmd struct {
		item         batchCommandItem
		asset        *asset_entity.Asset // 仅当资产解析成功时非 nil
		assetID      int64
		assetName    string
		checkCommand string // 权限检查用的（可能已规范化的）命令串
		decision     string // "allow" / "deny" / "needConfirm"
		denyMsg      string
	}
	resolved := make([]resolvedCmd, 0, len(commands))

	for _, cmd := range commands {
		asset, resolveErr := assetref.Resolve(ctx, cmd.Asset)
		if resolveErr != nil {
			// 原样透出解析错误，不再一律写成 "asset not found"：同名歧义
			// （assetref.ErrAmbiguous）会明确告诉模型改用数字 id，压成"找不到"
			// 只会让它换个名字重试，而重试同样歧义。
			resolved = append(resolved, resolvedCmd{
				item: cmd, decision: "deny", denyMsg: resolveErr.Error(),
			})
			continue
		}

		// 可选类型断言——与 exec 同一个函数、同一条不变式（早于审批）。断言不参与
		// 派发：协议永远从 asset.Type 取，这里只把方言写错的情况提前变成一条点名
		// 双方类型的错误。
		if err := permission.AssertAssetType(asset, cmd.Type); err != nil {
			resolved = append(resolved, resolvedCmd{
				item: cmd, asset: asset, assetID: asset.ID, assetName: asset.Name,
				decision: "deny", denyMsg: err.Error(),
			})
			continue
		}

		if _, ok := permission.ExecutorFor(asset.Type); !ok {
			resolved = append(resolved, resolvedCmd{
				item: cmd, asset: asset, assetID: asset.ID, assetName: asset.Name,
				decision: "deny",
				denyMsg:  fmt.Sprintf("asset %q (type=%s) has no exec support yet", asset.Name, asset.Type),
			})
			continue
		}

		// 权限检查用规范化后的命令：批的是这个，就该按这个匹配策略/审批弹窗/审计——
		// 与 handleExec 的第 8 步同一理由（kafka 的双 token 串就是靠这条才对得上）。
		checkCommand := cmd.Command
		if canonicalize, ok := permission.CanonicalizeFor(asset.Type); ok {
			canonical, cerr := canonicalize(asset, cmd.Command)
			if cerr != nil {
				resolved = append(resolved, resolvedCmd{
					item: cmd, asset: asset, assetID: asset.ID, assetName: asset.Name,
					decision: "deny", denyMsg: cerr.Error(),
				})
				continue
			}
			checkCommand = canonical
		}

		decision, denyMsg := "allow", ""
		result := permission.CheckPermission(ctx, asset.Type, asset.ID, checkCommand)
		switch result.Decision {
		case aictx.Deny:
			decision, denyMsg = "deny", result.Message
		case aictx.NeedConfirm:
			decision = "needConfirm"
		case aictx.Allow:
			decision = "allow"
		}

		resolved = append(resolved, resolvedCmd{
			item: cmd, asset: asset, assetID: asset.ID, assetName: asset.Name,
			checkCommand: checkCommand, decision: decision, denyMsg: denyMsg,
		})
	}

	// 聚合 needConfirm，一次性弹审批。
	if checker.ConfirmFunc() != nil {
		var needConfirmItems []permission.ApprovalItem
		var needConfirmIndices []int
		for i, r := range resolved {
			if r.decision == "needConfirm" {
				needConfirmItems = append(needConfirmItems, permission.ApprovalItem{
					Type:      permission.ApprovalTypeFor(r.asset.Type),
					AssetID:   r.assetID,
					AssetName: r.assetName,
					Command:   r.checkCommand,
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
		// 已解析出资产的条目回显真实类型；解析本身失败的条目没有类型可回显。
		resultType := r.item.Type
		if r.asset != nil {
			resultType = r.asset.Type
		}
		results = append(results, batchResultItem{
			AssetID: r.assetID, AssetName: r.assetName,
			Type: resultType, Command: r.item.Command,
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

			result := executeBatchItem(ctx, r.item, r.asset)
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

// executeBatchItem 把单条命令派发到资产真实类型对应的执行器（permission.ExecutorFor），
// 覆盖面与统一 exec 工具一致——不再是写死的 exec/sql/redis 三路 switch。
func executeBatchItem(ctx context.Context, item batchCommandItem, asset *asset_entity.Asset) batchResultItem {
	result := batchResultItem{
		AssetID: asset.ID, AssetName: asset.Name,
		Type: asset.Type, Command: item.Command,
	}

	exec, ok := permission.ExecutorFor(asset.Type)
	if !ok {
		// 解析阶段已经拦过一次；这里是并发执行路径上的兜底，只可能在执行器被
		// 反注册（仅测试）时发生。
		result.ExitCode = -1
		result.Error = fmt.Sprintf("asset %q (type=%s) has no exec support yet", asset.Name, asset.Type)
		return result
	}

	// 执行用**原始**命令，不是规范化后的串——规范化结果是给策略/审批/审计看的展示形式，
	// 喂给执行器是有损的（引号与内部空格会被吃掉）。与 handleExec 第 8 步同一理由。
	output, err := exec(ctx, asset, item.Command, "")
	if err != nil {
		result.ExitCode = -1
		result.Error = err.Error()
		return result
	}
	result.ExitCode = 0
	result.Stdout = output
	return result
}
