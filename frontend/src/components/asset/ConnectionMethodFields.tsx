import { useTranslation } from "react-i18next";
import { useMemo, useState } from "react";
import { ArrowDown, ArrowUp, Copy, GripVertical, Plus, Trash2 } from "lucide-react";
import { Button, Checkbox, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { Field, Segmented } from "@/components/asset/fields";
import {
  httpTunnelProxyLayer,
  socks5ProxyLayer,
  sshProxyLayer,
  type ConnectionFormFields,
  type ProxyChainLayerForm,
} from "./proxyConfig";

interface ConnectionMethodFieldsProps {
  value: ConnectionFormFields;
  onChange: (patch: Partial<ConnectionFormFields>) => void;
  /** 排除可选 SSH 资产(如自身),不能把自己选作跳板机/隧道。 */
  excludeIds?: number[];
  /** 兼容旧三段式连接方式字段；新 UI 统一显示为“隧道/代理”。 */
  tunnelOptionLabelKey?: string;
  /** 兼容旧三段式连接方式字段；新 UI 不再单独渲染 SSH 隧道选择器。 */
  tunnelSelectLabelKey?: string;
}

/** 连接方式与代理链配置,SSH 与数据库族共用。 */
export function ConnectionMethodFields({ value, onChange, excludeIds }: ConnectionMethodFieldsProps) {
  const { t } = useTranslation();
  const layers = useMemo(() => value.proxyChainLayers || [], [value.proxyChainLayers]);
  const isChainMode = value.connectionType !== "direct";
  const [selectedLayerIdValue, setSelectedLayerId] = useState("");
  const selectedLayerId = layers.some((layer) => layer.id === selectedLayerIdValue)
    ? selectedLayerIdValue
    : layers[0]?.id || "";
  const updateLayers = (next: ProxyChainLayerForm[]) => onChange({ proxyChainLayers: next });
  const patchLayer = (id: string, patch: Partial<ProxyChainLayerForm>) =>
    updateLayers(layers.map((layer) => (layer.id === id ? { ...layer, ...patch } : layer)));
  const moveLayer = (index: number, direction: -1 | 1) => {
    const nextIndex = index + direction;
    if (nextIndex < 0 || nextIndex >= layers.length) return;
    const next = [...layers];
    [next[index], next[nextIndex]] = [next[nextIndex], next[index]];
    updateLayers(next);
  };
  const duplicateLayer = (layer: ProxyChainLayerForm) => {
    const existingIds = new Set(layers.map((item) => item.id));
    let copyIndex = layers.length + 1;
    let nextId = `${layer.type}-copy-${copyIndex}`;
    while (existingIds.has(nextId)) {
      copyIndex += 1;
      nextId = `${layer.type}-copy-${copyIndex}`;
    }
    const nextLayer = {
      ...layer,
      id: nextId,
      name: `${layer.name || t("asset.proxyChainLayer")} Copy`,
      password: "",
      token: "",
    };
    updateLayers([...layers, nextLayer]);
    setSelectedLayerId(nextLayer.id);
  };
  const addLayer = (layer: ProxyChainLayerForm) => {
    updateLayers([...layers, layer]);
    setSelectedLayerId(layer.id);
  };
  const activeLayer = layers.find((layer) => layer.id === selectedLayerId) || layers[0];
  const setMode = (mode: "direct" | "chain") => {
    if (mode === "direct") {
      onChange({
        connectionType: "direct",
        sshTunnelId: 0,
        proxyHost: "",
        proxyPassword: "",
        encryptedProxyPassword: "",
        proxyChainLayers: [],
      });
      return;
    }
    onChange({ connectionType: value.connectionType === "direct" ? "jumphost" : value.connectionType });
  };

  return (
    <div className="flex flex-col gap-4">
      {/* Connection type (own label) */}
      <Field label={t("asset.connectionType")}>
        <Segmented
          value={isChainMode ? "chain" : "direct"}
          onChange={(v) => setMode(v as "direct" | "chain")}
          aria-label={t("asset.connectionType")}
          options={[
            { value: "direct", label: t("asset.connectionDirect") },
            { value: "chain", label: t("asset.connectionTunnelProxy") },
          ]}
        />
      </Field>

      {isChainMode && (
        <div className="flex flex-col gap-3">
          <div className="flex flex-col gap-2">
            <div className="min-w-0 break-words text-xs leading-5 text-muted-foreground">
              {layers.length > 0
                ? layers.map((layer) => layer.name || t("asset.proxyChainLayer")).join(" > ")
                : t("asset.proxyChainDirect")}
            </div>
            <div className="flex flex-wrap gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-8 gap-1.5"
                onClick={() => addLayer(sshProxyLayer())}
              >
                <Plus className="h-3.5 w-3.5" />
                {t("asset.proxyChainAddSSH")}
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-8 gap-1.5"
                onClick={() => addLayer(socks5ProxyLayer())}
              >
                <Plus className="h-3.5 w-3.5" />
                {t("asset.proxyChainAddProxy")}
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-8 gap-1.5"
                onClick={() => addLayer(httpTunnelProxyLayer())}
              >
                <Plus className="h-3.5 w-3.5" />
                {t("asset.proxyChainAddHTTPTunnel")}
              </Button>
            </div>
          </div>

          {layers.length > 0 && (
            <div className="flex flex-col gap-2">
              {layers.map((layer, index) => (
                <div
                  key={layer.id}
                  className="flex min-h-10 items-center gap-2 rounded-md border bg-background px-2 py-1.5"
                >
                  <GripVertical className="h-4 w-4 shrink-0 text-muted-foreground" />
                  <span className="w-5 shrink-0 text-center text-xs tabular-nums text-muted-foreground">
                    {index + 1}
                  </span>
                  <Checkbox
                    checked={layer.enabled}
                    onCheckedChange={(checked) => patchLayer(layer.id, { enabled: checked === true })}
                  />
                  <button
                    type="button"
                    className="min-w-0 flex-1 truncate text-left text-sm"
                    onClick={() => setSelectedLayerId(layer.id)}
                  >
                    {layer.name || t("asset.proxyChainLayer")}
                  </button>
                  <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
                    {layerTypeLabel(layer.type)}
                  </span>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7"
                    onClick={() => moveLayer(index, -1)}
                    disabled={index === 0}
                  >
                    <ArrowUp className="h-3.5 w-3.5" />
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7"
                    onClick={() => moveLayer(index, 1)}
                    disabled={index === layers.length - 1}
                  >
                    <ArrowDown className="h-3.5 w-3.5" />
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7"
                    onClick={() => duplicateLayer(layer)}
                  >
                    <Copy className="h-3.5 w-3.5" />
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7 text-destructive"
                    onClick={() => updateLayers(layers.filter((item) => item.id !== layer.id))}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </div>
              ))}
            </div>
          )}

          {activeLayer && (
            <ProxyChainLayerFields
              layer={activeLayer}
              onChange={(patch) => patchLayer(activeLayer.id, patch)}
              excludeIds={excludeIds}
            />
          )}
        </div>
      )}
    </div>
  );
}

function layerTypeLabel(type: ProxyChainLayerForm["type"]) {
  if (type === "ssh") return "SSH";
  if (type === "http_tunnel") return "HTTP";
  return "SOCKS5";
}

function ProxyChainLayerFields({
  layer,
  onChange,
  excludeIds,
}: {
  layer: ProxyChainLayerForm;
  onChange: (patch: Partial<ProxyChainLayerForm>) => void;
  excludeIds?: number[];
}) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-4 rounded-md border bg-muted/20 p-3">
      <div className="flex items-end gap-3">
        <Field label={t("asset.proxyChainLayerName")} className="flex-1">
          <Input value={layer.name} onChange={(e) => onChange({ name: e.target.value })} />
        </Field>
        <Field label="Type" className="w-[150px] shrink-0">
          <Select value={layer.type} onValueChange={(type) => onChange({ type: type as ProxyChainLayerForm["type"] })}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="ssh">SSH</SelectItem>
              <SelectItem value="socks5">SOCKS5</SelectItem>
              <SelectItem value="http_tunnel">HTTP Tunnel</SelectItem>
            </SelectContent>
          </Select>
        </Field>
      </div>

      {layer.type === "ssh" && (
        <Field label={t("asset.proxyChainSSHAsset")} required>
          <AssetSelect
            value={layer.sshAssetId}
            onValueChange={(sshAssetId) => onChange({ sshAssetId })}
            filterType="ssh"
            excludeIds={excludeIds}
            placeholder={t("asset.jumpHostNone")}
          />
        </Field>
      )}

      {layer.type === "socks5" && (
        <>
          <div className="flex items-end gap-3">
            <Field label={t("asset.proxyHost")} required className="flex-1">
              <Input value={layer.host} onChange={(e) => onChange({ host: e.target.value })} placeholder="127.0.0.1" />
            </Field>
            <Field label={t("asset.proxyPort")} required className="w-[110px] shrink-0">
              <Input
                className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                type="number"
                value={layer.port || ""}
                placeholder="1080"
                onChange={(e) => onChange({ port: Number(e.target.value) })}
              />
            </Field>
          </div>
          <div className="flex items-end gap-3">
            <Field label={t("asset.proxyUsername")} className="flex-1">
              <Input value={layer.username} onChange={(e) => onChange({ username: e.target.value })} />
            </Field>
            <Field label={t("asset.proxyPassword")} className="flex-1">
              <Input
                type="password"
                value={layer.password}
                onChange={(e) => onChange({ password: e.target.value })}
                placeholder={layer.encryptedPassword ? t("asset.passwordUnchanged") : ""}
              />
            </Field>
          </div>
        </>
      )}

      {layer.type === "http_tunnel" && (
        <>
          <Field label={t("asset.proxyChainHTTPURL")} required>
            <Input
              value={layer.url}
              onChange={(e) => onChange({ url: e.target.value })}
              placeholder="https://dbx.example.com/dbx_tunnel.php"
            />
          </Field>
          <div className="flex items-end gap-3">
            <Field label={t("asset.proxyChainHTTPToken")} required className="flex-1">
              <Input
                type="password"
                value={layer.token}
                onChange={(e) => onChange({ token: e.target.value })}
                placeholder={layer.encryptedToken ? t("asset.passwordUnchanged") : "dbx_tunnel.php token"}
              />
            </Field>
            <Field label={t("asset.proxyChainTimeout")} className="w-[150px] shrink-0">
              <Input
                className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                type="number"
                min={1}
                value={layer.timeoutSeconds || ""}
                onChange={(e) => onChange({ timeoutSeconds: Number(e.target.value) })}
              />
            </Field>
          </div>
        </>
      )}
    </div>
  );
}
