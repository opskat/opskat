import { useCallback, useEffect, useImperativeHandle, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { AlertTriangle, Info, RefreshCw } from "lucide-react";
import { Button, cn, Input, Textarea } from "@opskat/ui";
import { Field } from "@/components/asset/fields";
import { SecretInput } from "@/components/SecretInput";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import { useAssetCredential, type UseAssetCredential } from "./useAssetCredential";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import { proxyChainValidationKey, resolveSaveProxyChainSecrets, resolveSaveProxyPassword } from "./proxyConfig";
import {
  buildRedisConfig,
  listedMappingAnnouncedAddresses,
  nodeAddressMapValidationKey,
  parseNodeAddressMap,
  parseRedisConfig,
  parseRedisNodes,
  redisModeRequiredKey,
  resolveSaveSentinelPassword,
  resolveTestSentinelPassword,
  REDIS_DEFAULTS,
  type RedisFormState,
} from "./RedisConfigSection.config";
import type {
  AssetFormHandle,
  AssetTestAttempt,
  AssetTestResult,
  ConfigSectionProps,
} from "@/lib/assetTypes/formContract";
import { CancelTest } from "../../../wailsjs/go/system/System";
import { RedisProbe } from "../../../wailsjs/go/query/Query";
import { redis_svc } from "../../../wailsjs/go/models";

type Translate = (key: string, opts?: Record<string, unknown>) => string;

// 生成测试/探测的唯一 ID；配合后端 CancelTest 中断本次测试(镜像 AssetForm 的 newTestId,section 内自持)。
function newRedisTestId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `redis-test-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

/** 哨兵需要单独密码时用于区分测试失败原因,driving errorMessage 文案。 */
class RedisSentinelAuthRequiredError extends Error {}

/** 组装一次探测请求(测试连接 / 从哨兵读取 / 补全均复用):复用表单当前认证、隧道、TLS 配置。
 *  哨兵密码走 plainSentinelPassword 单独传,configJSON 只带既有密文(镜像数据节点密码 test 路径)。 */
function buildProbeRequest(state: RedisFormState, cred: UseAssetCredential) {
  const configJSON = buildRedisConfig(
    state,
    resolveTestCredential(cred.value),
    true,
    state.proxyPassword,
    Object.fromEntries(
      state.proxyChainLayers.map((layer) => [layer.id, { password: layer.password, token: layer.token }])
    ),
    resolveTestSentinelPassword(state)
  );
  return { configJSON, password: cred.value.password, sentinelPassword: state.sentinelPassword };
}

/** 测试成功行完整文案:集群/哨兵模式跳过壳的冒号外壳,自行以「 · 」分隔拼出整句(spec:「连接成功 ·
 *  集群 ok · M 主 R 从」/「连接成功 · 当前主节点 host:port」);标准模式留空,壳出通用整句「连接成功」。
 *  集群状态标签统一走 redisClusterStateLabel,ok/非 ok 都带「集群」前缀,非 ok 时如实显示原始 state。 */
function redisTestSuccessText(
  t: Translate,
  mode: RedisFormState["mode"],
  result: redis_svc.RedisProbeResult
): string | undefined {
  if (mode === "cluster" && result.cluster) {
    const state = t("asset.redisClusterStateLabel", { state: result.cluster.state });
    const unreachable = result.cluster.unreachableNodes?.length ?? 0;
    const detail = t(unreachable > 0 ? "asset.redisTestClusterUnreachableDetail" : "asset.redisTestClusterDetail", {
      state,
      masters: result.cluster.masters,
      replicas: result.cluster.replicas,
      count: unreachable,
    });
    return `${t("asset.testConnectionSuccess")} · ${detail}`;
  }
  if (mode === "sentinel" && result.sentinel) {
    const detail = t("asset.redisTestSentinelDetail", { addr: result.sentinel.masterAddr });
    return `${t("asset.testConnectionSuccess")} · ${detail}`;
  }
  return undefined;
}

export function RedisConfigSection({ editAsset, onValidityChange, ref }: ConfigSectionProps) {
  const { t } = useTranslation();
  const cred = useAssetCredential(editAsset);
  const baseHandleRef = useRef<AssetFormHandle>(null);
  const mountedRef = useRef(true);
  const activeAttemptRef = useRef<AssetTestAttempt | null>(null);
  const activeAttemptTokenRef = useRef<symbol | null>(null);

  // 识别结果为面板局部状态,来自最近一次探测(测试连接 / 从哨兵读取 / 补全);不参与序列化。
  const [lastProbe, setLastProbe] = useState<redis_svc.RedisProbeResult | null>(null);
  const [sentinelGroups, setSentinelGroups] = useState<redis_svc.RedisProbeSentinelGroup[]>([]);
  const [readingSentinel, setReadingSentinel] = useState(false);
  const [sentinelAuthFlash, setSentinelAuthFlash] = useState(false);
  const sentinelPasswordRef = useRef<HTMLInputElement>(null);

  // 哨兵回复需要认证:提示「哨兵需要单独的密码」并聚焦哨兵认证区的密码框。
  const flagSentinelAuthRequired = useCallback(() => {
    setSentinelAuthFlash(true);
    sentinelPasswordRef.current?.focus();
  }, []);

  const { state, patch } = useConfigSection<RedisFormState>({
    ref: baseHandleRef,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseRedisConfig(a.Config, a.sshTunnelId || 0) : { ...REDIS_DEFAULTS }),
    validate: (s) => {
      const requiredKey = redisModeRequiredKey(s);
      const proxyChainError = proxyChainValidationKey(s.proxyChainLayers);
      const mappingKey = s.mode !== "standalone" ? nodeAddressMapValidationKey(s.nodeAddressMap) : "";
      const canUse = !requiredKey && !proxyChainError;
      return {
        canTest: canUse,
        canSave: canUse && !mappingKey,
        saveDisabledReason: requiredKey || proxyChainError || mappingKey,
      };
    },
    build: async (s, ctx) => ({
      configJSON: buildRedisConfig(
        s,
        await resolveSaveCredential(cred.value, ctx.encryptPassword),
        false,
        await resolveSaveProxyPassword(s, ctx.encryptPassword),
        await resolveSaveProxyChainSecrets(s.proxyChainLayers, ctx.encryptPassword),
        await resolveSaveSentinelPassword(s, ctx.encryptPassword)
      ),
      sshTunnelId: s.connectionType === "jumphost" ? s.sshTunnelId : 0,
    }),
    buildTest: async (s) => {
      const req = buildProbeRequest(s, cred);
      return { assetType: "redis", configJSON: req.configJSON, password: req.password };
    },
    deps: [cred.value],
  });

  // 识别探测(从哨兵读取 / 失焦 / 切换识别出的模式后续识别):结果只更新识别状态,不做测试连接判定。
  const runRecognitionProbe = useCallback(
    async (formState: RedisFormState) => {
      setReadingSentinel(true);
      try {
        const testID = newRedisTestId();
        const req = buildProbeRequest(formState, cred);
        const result = await RedisProbe(testID, req.configJSON, req.password, req.sentinelPassword);
        if (!mountedRef.current) return;
        setLastProbe(result);
        if (result.sentinel?.authRequired) flagSentinelAuthRequired();
        const groups = result.sentinel?.groups ?? [];
        setSentinelGroups(groups);
        if (groups.length === 1 && !formState.masterName.trim()) {
          patch({ masterName: groups[0].name });
        }
      } catch (err) {
        toast.error(String(err));
      } finally {
        if (mountedRef.current) setReadingSentinel(false);
      }
    },
    [cred, patch, flagSentinelAuthRequired]
  );

  const readSentinelGroups = useCallback(() => {
    if (parseRedisNodes(state.nodes).length === 0) return;
    void runRecognitionProbe(state);
  }, [state, runRecognitionProbe]);

  const completeSentinels = useCallback(() => {
    const current = parseRedisNodes(state.nodes);
    const missing = (lastProbe?.sentinel?.otherSentinels ?? []).filter((addr) => !current.includes(addr));
    if (missing.length === 0) return;
    patch({ nodes: [...current, ...missing].join("\n") });
  }, [state.nodes, lastProbe, patch]);

  const switchToDetectedMode = useCallback(() => {
    const detected = lastProbe?.detectedMode;
    if (detected !== "cluster" && detected !== "sentinel") return;
    // 当前 host:port 作为第一个种子节点 / 哨兵节点,之前在该模式下填过的节点保留在其后。
    const seed = state.host && state.port ? `${state.host}:${state.port}` : "";
    const nodes = [seed, ...parseRedisNodes(state.nodes).filter((n) => n !== seed)].filter(Boolean).join("\n");
    const next: RedisFormState = { ...state, mode: detected, nodes };
    patch({ mode: detected, nodes });
    setLastProbe(null);
    // 按对应模式执行后续识别:集群 → 不可直连的节点;哨兵 → 组名 / 其它哨兵 / 认证。
    void runRecognitionProbe(next);
  }, [lastProbe, state, patch, runRecognitionProbe]);

  const generateMapping = useCallback(() => {
    const unreachable = lastProbe?.cluster?.unreachableNodes ?? [];
    const listed = listedMappingAnnouncedAddresses(state.nodeAddressMap);
    const missing = unreachable.filter((addr) => !listed.has(addr));
    if (missing.length === 0) return;
    const existingLines = state.nodeAddressMap.split("\n").filter((l) => l.trim());
    patch({ nodeAddressMap: [...existingLines, ...missing.map((addr) => `${addr} = `)].join("\n") });
  }, [lastProbe, state.nodeAddressMap, patch]);

  const startTest = useCallback((): AssetTestAttempt => {
    activeAttemptRef.current?.cancel();
    const token = Symbol("redis-form-test");
    const testID = newRedisTestId();
    let active = true;
    let testStarted = false;

    const run = async (): Promise<AssetTestResult> => {
      const req = buildProbeRequest(state, cred);
      testStarted = true;
      const result = await RedisProbe(testID, req.configJSON, req.password, req.sentinelPassword);
      if (!active) throw new Error("cancelled");
      if (mountedRef.current) setLastProbe(result);
      if (result.sentinel?.authRequired) {
        if (mountedRef.current) flagSentinelAuthRequired();
        throw new RedisSentinelAuthRequiredError();
      }
      return { successText: redisTestSuccessText(t, state.mode, result) };
    };

    const resultPromise = run().finally(() => {
      active = false;
      if (activeAttemptTokenRef.current === token) {
        activeAttemptRef.current = null;
        activeAttemptTokenRef.current = null;
      }
    });
    const attempt: AssetTestAttempt = {
      result: resultPromise,
      cancel: () => {
        if (!active) return;
        active = false;
        if (testStarted) void CancelTest(testID);
      },
      errorMessage: (e) =>
        e instanceof RedisSentinelAuthRequiredError ? t("asset.redisSentinelAuthRequired") : undefined,
    };
    activeAttemptRef.current = attempt;
    activeAttemptTokenRef.current = token;
    return attempt;
  }, [state, cred, t, flagSentinelAuthRequired]);

  useImperativeHandle(
    ref,
    () => ({
      buildConfig: (ctx) => baseHandleRef.current!.buildConfig(ctx),
      buildTestConfig: (ctx) => baseHandleRef.current!.buildTestConfig!(ctx),
      startTest: () => startTest(),
    }),
    [startTest]
  );

  // StrictMode 开发态会卸载再挂载同一实例:挂载时重置为 true,否则卸载清理后探测结果全被丢弃。
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      activeAttemptRef.current?.cancel();
    };
  }, []);

  const mismatchBanner = (s: RedisFormState) => {
    if (!lastProbe?.modeMismatch) return null;
    const detected = lastProbe.detectedMode;
    if (detected !== "cluster" && detected !== "sentinel") return null;
    const addr = s.host && s.port ? `${s.host}:${s.port}` : "";
    return (
      <div className="flex items-start gap-2 rounded-md border border-primary/30 bg-primary/5 px-3 py-2 text-xs">
        <Info className="mt-0.5 size-3.5 shrink-0 text-primary" />
        <div className="min-w-0 flex-1">
          <div className="font-medium">
            {t(detected === "cluster" ? "asset.redisDetectedClusterNode" : "asset.redisDetectedSentinelNode")}
          </div>
          <div className="mt-0.5 text-muted-foreground">{t("asset.redisDetectedModeHint", { addr })}</div>
        </div>
        <Button
          size="sm"
          className="h-7 shrink-0 text-xs"
          data-testid="redis-switch-mode-button"
          onClick={switchToDetectedMode}
        >
          {t(detected === "cluster" ? "asset.redisSwitchToClusterMode" : "asset.redisSwitchToSentinelMode")}
        </Button>
      </div>
    );
  };

  const groups: ConfigGroupSchema<RedisFormState>[] = [
    {
      key: "connection",
      label: "asset.tabConnection",
      fields: [
        {
          kind: "segmented",
          key: "mode",
          label: "asset.redisDeployMode",
          options: [
            { value: "standalone", label: "asset.redisModeStandalone", testid: "redis-mode-standalone" },
            { value: "cluster", label: "asset.redisModeCluster", testid: "redis-mode-cluster" },
            { value: "sentinel", label: "asset.redisModeSentinel", testid: "redis-mode-sentinel" },
          ],
        },
        {
          kind: "row",
          visibleWhen: (s) => s.mode === "standalone",
          fields: [
            {
              kind: "text",
              key: "host",
              label: "asset.host",
              required: true,
              placeholder: "example.com",
              width: "flex-1",
              testid: "redis-host-input",
            },
            {
              kind: "number",
              key: "port",
              label: "asset.port",
              placeholder: "6379",
              width: "w-[110px] shrink-0",
              blankWhenZero: true,
              testid: "redis-port-input",
            },
          ],
        },
        { kind: "custom", visibleWhen: (s) => s.mode === "standalone", render: (s) => mismatchBanner(s) },
        {
          kind: "textarea",
          key: "nodes",
          label: "asset.redisSeedNodes",
          visibleWhen: (s) => s.mode === "cluster",
          required: true,
          mono: true,
          rows: 3,
          placeholder: "10.0.0.1:6379\n10.0.0.2:6379",
          hint: "asset.redisSeedNodesHint",
          testid: "redis-nodes-textarea",
        },
        {
          kind: "custom",
          visibleWhen: (s) => s.mode === "sentinel",
          render: (s, patchFn) => {
            const otherSentinels = lastProbe?.sentinel?.otherSentinels ?? [];
            const current = parseRedisNodes(s.nodes);
            const missing = otherSentinels.filter((addr) => !current.includes(addr));
            return (
              <Field label={t("asset.redisSentinelNodes")} required>
                <Textarea
                  data-testid="redis-nodes-textarea"
                  rows={3}
                  className="font-mono text-sm"
                  value={s.nodes}
                  placeholder="10.0.0.31:26379"
                  onChange={(e) => patchFn({ nodes: e.target.value })}
                  onBlur={readSentinelGroups}
                />
                {missing.length > 0 && (
                  <div className="flex items-center gap-2 text-xs">
                    <span className="text-muted-foreground">
                      {t("asset.redisDiscoverOtherSentinels", { count: missing.length, addrs: missing.join("、") })}
                    </span>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-6 px-1.5 text-xs text-primary"
                      data-testid="redis-complete-sentinels-button"
                      onClick={completeSentinels}
                    >
                      {t("asset.redisCompleteSentinels")}
                    </Button>
                  </div>
                )}
              </Field>
            );
          },
        },
        {
          kind: "custom",
          visibleWhen: (s) => s.mode === "sentinel",
          render: (s, patchFn) => (
            <Field label={t("asset.redisMasterName")} required>
              <div className="flex gap-2">
                <Input
                  data-testid="redis-master-name-input"
                  className="flex-1 font-mono"
                  value={s.masterName}
                  onChange={(e) => patchFn({ masterName: e.target.value })}
                />
                <Button
                  type="button"
                  variant="outline"
                  className="shrink-0 gap-1.5"
                  data-testid="redis-read-sentinel-button"
                  disabled={readingSentinel || parseRedisNodes(s.nodes).length === 0}
                  onClick={readSentinelGroups}
                >
                  <RefreshCw className={cn("size-3.5", readingSentinel && "animate-spin")} />
                  {t("asset.redisReadFromSentinel")}
                </Button>
              </div>
              {sentinelGroups.length > 1 && (
                <div className="rounded-md border bg-popover p-1 text-xs shadow-sm">
                  {sentinelGroups.map((g) => (
                    <button
                      type="button"
                      key={g.name}
                      onClick={() => patchFn({ masterName: g.name })}
                      className={cn(
                        "flex h-8 w-full items-center justify-between rounded-sm px-2 text-left",
                        g.name === s.masterName ? "bg-accent" : "hover:bg-accent"
                      )}
                    >
                      <span className="font-mono">{g.name}</span>
                      <span className="font-mono text-muted-foreground">
                        {g.masterAddr} · {t("asset.redisSentinelGroupReplicas", { count: g.replicas })}
                      </span>
                    </button>
                  ))}
                </div>
              )}
              <p className="text-xs text-muted-foreground">{t("asset.redisMasterNameHint")}</p>
            </Field>
          ),
        },
        { kind: "text", key: "username", label: "asset.username", visibleWhen: (s) => s.mode !== "sentinel" },
        {
          kind: "text",
          key: "username",
          label: "asset.redisDataNodeUsername",
          visibleWhen: (s) => s.mode === "sentinel",
        },
        { kind: "password" },
        {
          kind: "custom",
          visibleWhen: (s) => s.mode === "sentinel",
          render: (s, patchFn) => (
            <div
              className={cn(
                "grid gap-3 rounded-md border border-dashed p-3 transition-colors",
                sentinelAuthFlash && "border-warning bg-warning/5"
              )}
            >
              <div className="text-[11px] font-medium tracking-[0.3px] text-muted-foreground">
                {t("asset.redisSentinelAuthTitle")}
              </div>
              <div className="flex gap-3">
                <Field label={t("asset.redisSentinelUsername")} className="flex-1">
                  <Input
                    data-testid="redis-sentinel-username-input"
                    value={s.sentinelUsername}
                    onChange={(e) => patchFn({ sentinelUsername: e.target.value })}
                  />
                </Field>
                <Field label={t("asset.redisSentinelPassword")} className="flex-1">
                  <SecretInput
                    ref={sentinelPasswordRef}
                    data-testid="redis-sentinel-password-input"
                    value={s.sentinelPassword}
                    onChange={(e) => {
                      setSentinelAuthFlash(false);
                      patchFn({ sentinelPassword: e.target.value });
                    }}
                    placeholder={s.encryptedSentinelPassword ? t("asset.passwordUnchanged") : ""}
                  />
                </Field>
              </div>
              {sentinelAuthFlash && <p className="text-xs text-warning">{t("asset.redisSentinelAuthRequired")}</p>}
            </div>
          ),
        },
        {
          kind: "number",
          key: "database",
          label: "asset.redisDatabase",
          min: 0,
          visibleWhen: (s) => s.mode !== "cluster",
        },
        {
          kind: "custom",
          visibleWhen: (s) => s.mode === "cluster",
          render: () => (
            <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <Info className="size-3.5" />
              {t("asset.redisClusterDbHint")}
            </p>
          ),
        },
      ],
    },
    { key: "tunnel", label: "asset.tabTunnel", fields: [{ kind: "tunnel" }] },
    {
      key: "tls",
      label: "asset.tabTls",
      fields: [
        { kind: "switch", key: "tls", label: "asset.tls" },
        { kind: "switch", key: "tlsInsecure", label: "asset.redisTlsInsecure", visibleWhen: (s) => s.tls },
        {
          kind: "text",
          key: "tlsServerName",
          label: "asset.redisTlsServerName",
          placeholder: "redis.example.com",
          visibleWhen: (s) => s.tls,
        },
        {
          kind: "text",
          key: "tlsCAFile",
          label: "asset.redisTlsCAFile",
          placeholder: "/path/to/ca.pem",
          visibleWhen: (s) => s.tls,
        },
        {
          kind: "text",
          key: "tlsCertFile",
          label: "asset.redisTlsCertFile",
          placeholder: "/path/to/client.crt",
          visibleWhen: (s) => s.tls,
        },
        {
          kind: "text",
          key: "tlsKeyFile",
          label: "asset.redisTlsKeyFile",
          placeholder: "/path/to/client.key",
          visibleWhen: (s) => s.tls,
        },
      ],
    },
    {
      key: "advanced",
      label: "asset.tabAdvanced",
      fields: [
        {
          kind: "row",
          fields: [
            {
              kind: "number",
              key: "commandTimeoutSeconds",
              label: "asset.redisCommandTimeout",
              min: 0,
              width: "flex-1",
            },
            { kind: "number", key: "scanPageSize", label: "asset.redisScanPageSize", min: 0, width: "flex-1" },
          ],
        },
        { kind: "text", key: "keySeparator", label: "asset.redisKeySeparator", placeholder: ":" },
        {
          kind: "custom",
          visibleWhen: (s) => s.mode !== "standalone",
          render: (s, patchFn) => {
            const unreachable = lastProbe?.cluster?.unreachableNodes ?? [];
            const listed = listedMappingAnnouncedAddresses(s.nodeAddressMap);
            const missingMapped = unreachable.filter((addr) => !listed.has(addr));
            const mapResult = parseNodeAddressMap(s.nodeAddressMap);
            return (
              <Field label={t("asset.redisNodeAddressMap")}>
                {unreachable.length > 0 && (
                  <div className="flex items-start gap-2 rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-xs text-warning">
                    <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
                    <div className="min-w-0 flex-1">
                      <div className="font-medium">
                        {t("asset.redisUnreachableNodesWarning", {
                          count: unreachable.length,
                          addrs: unreachable.join("、"),
                        })}
                      </div>
                      {missingMapped.length > 0 && (
                        <Button
                          variant="ghost"
                          size="sm"
                          className="mt-1 h-6 px-1.5 text-xs"
                          data-testid="redis-generate-mapping-button"
                          onClick={generateMapping}
                        >
                          {t("asset.redisGenerateMapping")}
                        </Button>
                      )}
                    </div>
                  </div>
                )}
                <Textarea
                  data-testid="redis-node-address-map-textarea"
                  rows={4}
                  className="font-mono text-sm"
                  value={s.nodeAddressMap}
                  placeholder="172.18.0.11:6379 = 10.20.0.5:7001"
                  onChange={(e) => patchFn({ nodeAddressMap: e.target.value })}
                />
                {mapResult.error && (
                  <p className="text-xs text-destructive">{t(mapResult.error.key, { line: mapResult.error.line })}</p>
                )}
                <p className="text-xs text-muted-foreground">{t("asset.redisNodeAddressMapHint")}</p>
              </Field>
            );
          },
        },
      ],
    },
  ];

  return <ConfigTabs groups={buildConfigGroups(groups, { state, patch, ctx: { cred, editAsset } })} />;
}
