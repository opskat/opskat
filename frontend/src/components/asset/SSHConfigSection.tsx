import { useEffect, useMemo, useRef, useState } from "react";
import { AlertTriangle, FolderOpen, Loader2, Lock, Trash2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import {
  Button,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@opskat/ui";
import { Field, Segmented } from "@/components/asset/fields";
import { SecretInput } from "@/components/SecretInput";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import {
  GetSSHConnectionSettings,
  InspectAgentSource,
  ListAgentSources,
  ListCredentialsByType,
} from "../../../wailsjs/go/system/System";
import { ListLocalSSHKeys, SelectSSHKeyFile } from "../../../wailsjs/go/ssh/SSH";
import {
  credential_entity,
  ssh as ssh_models,
  ssh_agent_svc,
  system as system_models,
} from "../../../wailsjs/go/models";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import {
  buildSSHConfig,
  parseSSHConfig,
  parseSSHPasswordCredentialConfig,
  SSH_DEFAULTS,
  type SSHFormState,
} from "./SSHConfigSection.config";
import { proxyChainValidationKey, resolveSaveProxyChainSecrets } from "./proxyConfig";
import type { ConfigSectionProps } from "@/lib/assetTypes/formContract";

const DEFAULT_GLOBAL_KEEPALIVE_SECONDS = 30;

export function SSHConfigSection({ editAsset, onValidityChange, ref }: ConfigSectionProps) {
  const { t } = useTranslation();
  // password-auth 凭据复用 db 族抽象;key-auth ssh_key 凭据 + 本地密钥由本 section 自持。
  const passwordCredentialConfig = useMemo(
    () => (editAsset ? parseSSHPasswordCredentialConfig(editAsset.Config) : undefined),
    [editAsset]
  );
  const cred = useAssetCredential(editAsset, passwordCredentialConfig);

  const { state, setState, patch } = useConfigSection<SSHFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) =>
      a
        ? parseSSHConfig(a.Config, a.sshTunnelId || 0)
        : { ...SSH_DEFAULTS, keepAliveIntervalSeconds: DEFAULT_GLOBAL_KEEPALIVE_SECONDS },
    validate: (s) => {
      const ok = !!s.host.trim();
      const proxyChainError = proxyChainValidationKey(s.proxyChainLayers);
      // agent-auth:未选来源 / 未选指纹 / 已存指纹当前缺失 → 禁止保存;绝不推断替代项。
      let agentError = "";
      if (s.authType === "agent") {
        if (s.agentMissingFingerprint) agentError = "asset.agentKeyUnavailable";
        else if (s.agentSourceId <= 0) agentError = "asset.agentSourceRequired";
        else if (!s.agentKeyFingerprint) agentError = "asset.agentKeyRequired";
      }
      const canUse = ok && !proxyChainError && !agentError;
      return {
        canTest: canUse,
        canSave: canUse,
        saveDisabledReason: !ok ? "asset.formMissingHost" : proxyChainError ? proxyChainError : agentError,
      };
    },
    build: async (s, ctx) => {
      // password-auth 凭据加密;passphrase / proxy 密码:明文优先加密,否则沿用既有密文。
      const passwordCred = await resolveSaveCredential(cred.value, ctx.encryptPassword);
      const passphrase = s.privateKeyPassphrase
        ? await ctx.encryptPassword(s.privateKeyPassphrase)
        : s.encryptedPrivateKeyPassphrase;
      const proxyPassword = s.proxyPassword ? await ctx.encryptPassword(s.proxyPassword) : s.encryptedProxyPassword;
      const proxyChainSecrets = await resolveSaveProxyChainSecrets(s.proxyChainLayers, ctx.encryptPassword);
      return {
        configJSON: buildSSHConfig(s, {
          passwordCred,
          keyCredentialId: s.credentialId,
          passphrase,
          proxyPassword,
          proxyChainSecrets,
          includeJumpHost: false, // save:隧道写 asset 顶层 sshTunnelId,不入 config.jump_host_id
        }),
        sshTunnelId: s.connectionType === "jumphost" && s.sshTunnelId > 0 ? s.sshTunnelId : 0,
      };
    },
    buildTest: async (s) => ({
      assetType: "ssh",
      // 测试:passphrase / proxy 用明文(passphrase 缺明文时沿用既有密文;proxy 仅明文),后端从 config.jump_host_id 读隧道。
      configJSON: buildSSHConfig(s, {
        passwordCred: resolveTestCredential(cred.value),
        keyCredentialId: s.credentialId,
        passphrase: s.privateKeyPassphrase || s.encryptedPrivateKeyPassphrase,
        proxyPassword: s.proxyPassword,
        proxyChainSecrets: Object.fromEntries(
          s.proxyChainLayers.map((layer) => [layer.id, { password: layer.password, token: layer.token }])
        ),
        includeJumpHost: true,
      }),
      password: cred.value.password,
    }),
    deps: [cred.value],
  });

  const [managedKeys, setManagedKeys] = useState<credential_entity.Credential[]>([]);
  const [localKeys, setLocalKeys] = useState<ssh_models.LocalSSHKeyInfo[]>([]);
  // SSH Agent 表单:来源列表 / 选中来源的身份摘要(有界,随来源变更重载并清除)。
  const [agentSources, setAgentSources] = useState<system_models.AgentSourceSummary[]>([]);
  const [agentSourceLoading, setAgentSourceLoading] = useState(true);
  const [agentIdentities, setAgentIdentities] = useState<ssh_agent_svc.IdentitySummary[]>([]);
  // 当前 agentIdentities 所属来源 id;与选中来源不一致即视为加载中(来源变更时旧身份不展示)。
  const [agentIdentitySource, setAgentIdentitySource] = useState(0);
  const [agentInspectError, setAgentInspectError] = useState(false);
  // state 最新快照:身份加载 effect 读"本次加载开始时的已选指纹",避免把 agentKeyFingerprint 加进 deps。
  const stateRef = useRef(state);
  useEffect(() => {
    stateRef.current = state;
  }, [state]);
  // 全局保活默认值：新建时写入此值；留空态的 placeholder 也显示它。
  const [globalKeepAlive, setGlobalKeepAlive] = useState(DEFAULT_GLOBAL_KEEPALIVE_SECONDS);
  // 用户是否手动清空过保活输入：清空后回落跟随全局，不再被异步加载的全局值覆盖。
  const keepAliveClearedRef = useRef(false);
  // 挂载即扫描,初始 true(避免在 effect 内同步 setState 触发级联渲染)。
  const [scanningKeys, setScanningKeys] = useState(true);

  // 自加载 ssh_key 凭据列表 + 扫描本地密钥(镜像旧壳 open 时的合并 load)。
  useEffect(() => {
    ListCredentialsByType("ssh_key")
      .then((keys) => setManagedKeys(keys || []))
      .catch(() => setManagedKeys([]));
    ListLocalSSHKeys()
      .then((keys) => setLocalKeys(keys || []))
      .catch(() => setLocalKeys([]))
      .finally(() => setScanningKeys(false));
    GetSSHConnectionSettings()
      .then((s) => {
        const seconds = s?.keepAliveIntervalSeconds;
        if (!seconds || seconds <= 0) return;
        setGlobalKeepAlive(seconds);
        if (editAsset) return;
        setState((prev) =>
          prev.keepAliveIntervalSeconds === DEFAULT_GLOBAL_KEEPALIVE_SECONDS && !keepAliveClearedRef.current
            ? { ...prev, keepAliveIntervalSeconds: seconds }
            : prev
        );
      })
      .catch(() => {});
  }, [editAsset, setState]);

  // 排除自身,不能把自己选作跳板机 / SSH 隧道。
  const jumpHostExcludeIds = editAsset?.ID ? [editAsset.ID] : undefined;

  // Agent 来源列表(挂载一次);新建表单绝不自动选择来源。初始 loading=true,仅在回调中解除。
  useEffect(() => {
    ListAgentSources()
      .then((sources) => setAgentSources(sources || []))
      .catch(() => setAgentSources([]))
      .finally(() => setAgentSourceLoading(false));
  }, []);

  // 选择来源后加载其身份;编辑态对已存指纹做可用性核对(缺失 → 单独只读展示 + 禁止保存)。
  // 加载中 = agentIdentitySource 与选中来源不一致(来源变更即隐藏旧身份),不在 effect 内同步 setState。
  useEffect(() => {
    if (state.authType !== "agent" || state.agentSourceId <= 0) return;
    const sourceId = state.agentSourceId;
    const fpToVerify = stateRef.current.agentKeyFingerprint;
    let cancelled = false;
    InspectAgentSource(sourceId)
      .then((res) => {
        if (cancelled) return;
        const identities = res?.identities || [];
        setAgentIdentities(identities);
        setAgentIdentitySource(sourceId);
        setAgentInspectError(false);
        if (fpToVerify && !identities.some((i) => i.fingerprint === fpToVerify)) {
          // 已存指纹当前不存在:单独只读展示、不入可选列表、不推断替代,保持禁止保存。
          patch({ agentKeyFingerprint: "", agentMissingFingerprint: fpToVerify });
        } else if (fpToVerify) {
          patch({ agentMissingFingerprint: "" });
        }
      })
      .catch(() => {
        if (cancelled) return;
        // 来源不可达:标记已尝试(结束加载态),保持已选值不变(不算"缺失")。
        setAgentIdentitySource(sourceId);
        setAgentInspectError(true);
      });
    return () => {
      cancelled = true;
    };
    // 身份加载仅依赖来源选择;已选指纹经 stateRef 读最新态(选择密钥不触发重载)。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.authType, state.agentSourceId]);

  const groups: ConfigGroupSchema<SSHFormState>[] = [
    {
      key: "connection",
      label: "asset.tabConnection",
      fields: [
        {
          kind: "row",
          fields: [
            {
              kind: "text",
              key: "host",
              label: "asset.host",
              required: true,
              placeholder: "example.com",
              width: "flex-1",
              testid: "ssh-host-input",
            },
            {
              kind: "number",
              key: "port",
              label: "asset.port",
              width: "w-[110px] shrink-0",
              blankWhenZero: true,
              placeholder: "22",
              testid: "ssh-port-input",
            },
          ],
        },
        {
          kind: "row",
          fields: [
            { kind: "text", key: "username", label: "asset.username", width: "flex-1", testid: "ssh-username-input" },
            {
              kind: "segmented",
              key: "authType",
              label: "asset.authType",
              width: "w-[190px] shrink-0",
              options: [
                { value: "password", label: "asset.authPassword", testid: "ssh-auth-type-option-password" },
                { value: "key", label: "asset.authKey", testid: "ssh-auth-type-option-key" },
                { value: "agent", label: "asset.authAgentOption", testid: "ssh-auth-type-option-agent" },
              ],
            },
          ],
        },
        { kind: "password", placeholder: "asset.passwordPlaceholder", visibleWhen: (s) => s.authType === "password" },
        {
          kind: "custom",
          visibleWhen: (s) => s.authType === "key",
          render: () => (
            /* ↓↓↓ 逐字搬入:当前 SSHConfigSection.tsx 中 {state.authType === "key" && ( ... )} 内的整个
                   <div className="flex flex-col gap-4"> ... </div>,原样不改 ↓↓↓ */
            <div className="flex flex-col gap-4">
              <Field label={t("asset.keySource")}>
                <Segmented
                  value={state.keySource}
                  onChange={(v) => patch({ keySource: v as "managed" | "file" })}
                  aria-label={t("asset.keySource")}
                  options={[
                    { value: "managed", label: t("asset.keySourceManaged"), testid: "ssh-key-source-option-managed" },
                    { value: "file", label: t("asset.keySourceFile"), testid: "ssh-key-source-option-file" },
                  ]}
                />
              </Field>

              {state.keySource === "managed" && (
                <Field label={t("asset.selectKey")}>
                  {managedKeys.length > 0 ? (
                    <Select
                      value={String(state.credentialId)}
                      onValueChange={(v) => {
                        const id = Number(v);
                        if (id !== 0) {
                          const credKey = managedKeys.find((k) => k.id === id);
                          if (credKey && credKey.username) {
                            patch({ credentialId: id, username: credKey.username });
                            return;
                          }
                        }
                        patch({ credentialId: id });
                      }}
                    >
                      <SelectTrigger className="w-full">
                        <SelectValue placeholder={t("asset.selectKeyPlaceholder")} />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="0">{t("asset.selectKeyPlaceholder")}</SelectItem>
                        {managedKeys.map((k) => (
                          <SelectItem key={k.id} value={String(k.id)}>
                            {k.name}
                            {k.username ? ` (${k.username})` : ""} ({(k.keyType || "").toUpperCase()})
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  ) : (
                    <p className="text-xs text-muted-foreground">{t("asset.noManagedKeys")}</p>
                  )}
                </Field>
              )}

              {state.keySource === "file" && (
                <Field label={t("asset.discoveredKeys")}>
                  {scanningKeys ? (
                    <div className="flex items-center gap-2 text-xs text-muted-foreground py-1">
                      <Loader2 className="h-3 w-3 animate-spin" />
                      {t("asset.scanningKeys")}
                    </div>
                  ) : localKeys.length > 0 ? (
                    <div className="grid gap-1.5">
                      {localKeys.map((k) => {
                        const selected = state.selectedKeyPaths.includes(k.path);
                        return (
                          <label
                            key={k.path}
                            data-testid={`ssh-local-key-${k.path.split("/").pop() || "key"}`}
                            className="flex items-center gap-2 text-xs cursor-pointer hover:bg-accent rounded px-2 py-1.5"
                          >
                            <input
                              type="checkbox"
                              checked={selected}
                              onChange={() => {
                                if (selected) {
                                  patch({ selectedKeyPaths: state.selectedKeyPaths.filter((p) => p !== k.path) });
                                } else {
                                  patch({ selectedKeyPaths: [...state.selectedKeyPaths, k.path] });
                                }
                              }}
                              className="rounded"
                            />
                            {k.isEncrypted && (
                              <Tooltip>
                                <TooltipTrigger asChild>
                                  <Lock className="h-3 w-3 text-warning" />
                                </TooltipTrigger>
                                <TooltipContent>{t("asset.keyEncrypted")}</TooltipContent>
                              </Tooltip>
                            )}
                            <span className="font-medium truncate">{k.path.split("/").pop()}</span>
                            <span className="text-muted-foreground">({k.keyType})</span>
                            {k.fingerprint && (
                              <span className="text-muted-foreground truncate ml-auto" title={k.fingerprint}>
                                {k.fingerprint.substring(0, 20)}...
                              </span>
                            )}
                          </label>
                        );
                      })}
                    </div>
                  ) : (
                    <p className="text-xs text-muted-foreground">{t("asset.noLocalKeys")}</p>
                  )}

                  {state.selectedKeyPaths
                    .filter((p) => !localKeys.some((k) => k.path === p))
                    .map((path) => (
                      <div key={path} className="flex items-center gap-2 text-xs px-2 py-1.5 bg-accent rounded">
                        <span className="truncate flex-1">{path}</span>
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          className="h-5 w-5 shrink-0"
                          onClick={() =>
                            patch({ selectedKeyPaths: state.selectedKeyPaths.filter((p2) => p2 !== path) })
                          }
                        >
                          <Trash2 className="h-3 w-3" />
                        </Button>
                      </div>
                    ))}

                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="w-full mt-1"
                    onClick={async () => {
                      try {
                        const info = await SelectSSHKeyFile();
                        if (info && !state.selectedKeyPaths.includes(info.path)) {
                          patch({ selectedKeyPaths: [...state.selectedKeyPaths, info.path] });
                          if (!localKeys.some((k) => k.path === info.path)) {
                            setLocalKeys([...localKeys, info]);
                          }
                        }
                      } catch (e) {
                        toast.error(String(e));
                      }
                    }}
                  >
                    <FolderOpen className="h-3.5 w-3.5 mr-1.5" />
                    {t("asset.browseKeyFile")}
                  </Button>

                  {/* Passphrase for local key file */}
                  {state.selectedKeyPaths.length > 0 && (
                    <Field label={t("sshKey.passphrase")} className="mt-1">
                      <SecretInput
                        value={state.privateKeyPassphrase}
                        onChange={(e) => patch({ privateKeyPassphrase: e.target.value })}
                        placeholder={t("sshKey.passphrasePlaceholder")}
                      />
                    </Field>
                  )}
                </Field>
              )}
            </div>
          ),
        },
        {
          kind: "custom",
          visibleWhen: (s) => s.authType === "agent",
          render: () => (
            <div className="flex flex-col gap-4">
              <Field label={t("asset.agentSource")}>
                {agentSourceLoading ? (
                  <div className="flex items-center gap-2 text-xs text-muted-foreground py-1">
                    <Loader2 className="h-3 w-3 animate-spin" />
                    {t("asset.agentSourceLoading")}
                  </div>
                ) : agentSources.length > 0 ? (
                  <Select
                    value={state.agentSourceId > 0 ? String(state.agentSourceId) : ""}
                    onValueChange={(v) => {
                      const id = Number(v);
                      // 修改来源清除尚未保存的密钥选择;身份随新来源重新加载。
                      patch({ agentSourceId: id || 0, agentKeyFingerprint: "", agentMissingFingerprint: "" });
                    }}
                  >
                    <SelectTrigger className="w-full" data-testid="ssh-agent-source-trigger">
                      <SelectValue placeholder={t("asset.agentSourcePlaceholder")} />
                    </SelectTrigger>
                    <SelectContent>
                      {agentSources.map((src) => (
                        <SelectItem key={src.id} value={String(src.id)}>
                          {src.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                ) : (
                  <p className="text-xs text-muted-foreground">{t("asset.agentNoSources")}</p>
                )}
              </Field>

              {state.agentMissingFingerprint && (
                <div
                  data-testid="ssh-agent-key-unavailable"
                  className="rounded-md border border-destructive/40 bg-destructive/10 p-3 space-y-1"
                >
                  <div className="flex items-center gap-2 text-xs text-destructive">
                    <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
                    <span>{t("asset.agentKeyUnavailable")}</span>
                  </div>
                  <div className="font-mono text-xs break-all select-text text-foreground">
                    {state.agentMissingFingerprint}
                  </div>
                  <p className="text-[11px] leading-snug text-muted-foreground">{t("asset.agentKeyUnavailableHint")}</p>
                </div>
              )}

              <Field label={t("asset.agentSelectKey")}>
                {state.agentSourceId <= 0 ? (
                  <p className="text-xs text-muted-foreground">{t("asset.agentSelectSourceFirst")}</p>
                ) : state.agentSourceId > 0 && agentIdentitySource !== state.agentSourceId ? (
                  <div className="flex items-center gap-2 text-xs text-muted-foreground py-1">
                    <Loader2 className="h-3 w-3 animate-spin" />
                    {t("asset.agentIdentityLoading")}
                  </div>
                ) : agentInspectError ? (
                  <p className="text-xs text-muted-foreground">{t("asset.agentIdentityLoadError")}</p>
                ) : agentIdentities.length > 0 ? (
                  <Select
                    value={state.agentKeyFingerprint}
                    onValueChange={(fp) => patch({ agentKeyFingerprint: fp, agentMissingFingerprint: "" })}
                  >
                    <SelectTrigger className="w-full" data-testid="ssh-agent-key-trigger">
                      <SelectValue placeholder={t("asset.agentSelectKeyPlaceholder")} />
                    </SelectTrigger>
                    <SelectContent>
                      {agentIdentities.map((idn) => (
                        <SelectItem key={idn.fingerprint} value={idn.fingerprint}>
                          <span className="flex w-full items-center gap-1.5 min-w-0">
                            <span className="font-mono text-xs truncate">{idn.fingerprint}</span>
                            <span className="text-muted-foreground shrink-0 text-xs">{idn.type}</span>
                            <span className="text-muted-foreground truncate text-xs">
                              {idn.comment || t("agentSource.noComment")}
                            </span>
                          </span>
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                ) : (
                  <p className="text-xs text-muted-foreground">{t("asset.agentNoIdentity")}</p>
                )}
              </Field>
            </div>
          ),
        },
      ],
    },
    {
      key: "tunnel",
      label: "asset.tabTunnel",
      fields: [
        {
          kind: "tunnel",
          excludeIds: jumpHostExcludeIds,
        },
      ],
    },
    {
      key: "advanced",
      label: "asset.tabAdvanced",
      fields: [
        {
          kind: "custom",
          render: (s, patchState) => {
            // 创建态默认写入全局值；清空则回落为 0＝跟随全局(config 不写)。编辑态直读已存值。
            const shownValue = s.keepAliveIntervalSeconds > 0 ? s.keepAliveIntervalSeconds : "";
            return (
              <Field label={t("asset.sshKeepAliveInterval")}>
                <div className="flex items-center gap-2">
                  <Input
                    type="number"
                    min={5}
                    max={3600}
                    className="w-32 [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                    data-testid="ssh-keepalive-input"
                    value={shownValue}
                    placeholder={t("asset.sshKeepAliveIntervalPlaceholder", { seconds: globalKeepAlive })}
                    onChange={(e) => {
                      const v = e.target.value;
                      keepAliveClearedRef.current = v === "";
                      patchState({ keepAliveIntervalSeconds: v === "" ? 0 : Math.trunc(Number(v)) || 0 });
                    }}
                    onBlur={() => {
                      // 离焦时把非零值钳到合法区间，保证存进 config 的值有效（0 除外）。
                      const n = s.keepAliveIntervalSeconds;
                      if (n > 0 && n < 5) patchState({ keepAliveIntervalSeconds: 5 });
                      else if (n > 3600) patchState({ keepAliveIntervalSeconds: 3600 });
                    }}
                  />
                  <span className="text-sm text-muted-foreground">{t("connection.secondsUnit")}</span>
                </div>
              </Field>
            );
          },
        },
        {
          kind: "custom",
          render: (s, patchState) => (
            <Field label={t("asset.sshRestoreCwdOnReconnect")}>
              <Switch
                checked={s.restoreCwdOnReconnect}
                onCheckedChange={(v) => patchState({ restoreCwdOnReconnect: v })}
                data-testid="ssh-restore-cwd-switch"
              />
            </Field>
          ),
        },
      ],
    },
  ];

  return <ConfigTabs groups={buildConfigGroups(groups, { state, patch, ctx: { cred, editAsset } })} />;
}
