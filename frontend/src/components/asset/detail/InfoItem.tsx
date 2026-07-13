import type { ComponentType, ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Globe, Monitor, Route, Server, Target } from "lucide-react";
import { cn } from "@opskat/ui";
import {
  layerTypeShortLabel,
  type ProxyChainJSON,
  type ProxyChainLayerJSON,
  type ProxyConfigJSON,
} from "../proxyConfig";

export function DetailSection({ title, children }: { title: ReactNode; children: ReactNode }) {
  return (
    <div className="rounded-xl border bg-card p-4">
      <h3 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">{title}</h3>
      {children}
    </div>
  );
}

export function DetailGrid({ children }: { children: ReactNode }) {
  return <div className="grid grid-cols-2 gap-4 text-sm">{children}</div>;
}

export function TunnelInfo({ label, name }: { label: string; name: string }) {
  return (
    <div className="mt-3 border-t pt-3 text-sm">
      <InfoItem label={label} value={name} mono />
    </div>
  );
}

/** SOCKS5 代理详情段,SSH 与数据库族详情卡共用;无 proxy 时不渲染。 */
export function ProxyDetailSection({ proxy }: { proxy?: ProxyConfigJSON | null }) {
  const { t } = useTranslation();
  if (!proxy) return null;
  return (
    <DetailSection title={t("asset.proxy")}>
      <DetailGrid>
        <InfoItem label={t("asset.proxyType")} value={(proxy.type || "socks5").toUpperCase()} />
        <InfoItem label={t("asset.proxyHost")} value={`${proxy.host}:${proxy.port}`} mono />
        {proxy.username && <InfoItem label={t("asset.proxyUsername")} value={proxy.username} />}
      </DetailGrid>
    </DetailSection>
  );
}

export function InfoItem({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <span className="text-xs text-muted-foreground">{label}</span>
      <p className={cn("mt-0.5 text-sm", mono && "font-mono")}>{value}</p>
    </div>
  );
}

const CHAIN_ICON: Record<
  ProxyChainLayerJSON["type"],
  { icon: ComponentType<{ className?: string }>; text: string; bg: string }
> = {
  ssh: { icon: Server, text: "text-primary", bg: "bg-primary/10" },
  socks5: { icon: Route, text: "text-success", bg: "bg-success/15" },
  http_tunnel: { icon: Globe, text: "text-warning", bg: "bg-warning/15" },
};

function chainLayerLines(layer: ProxyChainLayerJSON, resolveSshName: (id?: number) => string | null) {
  if (layer.type === "ssh")
    return { name: resolveSshName(layer.ssh_asset_id) || `#${layer.ssh_asset_id ?? "?"}`, detail: "" };
  if (layer.type === "socks5")
    return { name: layer.name || "SOCKS5", detail: `${layer.host ?? ""}:${layer.port ?? ""}` };
  return { name: layer.name || "HTTP", detail: layer.url || "" };
}

/** 只读代理链:本机 → 各跳 → 目标。无 layers 时不渲染。 */
export function ProxyChainDetailSection({
  chain,
  resolveSshName,
}: {
  chain?: ProxyChainJSON | null;
  resolveSshName: (id?: number) => string | null;
}) {
  const { t } = useTranslation();
  const layers = [...(chain?.layers || [])].sort((a, b) => (a.order || 0) - (b.order || 0));
  if (!layers.length) return null;

  return (
    <DetailSection
      title={
        <span className="flex items-center gap-2">
          <Route className="h-3.5 w-3.5" />
          {t("asset.proxyChainTitle")}
          <span className="rounded-full bg-muted px-2 py-0.5 text-[10.5px] font-medium normal-case tracking-normal text-muted-foreground">
            {t("asset.proxyChainHops", { count: layers.length })}
          </span>
        </span>
      }
    >
      <div className="relative flex flex-col gap-0">
        <div className="pointer-events-none absolute left-[10px] top-3 bottom-3 w-px bg-border" aria-hidden />
        <ChainEndpoint icon={Monitor} tone="muted" text={t("asset.proxyChainReadonlyLocal")} />
        {layers.map((layer, i) => {
          const meta = CHAIN_ICON[layer.type];
          const Icon = meta.icon;
          const line = chainLayerLines(layer, resolveSshName);
          return (
            <div key={layer.id || i} className="relative z-10 flex items-center gap-3 py-1.5">
              <span className={cn("flex h-5 w-5 shrink-0 items-center justify-center rounded-full", meta.bg)}>
                <Icon className={cn("h-3 w-3", meta.text)} />
              </span>
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className={cn("rounded bg-muted px-1.5 py-0.5 text-[10px] font-semibold", meta.text)}>
                    {layerTypeShortLabel(layer.type)}
                  </span>
                  <span className="truncate text-[12.5px] font-medium">{line.name}</span>
                </div>
                {line.detail && (
                  <div className="truncate font-mono text-[11px] text-muted-foreground">{line.detail}</div>
                )}
              </div>
            </div>
          );
        })}
        <ChainEndpoint icon={Target} tone="primary" text={t("asset.proxyChainReadonlyTarget")} />
      </div>
    </DetailSection>
  );
}

function ChainEndpoint({
  icon: Icon,
  tone,
  text,
}: {
  icon: ComponentType<{ className?: string }>;
  tone: "muted" | "primary";
  text: string;
}) {
  return (
    <div className="relative z-10 flex items-center gap-3 py-1.5">
      <span
        className={cn(
          "flex h-5 w-5 shrink-0 items-center justify-center rounded-full border bg-card",
          tone === "primary" ? "border-primary/60 text-primary" : "border-border text-muted-foreground"
        )}
      >
        <Icon className="h-3 w-3" />
      </span>
      <span className="text-[12px] font-semibold">{text}</span>
    </div>
  );
}
