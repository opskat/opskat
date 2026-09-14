# AI Provider 自定义请求头

> Status: Draft
> Owner: OpsKat 维护者
> Last updated: 2026-09-14

**Objective:** 让用户为单个 AI Provider 配置额外的 HTTP 请求头，并支持把值绑定到当前会话，从而能正常使用 OpenCode 这类要求会话头的网关。

**Hard invariant:** 未配置自定义请求头的 Provider，其请求内容与现在逐字节一致；自定义请求头永远不能改写 OpsKat 自己设置的鉴权头。

## Problem

1. **OpsKat 无法连接 OpenCode 网关（issue #314）。** 对话请求返回
   `400 ... Request is missing x-opencode-session and cannot be routed efficiently`。
   OpenCode Go 的中继自 2026-09-05 起要求每个推理请求带一个稳定的 `x-opencode-session`
   头（用于后端亲和与 prompt cache 路由）。同期
   [deepseek-harness#5495](https://github.com/deepseek-ai/deepseek-harness/discussions/5495)、
   [openclaw#144763](https://github.com/openclaw/openclaw/issues/144763) 等一批客户端
   中了同一问题，社区修法统一是按会话注入一个稳定的 opaque 标识。
2. **报告人「能识别到模型、但无法使用」有据可查。** 模型列表走
   `internal/ai/runner/fetch_models.go:34` 的 `GET /models`，不经过中继，因此成功；
   真正的对话请求经中继被拒。
3. **配置面上没有任何口子。** `ai_provider_entity.AIProvider` 只有
   `APIBase / APIKey / Model / MaxOutputTokens / ContextWindow / Reasoning*`；
   `internal/ai/runner/runner.go:27` 的 `BuildProvider` 也只消费这些字段，
   用户无处填写额外的头。

## Actors and user stories

1. 作为使用第三方网关的用户，我想给 Provider 加上网关要求的请求头，这样不必等 OpsKat
   为每一家网关单独适配就能用起来。
2. 作为 OpenCode 用户，我想让会话头随对话自动变化，这样既不会 400，也不会因为所有对话
   共用一个 session 而破坏对端的路由亲和与 prompt cache。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | 做成**通用**的 key/value 请求头配置，而不是识别 OpenCode 域名自动加头 | AGENTS.md 的 OCP 约定禁止在共享代码里按厂商/类型字符串分支；通用口子同时覆盖 OpenRouter、企业代理、AI Gateway 等。Rejected: 按 `APIBase` 命中 `opencode` 自动注入 —— 写死厂商分支，下一个网关还得再改一次 |
| 2 | 值支持 `{{session}}` 一个占位符 | 对端要求的是「每会话稳定」而非「全局固定」；填死常量虽然能消掉 400，但会把所有对话压到同一个 session 上。Rejected: 只支持字面量 —— 用户无法表达会话维度；Rejected: 提供一整套模板语法 —— 没有第二个已知需求，属于无依据的扩展点 |
| 3 | 用户填 `Authorization` / `x-api-key` / `anthropic-version` / `Content-Type` 时**硬拦，不允许保存** | 这些头由 OpsKat 自己写，用户填了只会被静默丢弃，允许保存等于让用户以为生效。Rejected: 只告警但允许覆盖 —— 极易把 API Key 配到失效，排查成本远高于收益 |
| 4 | 头的值**明文存储**，不走凭证加密通道 | 维护者决策。取舍已知并接受：见「安全与隐私」 |
| 5 | 不提供网关快捷预设按钮 | 维护者决策：预设一旦开头，就会变成一份需要跟随上游变化维护的网关名单 |
| 6 | anthropic 兼容路径同步支持 | issue #314 报告人两种兼容模式都试过；只修 openai 一侧等于问题只解决一半。代价是需要先在 `github.com/cago-frame/agents` 的 anthropics provider 上开出请求头入口——当前 `Config` 只有 `BaseURL/APIKey/MaxRetries/CacheTTL`，无 HTTP client 或 header 钩子 |

## 配置与校验

Provider 配置新增一组有序的「自定义请求头」条目，每条是一个名称与一个值，默认为空。

- **前置**：用户在 Provider 表单中展开「自定义请求头」区块。**动作**：添加一条并填写名称与值。
  **可观察结果**：保存后该 Provider 的后续请求都会带上这个头。
- **前置**：用户填入的名称与同一 Provider 中更靠前的一条重名（大小写不敏感）。**动作**：尝试保存。
  **可观察结果**：靠后的那一条标记为错误并给出「请合并成一条」的说明，保存被禁用；靠前的那条不受影响。
- **前置**：用户填入的名称属于 OpsKat 自己设置的头。**动作**：尝试保存。
  **可观察结果**：该条标记为错误并说明该头由 OpsKat 设置、不能在此覆盖，保存被禁用。
- **前置**：某条的名称为空。**动作**：保存。**可观察结果**：该条被丢弃，不产生任何请求头，也不报错——
  这是用户点了「添加」但改变主意的正常路径。值为空但名称非空的条目保留，发送空值头。

未配置任何条目的 Provider，其请求与本次改动前一致。

## 会话占位符

值中的 `{{session}}` 在**每次发出请求前**展开为当前对话的标识。

- 同一个对话内的每一次请求，展开结果都相同，跨应用重启依然相同。
- 不同对话之间，展开结果不同。
- 展开结果不泄露 OpsKat 的内部标识，第三方网关无法从中反推用户有多少个对话。
- 会话级 Provider 切换（#246）下，头跟随本次实际使用的 Provider，展开结果仍由对话决定。

拉取模型列表不发生在任何对话里。此时 `{{session}}` 展开为一个与对话无关、但在同一安装内稳定的值，
使得需要会话头的网关也能成功返回模型列表。

`{{session}}` 之外的内容按字面量发送，不做任何转义或模板求值。

## 请求路径覆盖

自定义请求头对该 Provider 的**全部**出站 HTTP 请求生效，即对话请求与拉取模型列表两条路径一致；
openai 兼容与 anthropic 兼容两种 Provider 类型的行为相同。

## 安全与隐私

头的值明文存入本地数据库。这是维护者的明确决策，代价是：网关 token、代理密钥这类写在自定义头里的
凭证，会随备份、导出、以及任何直接读库的手段以明文形式带出，与 `APIKey` 字段已有的加密保护不同步。
界面上不对这些值做遮蔽显示。

自定义头随请求发往用户自己填写的 `APIBase`，OpsKat 不向任何其他地址发送它们。

## UI

区块位于 Provider 表单中「推理强度」卡片之后、保存按钮之前，与推理强度采用同一种分组卡片外观，
默认折叠。折叠态在标题旁显示已配置条目数；未配置时不显示计数，不打扰主流程。

每条渲染为「名称 + 值 + 删除」。可用宽度不足时（Provider 表单在设置页与新手向导中宽度不同）
折行为「名称 + 删除」与「值」两行，而不是压缩名称输入框——按容器宽度而非视口宽度决定。
Tab 顺序始终是名称 → 值 → 删除。

空态说明「某些网关要求请求带上额外的头，否则会返回 400」，输入框占位文本直接给出
`x-opencode-session` 与 `{{session}}`。区块底部固定一段说明，讲清 `{{session}}` 的会话语义
以及 OpenCode 这类网关的要求。

亮色与暗色主题均使用既有 token；错误态使用 `destructive` token。

视觉参考（非约束）：`.dev-kit/artifacts/2026-09-14-ai-provider-custom-headers/mockups/`。

## Out of scope

- 请求头的加密存储（决策 4 已明确不做）
- 网关快捷预设 / 内置网关名单（决策 5）
- `{{session}}` 之外的模板变量
- 为 OpenCode 的 `/v1/responses`、Google 格式等其他端点形态做适配
- 全局（跨 Provider）的请求头配置

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| Provider 表单组件 | 重名与保留头阻止保存；空名称条目被丢弃；折叠态计数 | `frontend/src/__tests__/AIProviderForm.test.tsx` |
| `runner.BuildProvider` 构造出的 provider 发出的请求 | 配置的头出现在实际请求上；`{{session}}` 按会话展开且同会话稳定、跨会话不同；未配置时请求头与改动前一致 | cago 的 `provider/openai/openai_http_test.go` / `provider/anthropics/anthropics_http_test.go` 用的是同类 HTTP 断言 |
| 模型列表拉取 | 自定义头同样出现在 `GET /models` 上 | 无 |

无法自动化的部分：对 OpenCode 真实网关的端到端验证需要报告人的账号与 API Key，由 issue #314
的报告人在预发布版本上确认，或在维护者自备 key 时手动跑一次。cago 侧的请求头入口需要先在
`github.com/cago-frame/agents` 落地并发版，本仓才能完成 anthropic 路径——这条依赖在实现计划里
排在最前面。

## Open questions

无。
