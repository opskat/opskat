import { forwardRef, useEffect, useMemo, useState } from "react";
import { Trash2, FolderOpen, Loader2, Lock } from "lucide-react";
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
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@opskat/ui";
import { Field, Segmented } from "@/components/asset/fields";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import { GetSSHConnectionSettings, ListCredentialsByType } from "../../../wailsjs/go/system/System";
import { ListLocalSSHKeys, SelectSSHKeyFile } from "../../../wailsjs/go/ssh/SSH";
import { credential_entity, ssh as ssh_models } from "../../../wailsjs/go/models";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import {
  buildSSHConfig,
  parseSSHConfig,
  parseSSHPasswordCredentialConfig,
  SSH_DEFAULTS,
  type SSHFormState,
} from "./SSHConfigSection.config";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";

export const SSHConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function SSHConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const { t } = useTranslation();
  // password-auth 凭据复用 db 族抽象;key-auth ssh_key 凭据 + 本地密钥由本 section 自持。
  const passwordCredentialConfig = useMemo(
    () => (editAsset ? parseSSHPasswordCredentialConfig(editAsset.Config) : undefined),
    [editAsset]
  );
  const cred = useAssetCredential(editAsset, passwordCredentialConfig);

  const { state, patch } = useConfigSection<SSHFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseSSHConfig(a.Config, a.sshTunnelId || 0) : { ...SSH_DEFAULTS }),
    validate: (s) => {
      const ok = !!s.host.trim();
      return { canTest: ok, canSave: ok, saveDisabledReason: ok ? "" : "asset.formMissingHost" };
    },
    build: async (s, ctx) => {
      // password-auth 凭据加密;passphrase / proxy 密码:明文优先加密,否则沿用既有密文。
      const passwordCred = await resolveSaveCredential(cred.value, ctx.encryptPassword);
      const passphrase = s.privateKeyPassphrase
        ? await ctx.encryptPassword(s.privateKeyPassphrase)
        : s.encryptedPrivateKeyPassphrase;
      const proxyPassword = s.proxyPassword ? await ctx.encryptPassword(s.proxyPassword) : s.encryptedProxyPassword;
      return {
        configJSON: buildSSHConfig(s, {
          passwordCred,
          keyCredentialId: s.credentialId,
          passphrase,
          proxyPassword,
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
        includeJumpHost: true,
      }),
      password: cred.value.password,
    }),
    deps: [cred.value],
  });

  const [managedKeys, setManagedKeys] = useState<credential_entity.Credential[]>([]);
  const [localKeys, setLocalKeys] = useState<ssh_models.LocalSSHKeyInfo[]>([]);
  // 全局保活默认值：新建时预填此值(仅显示)，也作为「高级」留空态的 placeholder。
  const [globalKeepAlive, setGlobalKeepAlive] = useState(30);
  // 用户是否手动清空过保活输入：清空后回落跟随全局(显示 placeholder)，不再预填全局数字。
  const [keepAliveCleared, setKeepAliveCleared] = useState(false);
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
      .then((s) => s && setGlobalKeepAlive(s.keepAliveIntervalSeconds))
      .catch(() => {});
  }, []);

  // 排除自身,不能把自己选作跳板机 / SSH 隧道。
  const jumpHostExcludeIds = editAsset?.ID ? [editAsset.ID] : undefined;

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
                      <Input
                        type="password"
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
      ],
    },
    {
      key: "tunnel",
      label: "asset.tabTunnel",
      fields: [
        {
          kind: "tunnel",
          tunnelOptionLabelKey: "asset.connectionJumpHost",
          tunnelSelectLabelKey: "asset.selectJumpHost",
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
            // 新建时预填全局默认值(仅显示)：未改动即保存仍存 0＝跟随全局(config 不写)；改成别的值＝
            // 固定覆盖；清空＝回落跟随全局(出 placeholder)。编辑态不预填，直读已存值(0＝跟随)。
            const prefillGlobal = !editAsset && s.keepAliveIntervalSeconds === 0 && !keepAliveCleared;
            const shownValue =
              s.keepAliveIntervalSeconds > 0 ? s.keepAliveIntervalSeconds : prefillGlobal ? globalKeepAlive : "";
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
                      setKeepAliveCleared(v === "");
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
      ],
    },
  ];

  return <ConfigTabs groups={buildConfigGroups(groups, { state, patch, ctx: { cred, editAsset } })} />;
});
