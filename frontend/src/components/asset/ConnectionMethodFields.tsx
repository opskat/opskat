import { useMemo, useRef, useState, type ComponentType } from "react";
import { useTranslation } from "react-i18next";
import {
  ChevronUp,
  CircleAlert,
  Copy,
  Globe,
  GripVertical,
  Monitor,
  Plus,
  Route,
  Server,
  Target,
  Trash2,
  TriangleAlert,
} from "lucide-react";
import {
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  cn,
} from "@opskat/ui";
import { DndContext, PointerSensor, closestCenter, useSensor, useSensors, type DragEndEvent } from "@dnd-kit/core";
import { SortableContext, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { SecretInput } from "@/components/SecretInput";
import { Field, Segmented } from "@/components/asset/fields";
import {
  httpTunnelProxyLayer,
  proxyChainLayerErrors,
  reorderLayers,
  socks5ProxyLayer,
  sshProxyLayer,
  type ConnectionFormFields,
  type ProxyChainLayerError,
  type ProxyChainLayerForm,
  type ProxyChainLayerType,
} from "./proxyConfig";

interface ConnectionMethodFieldsProps {
  value: ConnectionFormFields;
  onChange: (patch: Partial<ConnectionFormFields>) => void;
  /** 排除可选 SSH 资产(如自身),不能把自己选作跳板机/隧道。 */
  excludeIds?: number[];
  /**
   * 扩展资产类型专用:声明了 connection.sshTunnel 时传入,在同一个"连接方式"选择器里追加
   * 一个 SSH 隧道选项。隧道资产走资产的 sshTunnelId 列,与代理链走宿主保留键是两条独立的
   * 持久化路径,因此隧道资产 id 由调用方独立维护、独立传入,不占用
   * value.proxyChainLayers / value.connectionType——切到 chain 再切回 tunnel(或反过来)
   * 都不会碰对方的数据。省略时(内置类型)渲染与之前完全一致:Direct / Tunnel+Proxy 两项。
   */
  sshTunnel?: {
    assetId: number;
    onAssetIdChange: (assetId: number) => void;
    /** 隧道方式当前是否选中(与 chain 互斥),由调用方在自己的表单状态里维护。 */
    active: boolean;
    onActiveChange: (active: boolean) => void;
    testId?: string;
  };
  /** 是否出现"代理链"(chain)选项;省略时默认 true,内置类型行为不变。扩展类型只声明了
   *  connection.sshTunnel、未声明 proxyChain 时传 false,隐藏链路构建器。 */
  showChain?: boolean;
}

type ConnectionMethod = "direct" | "tunnel" | "chain";

interface LayerVisual {
  icon: ComponentType<{ className?: string }>;
  /** text-* class for the accent */
  text: string;
  /** soft background for the marker */
  softBg: string;
  /** solid background + foreground for the selected marker */
  solidBg: string;
  /** border for a selected card */
  ring: string;
  nameKey: string;
  descKey: string;
}

const LAYER_VISUAL: Record<ProxyChainLayerType, LayerVisual> = {
  ssh: {
    icon: Server,
    text: "text-primary",
    softBg: "bg-primary/10",
    solidBg: "bg-primary text-primary-foreground",
    ring: "border-primary/60",
    nameKey: "asset.proxyChainTypeSSHName",
    descKey: "asset.proxyChainTypeSSHDesc",
  },
  socks5: {
    icon: Route,
    text: "text-success",
    softBg: "bg-success/15",
    solidBg: "bg-success text-success-foreground",
    ring: "border-success/60",
    nameKey: "asset.proxyChainTypeSOCKS5Name",
    descKey: "asset.proxyChainTypeSOCKS5Desc",
  },
  http_tunnel: {
    icon: Globe,
    text: "text-warning",
    softBg: "bg-warning/15",
    solidBg: "bg-warning text-warning-foreground",
    ring: "border-warning/50",
    nameKey: "asset.proxyChainTypeHTTPName",
    descKey: "asset.proxyChainTypeHTTPDesc",
  },
};

/** 连接方式与代理链配置,SSH 与数据库族共用;扩展资产类型经 sshTunnel/showChain 复用同一个选择器。 */
export function ConnectionMethodFields({
  value,
  onChange,
  excludeIds,
  sshTunnel,
  showChain = true,
}: ConnectionMethodFieldsProps) {
  const { t } = useTranslation();
  const layers = useMemo(() => value.proxyChainLayers || [], [value.proxyChainLayers]);
  const chainConfigured = showChain && value.connectionType !== "direct";
  const method: ConnectionMethod = sshTunnel?.active ? "tunnel" : chainConfigured ? "chain" : "direct";
  const isChainMode = method === "chain";
  const [selectedLayerIdValue, setSelectedLayerId] = useState("");
  const selectedLayerId = layers.some((l) => l.id === selectedLayerIdValue) ? selectedLayerIdValue : "";
  const errors = useMemo(() => proxyChainLayerErrors(layers), [layers]);
  const errorByLayer = useMemo(() => {
    const map = new Map<string, ProxyChainLayerError[]>();
    for (const e of errors) map.set(e.id, [...(map.get(e.id) || []), e]);
    return map;
  }, [errors]);

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }));

  const updateLayers = (next: ProxyChainLayerForm[]) => onChange({ proxyChainLayers: next });
  const patchLayer = (id: string, patch: Partial<ProxyChainLayerForm>) =>
    updateLayers(layers.map((l) => (l.id === id ? { ...l, ...patch } : l)));
  const removeLayer = (id: string) => updateLayers(layers.filter((l) => l.id !== id));
  const duplicateLayer = (layer: ProxyChainLayerForm) => {
    const existing = new Set(layers.map((l) => l.id));
    let i = layers.length + 1;
    let nextId = `${layer.type}-copy-${i}`;
    while (existing.has(nextId)) nextId = `${layer.type}-copy-${(i += 1)}`;
    const copy = {
      ...layer,
      id: nextId,
      name: `${layer.name || t("asset.proxyChainLayer")} Copy`,
      password: "",
      token: "",
    };
    updateLayers([...layers, copy]);
    setSelectedLayerId(copy.id);
  };
  const addLayer = (layer: ProxyChainLayerForm) => {
    updateLayers([...layers, layer]);
    setSelectedLayerId(layer.id);
  };
  // 记住切到「直连」前的链路,切回时恢复;避免误触直连丢失已配置的代理节点。
  // 「直连」仍会清空持久化的 proxyChainLayers(build 语义不变),恢复只发生在本次会话的来回切换。
  const stashedLayers = useRef<ProxyChainLayerForm[]>([]);
  const setMethod = (next: ConnectionMethod) => {
    if (next === "tunnel") {
      // 只切换"选中哪个方式",不碰代理链数据——从 chain 切到 tunnel 再切回时链路原样还在。
      sshTunnel?.onActiveChange(true);
      return;
    }
    if (sshTunnel?.active) {
      // 关闭隧道方式;隧道资产 id 留在调用方状态里不清零,切回 tunnel 时不用重选。
      sshTunnel.onActiveChange(false);
    }
    if (next === "direct") {
      if (layers.length) stashedLayers.current = layers;
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
    const restore = layers.length === 0 && stashedLayers.current.length > 0 ? stashedLayers.current : undefined;
    onChange({
      connectionType: value.connectionType === "direct" ? "jumphost" : value.connectionType,
      ...(restore ? { proxyChainLayers: restore } : {}),
    });
  };
  const onDragEnd = (e: DragEndEvent) => {
    if (e.over && e.active.id !== e.over.id) {
      updateLayers(reorderLayers(layers, String(e.active.id), String(e.over.id)));
    }
  };

  return (
    <div className="flex flex-col gap-4">
      <Field label={t("asset.connectionType")}>
        <Segmented
          value={method}
          onChange={setMethod}
          aria-label={t("asset.connectionType")}
          options={[
            { value: "direct" as const, label: t("asset.connectionDirect") },
            ...(sshTunnel ? [{ value: "tunnel" as const, label: t("asset.sshTunnel") }] : []),
            ...(showChain ? [{ value: "chain" as const, label: t("asset.connectionTunnelProxy") }] : []),
          ]}
        />
        {sshTunnel && method === "tunnel" && (
          <AssetSelect
            value={sshTunnel.assetId}
            onValueChange={sshTunnel.onAssetIdChange}
            filterType="ssh"
            placeholder={t("asset.jumpHostNone")}
            testId={sshTunnel.testId}
          />
        )}
      </Field>

      {isChainMode && (
        <div className="flex flex-col gap-3">
          {errors.length > 0 && (
            <div className="flex items-center gap-2 rounded-md bg-destructive/10 px-3 py-2 text-[12px] font-medium text-destructive">
              <TriangleAlert className="h-3.5 w-3.5 shrink-0" />
              {t("asset.proxyChainProblems", { count: errors.length })}
            </div>
          )}

          <div className="relative flex flex-col gap-2">
            {/* rail spine behind the markers */}
            <div className="pointer-events-none absolute left-[13px] top-3 bottom-3 w-px bg-border" aria-hidden />

            <EndpointRow
              icon={Monitor}
              tone="muted"
              title={t("asset.proxyChainLocal")}
              sub={t("asset.proxyChainLocalHint")}
            />

            {layers.length === 0 ? (
              <EmptySlot title={t("asset.proxyChainEmptyTitle")} hint={t("asset.proxyChainEmptyHint")} />
            ) : (
              <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={onDragEnd}>
                <SortableContext items={layers.map((l) => l.id)} strategy={verticalListSortingStrategy}>
                  {layers.map((layer) => (
                    <SortableHop
                      key={layer.id}
                      layer={layer}
                      selected={layer.id === selectedLayerId}
                      hasError={errorByLayer.has(layer.id)}
                      onSelect={() => setSelectedLayerId(selectedLayerId === layer.id ? "" : layer.id)}
                      onDuplicate={() => duplicateLayer(layer)}
                      onRemove={() => removeLayer(layer.id)}
                      onPatch={(patch) => patchLayer(layer.id, patch)}
                      layerErrors={errorByLayer.get(layer.id) || []}
                      excludeIds={excludeIds}
                    />
                  ))}
                </SortableContext>
              </DndContext>
            )}

            <EndpointRow icon={Target} tone="primary" title={t("asset.proxyChainTargetLabel")} sub="" />
          </div>

          <div className="pl-[34px]">
            <AddNodeMenu
              empty={layers.length === 0}
              onAdd={(type) =>
                addLayer(
                  type === "ssh" ? sshProxyLayer() : type === "socks5" ? socks5ProxyLayer() : httpTunnelProxyLayer()
                )
              }
            />
          </div>

          <p className="pl-[34px] text-[11px] leading-relaxed text-muted-foreground/80">
            {t("asset.proxyChainDirectHint")}
          </p>
        </div>
      )}
    </div>
  );
}

