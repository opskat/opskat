import { forwardRef, useEffect, useImperativeHandle, useMemo, useState } from "react";
import { Trash2, FolderOpen, Loader2, Lock } from "lucide-react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import {
  Button,
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@opskat/ui";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { PasswordSourceField } from "@/components/asset/PasswordSourceField";
import { ListCredentialsByType } from "../../../wailsjs/go/system/System";
import { ListLocalSSHKeys, SelectSSHKeyFile } from "../../../wailsjs/go/ssh/SSH";
import { credential_entity, ssh as ssh_models } from "../../../wailsjs/go/models";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import {
  buildSSHConfig,
  parseSSHConfig,
  parseSSHPasswordCredentialConfig,
  SSH_DEFAULTS,
  type SSHFormState,
} from "./SSHConfigSection.config";

export const SSHConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function SSHConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const { t } = useTranslation();
  const [state, setState] = useState<SSHFormState>(() => {
    if (!editAsset) return { ...SSH_DEFAULTS };
    // sshTunnelId 优先 asset 顶层字段(镜像旧 asset.sshTunnelId || cfg.jump_host_id || 0),
    // 并参与 connectionType 派生,故传入 parseSSHConfig。
    return parseSSHConfig(editAsset.Config, editAsset.sshTunnelId || 0);
  });
  const patch = (p: Partial<SSHFormState>) => setState((s) => ({ ...s, ...p }));
  // password-auth 凭据复用 db 族抽象;key-auth ssh_key 凭据 + 本地密钥由本 section 自持。
  const passwordCredentialConfig = useMemo(
    () => (editAsset ? parseSSHPasswordCredentialConfig(editAsset.Config) : undefined),
    [editAsset]
  );
  const cred = useAssetCredential(editAsset, passwordCredentialConfig);

  const [managedKeys, setManagedKeys] = useState<credential_entity.Credential[]>([]);
  const [localKeys, setLocalKeys] = useState<ssh_models.LocalSSHKeyInfo[]>([]);
  // 挂载即开始扫描,初始 true(避免在 effect 内同步 setState 触发级联渲染)。
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
  }, []);

  // 排除自身,不能把自己选作跳板机 / SSH 隧道。
  const jumpHostExcludeIds = editAsset?.ID ? [editAsset.ID] : undefined;

  // host 为保存/测试共同必填;上报反应式校验(onValidityChange 为壳 setState,身份稳定)。
  useEffect(() => {
    const ok = !!state.host.trim();
    onValidityChange({ canTest: ok, canSave: ok, saveDisabledReason: ok ? "" : "asset.formMissingHost" });
  }, [state.host, onValidityChange]);

  useImperativeHandle(
    ref,
    () => ({
      buildConfig: async (ctx) => {
        // password-auth 凭据加密;passphrase / proxy 密码:明文优先加密,否则沿用既有密文。
        const passwordCred = await resolveSaveCredential(cred.value, ctx.encryptPassword);
        const passphrase = state.privateKeyPassphrase
          ? await ctx.encryptPassword(state.privateKeyPassphrase)
          : state.encryptedPrivateKeyPassphrase;
        const proxyPassword = state.proxyPassword
          ? await ctx.encryptPassword(state.proxyPassword)
          : state.encryptedProxyPassword;
        const configJSON = buildSSHConfig(state, {
          passwordCred,
          keyCredentialId: state.credentialId,
          passphrase,
          proxyPassword,
          includeJumpHost: false, // save:隧道写 asset 顶层 sshTunnelId,不入 config.jump_host_id
        });
        return {
          configJSON,
          sshTunnelId: state.connectionType === "jumphost" && state.sshTunnelId > 0 ? state.sshTunnelId : 0,
        };
      },
      buildTestConfig: async () => {
        // 测试:passphrase / proxy 密码用明文(无加密),passphrase 缺明文时沿用既有密文;proxy 仅明文。
        const passwordCred = resolveTestCredential(cred.value);
        const configJSON = buildSSHConfig(state, {
          passwordCred,
          keyCredentialId: state.credentialId,
          passphrase: state.privateKeyPassphrase || state.encryptedPrivateKeyPassphrase,
          proxyPassword: state.proxyPassword,
          includeJumpHost: true, // test:后端从 config.jump_host_id 读隧道
        });
        return { assetType: "ssh", configJSON, password: cred.value.password };
      },
    }),
    [state, cred.value]
  );

  return (
    <>
      {/* SSH: Connection & Auth (single visual block) */}
      <div className="grid gap-3 border rounded-lg p-3">
        {/* Connection type (own label) */}
        <div className="grid gap-2">
          <Label>{t("asset.connectionType")}</Label>
          <Select
            value={state.connectionType}
            onValueChange={(v) => patch({ connectionType: v as "direct" | "jumphost" | "proxy" })}
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="direct">{t("asset.connectionDirect")}</SelectItem>
              <SelectItem value="jumphost">{t("asset.connectionJumpHost")}</SelectItem>
              <SelectItem value="proxy">{t("asset.connectionProxy")}</SelectItem>
            </SelectContent>
          </Select>
        </div>

        {/* Jump host selector */}
        {state.connectionType === "jumphost" && (
          <div className="grid gap-2">
            <Label>{t("asset.selectJumpHost")}</Label>
            <AssetSelect
              value={state.sshTunnelId}
              onValueChange={(v) => patch({ sshTunnelId: v })}
              filterType="ssh"
              excludeIds={jumpHostExcludeIds}
              placeholder={t("asset.jumpHostNone")}
            />
          </div>
        )}

        {/* Proxy config (inline, no nested border since we are already in a block) */}
        {state.connectionType === "proxy" && (
          <div className="grid gap-2">
            <div className="grid grid-cols-3 gap-2">
              <div className="grid gap-1">
                <Label className="text-xs">{t("asset.proxyType")}</Label>
                <Select value={state.proxyType} onValueChange={(v) => patch({ proxyType: v })}>
                  <SelectTrigger className="h-8 text-xs">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="socks5">SOCKS5</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="grid gap-1">
                <Label className="text-xs">{t("asset.proxyHost")}</Label>
                <Input
                  className="h-8 text-xs"
                  value={state.proxyHost}
                  onChange={(e) => patch({ proxyHost: e.target.value })}
                  placeholder="127.0.0.1"
                />
              </div>
              <div className="grid gap-1">
                <Label className="text-xs">{t("asset.proxyPort")}</Label>
                <Input
                  className="h-8 text-xs [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                  type="number"
                  value={state.proxyPort || ""}
                  placeholder="1080"
                  onChange={(e) => patch({ proxyPort: Number(e.target.value) })}
                />
              </div>
            </div>
            <div className="grid grid-cols-2 gap-2">
              <div className="grid gap-1">
                <Label className="text-xs">{t("asset.proxyUsername")}</Label>
                <Input
                  className="h-8 text-xs"
                  value={state.proxyUsername}
                  onChange={(e) => patch({ proxyUsername: e.target.value })}
                />
              </div>
              <div className="grid gap-1">
                <Label className="text-xs">{t("asset.proxyPassword")}</Label>
                <Input
                  className="h-8 text-xs"
                  type="password"
                  value={state.proxyPassword}
                  onChange={(e) => patch({ proxyPassword: e.target.value })}
                  placeholder={state.encryptedProxyPassword ? t("asset.passwordUnchanged") : ""}
                />
              </div>
            </div>
          </div>
        )}

        {/* Host + Port (each labeled) */}
        <div className="grid grid-cols-[1fr_120px] gap-3">
          <div className="grid gap-2">
            <Label>{t("asset.host")}</Label>
            <Input value={state.host} onChange={(e) => patch({ host: e.target.value })} placeholder="example.com" />
          </div>
          <div className="grid gap-2">
            <Label>{t("asset.port")}</Label>
            <Input
              className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
              type="number"
              value={state.port || ""}
              placeholder="22"
              onChange={(e) => patch({ port: Number(e.target.value) })}
            />
          </div>
        </div>

        {/* Username + Auth type */}
        <div className="grid grid-cols-2 gap-3">
          <div className="grid gap-2">
            <Label>{t("asset.username")}</Label>
            <Input value={state.username} onChange={(e) => patch({ username: e.target.value })} />
          </div>
          <div className="grid gap-2">
            <Label>{t("asset.authType")}</Label>
            <Select value={state.authType} onValueChange={(v) => patch({ authType: v })}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="password">{t("asset.authPassword")}</SelectItem>
                <SelectItem value="key">{t("asset.authKey")}</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>

        {/* Password (when auth_type=password) */}
        {state.authType === "password" && (
          <PasswordSourceField
            source={cred.value.passwordSource}
            onSourceChange={cred.setPasswordSource}
            password={cred.value.password}
            onPasswordChange={cred.setPassword}
            credentialId={cred.value.passwordCredentialId}
            onCredentialIdChange={cred.setPasswordCredentialId}
            managedPasswords={cred.managedPasswords}
            placeholder={t("asset.passwordPlaceholder")}
            hasExistingPassword={!!cred.value.encryptedPassword}
            editAssetId={editAsset?.ID}
            onUsernameChange={(v) => patch({ username: v })}
          />
        )}

        {/* Key config (inline, no nested border since we are already in a block) */}
        {state.authType === "key" && (
          <div className="grid gap-3">
            <div className="grid gap-2">
              <Label>{t("asset.keySource")}</Label>
              <Select value={state.keySource} onValueChange={(v) => patch({ keySource: v as "managed" | "file" })}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="managed">{t("asset.keySourceManaged")}</SelectItem>
                  <SelectItem value="file">{t("asset.keySourceFile")}</SelectItem>
                </SelectContent>
              </Select>
            </div>

            {state.keySource === "managed" && (
              <div className="grid gap-2">
                <Label>{t("asset.selectKey")}</Label>
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
                    <SelectTrigger>
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
              </div>
            )}

            {state.keySource === "file" && (
              <div className="grid gap-2">
                <Label>{t("asset.discoveredKeys")}</Label>
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
                                <Lock className="h-3 w-3 text-amber-500" />
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
                        onClick={() => patch({ selectedKeyPaths: state.selectedKeyPaths.filter((p2) => p2 !== path) })}
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
                  <div className="grid gap-1.5 mt-2">
                    <Label className="text-xs">{t("sshKey.passphrase")}</Label>
                    <Input
                      type="password"
                      className="h-8 text-xs"
                      value={state.privateKeyPassphrase}
                      onChange={(e) => patch({ privateKeyPassphrase: e.target.value })}
                      placeholder={t("sshKey.passphrasePlaceholder")}
                    />
                  </div>
                )}
              </div>
            )}
          </div>
        )}
      </div>
    </>
  );
});
