import { useTranslation } from "react-i18next";
import { Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { Field, Segmented } from "@/components/asset/fields";
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
    <div className="flex flex-col gap-4">
      {/* Connection type (own label) */}
      <Field label={t("asset.connectionType")}>
        <Segmented
          value={value.connectionType}
          onChange={(v) => onChange({ connectionType: v as ConnectionType })}
          aria-label={t("asset.connectionType")}
          options={[
            { value: "direct", label: t("asset.connectionDirect") },
            { value: "jumphost", label: t(tunnelOptionLabelKey) },
            { value: "proxy", label: t("asset.connectionProxy") },
          ]}
        />
      </Field>

      {/* Jump host / SSH tunnel selector */}
      {value.connectionType === "jumphost" && (
        <Field label={t(tunnelSelectLabelKey)}>
          <AssetSelect
            value={value.sshTunnelId}
            onValueChange={(v) => onChange({ sshTunnelId: v })}
            filterType="ssh"
            excludeIds={excludeIds}
            placeholder={t("asset.jumpHostNone")}
          />
        </Field>
      )}

      {/* Proxy config (inline, no nested border since we are already in a block) */}
      {value.connectionType === "proxy" && (
        <div className="flex flex-col gap-4">
          <div className="flex items-end gap-3">
            <Field label={t("asset.proxyType")} className="w-[120px] shrink-0">
              <Select value={value.proxyType} onValueChange={(v) => onChange({ proxyType: v })}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="socks5">SOCKS5</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field label={t("asset.proxyHost")} className="flex-1">
              <Input
                value={value.proxyHost}
                onChange={(e) => onChange({ proxyHost: e.target.value })}
                placeholder="127.0.0.1"
              />
            </Field>
            <Field label={t("asset.proxyPort")} className="w-[110px] shrink-0">
              <Input
                className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                type="number"
                value={value.proxyPort || ""}
                placeholder="1080"
                onChange={(e) => onChange({ proxyPort: Number(e.target.value) })}
              />
            </Field>
          </div>
          <div className="flex items-end gap-3">
            <Field label={t("asset.proxyUsername")} className="flex-1">
              <Input value={value.proxyUsername} onChange={(e) => onChange({ proxyUsername: e.target.value })} />
            </Field>
            <Field label={t("asset.proxyPassword")} className="flex-1">
              <Input
                type="password"
                value={value.proxyPassword}
                onChange={(e) => onChange({ proxyPassword: e.target.value })}
                placeholder={value.encryptedProxyPassword ? t("asset.passwordUnchanged") : ""}
              />
            </Field>
          </div>
        </div>
      )}
    </div>
  );
}