function EndpointRow({
  icon: Icon,
  tone,
  title,
  sub,
}: {
  icon: ComponentType<{ className?: string }>;
  tone: "muted" | "primary";
  title: string;
  sub: string;
}) {
  return (
    <div className="relative z-10 flex items-center gap-3">
      <span
        className={cn(
          "flex h-7 w-7 shrink-0 items-center justify-center rounded-full border bg-background",
          tone === "primary" ? "border-primary/60 text-primary" : "border-border text-muted-foreground"
        )}
      >
        <Icon className="h-3.5 w-3.5" />
      </span>
      <div className="min-w-0">
        <div className="truncate text-[12.5px] font-semibold">{title}</div>
        {sub && <div className="truncate text-[11px] text-muted-foreground">{sub}</div>}
      </div>
    </div>
  );
}

function EmptySlot({ title, hint }: { title: string; hint: string }) {
  return (
    <div className="relative z-10 flex items-center gap-3">
      <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full border border-dashed border-border bg-background text-muted-foreground">
        <Plus className="h-3.5 w-3.5" />
      </span>
      <div className="min-w-0 flex-1 rounded-lg border border-dashed bg-background px-3 py-2.5">
        <div className="text-[12.5px] font-medium text-muted-foreground">{title}</div>
        <div className="text-[11px] text-muted-foreground/70">{hint}</div>
      </div>
    </div>
  );
}

