import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@opskat/ui";
import type { ProxyConfigJSON } from "../proxyConfig";

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
