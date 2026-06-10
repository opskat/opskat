import { useTranslation } from "react-i18next";
import { Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";
import { AssetSelect } from "@/components/asset/AssetSelect";
import type { ConnectionFormFields, ConnectionType } from "./proxyConfig";

interface ConnectionMethodFieldsProps {
  value: ConnectionFormFields;
  onChange: (patch: Partial<ConnectionFormFields>) => void;
  /** 排除可选 SSH 资产(如自身),不能把自己选作跳板机/隧道。 */
  excludeIds?: number[];
  /** 隧道选项文案 key:SSH 表单用 "asset.connectionJumpHost",数据库族默认 "asset.sshTunnel"。 */
  tunnelOptionLabelKey?: string;
  /** 隧道选择器 Label 文案 key:SSH 表单用 "asset.selectJumpHost",数据库族默认 "asset.sshTunnel"。 */
  tunnelSelectLabelKey?: string;
}

/** 连接方式选择(直连 / SSH 隧道 / SOCKS5 代理)+ 对应的条件字段,SSH 与数据库族共用。 */
export function ConnectionMethodFields({
  value,
  onChange,
  excludeIds,
  tunnelOptionLabelKey = "asset.sshTunnel",
  tunnelSelectLabelKey = "asset.sshTunnel",
}: ConnectionMethodFieldsProps) {
  const { t } = useTranslation();
  return (
    <>
      {/* Connection type (own label) */}
      <div className="grid gap-2">
        <Label>{t("asset.connectionType")}</Label>
        <Select value={value.connectionType} onValueChange={(v) => onChange({ connectionType: v as ConnectionType })}>
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="direct">{t("asset.connectionDirect")}</SelectItem>
            <SelectItem value="jumphost">{t(tunnelOptionLabelKey)}</SelectItem>
            <SelectItem value="proxy">{t("asset.connectionProxy")}</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {/* Jump host / SSH tunnel selector */}
      {value.connectionType === "jumphost" && (
        <div className="grid gap-2">
          <Label>{t(tunnelSelectLabelKey)}</Label>
          <AssetSelect
            value={value.sshTunnelId}
            onValueChange={(v) => onChange({ sshTunnelId: v })}
            filterType="ssh"
            excludeIds={excludeIds}
            placeholder={t("asset.jumpHostNone")}
          />
        </div>
      )}

      {/* Proxy config (inline, no nested border since we are already in a block) */}
      {value.connectionType === "proxy" && (
        <div className="grid gap-2">
          <div className="grid grid-cols-3 gap-2">
            <div className="grid gap-1">
              <Label className="text-xs">{t("asset.proxyType")}</Label>
              <Select value={value.proxyType} onValueChange={(v) => onChange({ proxyType: v })}>
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
                value={value.proxyHost}
                onChange={(e) => onChange({ proxyHost: e.target.value })}
                placeholder="127.0.0.1"
              />
            </div>
            <div className="grid gap-1">
              <Label className="text-xs">{t("asset.proxyPort")}</Label>
              <Input
                className="h-8 text-xs [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                type="number"
                value={value.proxyPort || ""}
                placeholder="1080"
                onChange={(e) => onChange({ proxyPort: Number(e.target.value) })}
              />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-2">
            <div className="grid gap-1">
              <Label className="text-xs">{t("asset.proxyUsername")}</Label>
              <Input
                className="h-8 text-xs"
                value={value.proxyUsername}
                onChange={(e) => onChange({ proxyUsername: e.target.value })}
              />
            </div>
            <div className="grid gap-1">
              <Label className="text-xs">{t("asset.proxyPassword")}</Label>
              <Input
                className="h-8 text-xs"
                type="password"
                value={value.proxyPassword}
                onChange={(e) => onChange({ proxyPassword: e.target.value })}
                placeholder={value.encryptedProxyPassword ? t("asset.passwordUnchanged") : ""}
              />
            </div>
          </div>
        </div>
      )}
    </>
  );
}