function AddNodeMenu({ empty, onAdd }: { empty: boolean; onAdd: (type: ProxyChainLayerType) => void }) {
  const { t } = useTranslation();
  const items: ProxyChainLayerType[] = ["ssh", "socks5", "http_tunnel"];
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button type="button" variant={empty ? "default" : "outline"} size="sm" className="h-9 w-full gap-1.5">
          <Plus className="h-3.5 w-3.5" />
          {empty ? t("asset.proxyChainAddFirst") : t("asset.proxyChainAddNode")}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-[260px]">
        {items.map((type) => {
          const v = LAYER_VISUAL[type];
          const Icon = v.icon;
          return (
            <DropdownMenuItem key={type} onSelect={() => onAdd(type)} className="gap-2.5 py-2">
              <span className={cn("flex h-7 w-7 shrink-0 items-center justify-center rounded-md", v.softBg)}>
                <Icon className={cn("h-3.5 w-3.5", v.text)} />
              </span>
              <span className="flex min-w-0 flex-col">
                <span className="text-[12.5px] font-semibold">{t(v.nameKey)}</span>
                <span className="text-[11px] text-muted-foreground">{t(v.descKey)}</span>
              </span>
            </DropdownMenuItem>
          );
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function SortableHop({
  layer,
  selected,
  hasError,
  onSelect,
  onDuplicate,
  onRemove,
  onPatch,
  layerErrors,
  excludeIds,
}: {
  layer: ProxyChainLayerForm;
  selected: boolean;
  hasError: boolean;
  onSelect: () => void;
  onDuplicate: () => void;
  onRemove: () => void;
  onPatch: (patch: Partial<ProxyChainLayerForm>) => void;
  layerErrors: ProxyChainLayerError[];
  excludeIds?: number[];
}) {
  const { t } = useTranslation();
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: layer.id });
  const v = LAYER_VISUAL[layer.type];
  const Icon = v.icon;
  const style = { transform: CSS.Transform.toString(transform), transition };
  const cardBorder = hasError ? "border-destructive/70" : selected ? v.ring : "border-border";

  return (
    <div
      ref={setNodeRef}
      style={style}
      className={cn("relative z-10 flex items-start gap-3", isDragging && "opacity-70")}
    >
      <span
        className={cn(
          "mt-1 flex h-7 w-7 shrink-0 items-center justify-center rounded-full border border-transparent",
          selected ? v.solidBg : cn("bg-background", v.softBg, v.text)
        )}
      >
        <Icon className="h-3.5 w-3.5" />
      </span>

      <div className={cn("min-w-0 flex-1 rounded-lg border bg-card", cardBorder, isDragging && "shadow-lg")}>
        <div className="flex items-center gap-2 px-3 py-2">
          <button
            type="button"
            className="shrink-0 cursor-grab touch-none text-muted-foreground active:cursor-grabbing"
            aria-label={t("asset.proxyChainReorderHint")}
            {...attributes}
            {...listeners}
          >
            <GripVertical className="h-4 w-4" />
          </button>
          <button type="button" className="min-w-0 flex-1 cursor-pointer text-left" onClick={onSelect}>
            <div className="truncate text-[13.5px] font-semibold">{layer.name || t("asset.proxyChainLayer")}</div>
            <div className="truncate text-[11.5px] text-muted-foreground">
              {t(v.nameKey)}
              {layer.type === "socks5" && layer.host ? ` · ${layer.host}:${layer.port}` : ""}
              {layer.type === "http_tunnel" && layer.url ? ` · ${layer.url}` : ""}
            </div>
          </button>
          <div className="flex shrink-0 items-center gap-0.5">
            <Button type="button" variant="ghost" size="icon" className="h-7 w-7" onClick={onDuplicate}>
              <Copy className="h-3.5 w-3.5" />
            </Button>
            <Button type="button" variant="ghost" size="icon" className="h-7 w-7 text-destructive" onClick={onRemove}>
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
            {selected && (
              <Button type="button" variant="ghost" size="icon" className="h-7 w-7" onClick={onSelect}>
                <ChevronUp className="h-3.5 w-3.5" />
              </Button>
            )}
          </div>
        </div>

        {selected && (
          <div className="border-t p-3">
            <ProxyChainLayerFields layer={layer} onChange={onPatch} excludeIds={excludeIds} errors={layerErrors} />
          </div>
        )}
      </div>
    </div>
  );
}

function fieldError(errors: ProxyChainLayerError[], field: ProxyChainLayerError["field"], t: (k: string) => string) {
  const e = errors.find((x) => x.field === field);
  return e ? t(e.messageKey) : "";
}

function ProxyChainLayerFields({
  layer,
  onChange,
  excludeIds,
  errors,
}: {
  layer: ProxyChainLayerForm;
  onChange: (patch: Partial<ProxyChainLayerForm>) => void;
  excludeIds?: number[];
  errors: ProxyChainLayerError[];
}) {
  const { t } = useTranslation();
  const invalid = "border-destructive focus-visible:ring-destructive/40";
  const errText = (msg: string) =>
    msg ? (
      <span className="mt-1 flex items-center gap-1 text-[11px] text-destructive">
        <CircleAlert className="h-3 w-3" />
        {msg}
      </span>
    ) : null;

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-end gap-3">
        <Field label={t("asset.proxyChainLayerName")} className="flex-1">
          <Input value={layer.name} onChange={(e) => onChange({ name: e.target.value })} />
        </Field>
        <Field label={t("asset.proxyChainType")} className="w-[140px] shrink-0">
          <Select value={layer.type} onValueChange={(type) => onChange({ type: type as ProxyChainLayerType })}>
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
          {errText(fieldError(errors, "sshAssetId", t))}
        </Field>
      )}

      {layer.type === "socks5" && (
        <>
          <div className="flex items-end gap-3">
            <Field label={t("asset.proxyHost")} required className="flex-1">
              <Input
                className={cn(fieldError(errors, "host", t) && invalid)}
                value={layer.host}
                onChange={(e) => onChange({ host: e.target.value })}
                placeholder="127.0.0.1"
              />
              {errText(fieldError(errors, "host", t))}
            </Field>
            <Field label={t("asset.proxyPort")} required className="w-[110px] shrink-0">
              <Input
                className={cn(
                  "[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none",
                  fieldError(errors, "port", t) && invalid
                )}
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
              <SecretInput
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
              className={cn(fieldError(errors, "url", t) && invalid)}
              value={layer.url}
              onChange={(e) => onChange({ url: e.target.value })}
              placeholder="https://dbx.example.com/dbx_tunnel.php"
            />
            {errText(fieldError(errors, "url", t))}
          </Field>
          <div className="flex items-end gap-3">
            <Field label={t("asset.proxyChainHTTPToken")} required className="flex-1">
              <SecretInput
                className={cn(fieldError(errors, "token", t) && invalid)}
                value={layer.token}
                onChange={(e) => onChange({ token: e.target.value })}
                placeholder={layer.encryptedToken ? t("asset.passwordUnchanged") : "dbx_tunnel.php token"}
              />
              {errText(fieldError(errors, "token", t))}
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
