import { useCallback, useEffect, useMemo, useState, type ComponentType } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import {
  AlertCircle,
  ArrowUpCircle,
  Check,
  Download,
  FilePen,
  FileText,
  Globe,
  Loader2,
  Network,
  Puzzle,
  RefreshCw,
  Router,
  Search,
  SearchX,
  ShieldAlert,
  ShieldCheck,
  ShieldX,
} from "lucide-react";
import { Button, Card, CardContent, CardDescription, CardHeader, CardTitle, Input, cn } from "@opskat/ui";
import { EntityIcon } from "@/components/asset/AssetIcon";
import { formatBytes } from "@/lib/formatBytes";
import { pinyinMatch } from "@/lib/pinyin";
import { useSettingsUiStore } from "@/stores/settingsUiStore";
import { ListStore, RefreshStore } from "../../../wailsjs/go/extension/Extension";
import type { extension, extstore_svc } from "../../../wailsjs/go/models";
import { revealDownloadMirrorSetting } from "./downloadMirror";

export type StoreCard = extstore_svc.Card;

interface CapabilityTag {
  key: string;
  label: string;
  icon: ComponentType<{ className?: string }>;
  /** What the grant covers (paths / URL prefixes), shown on hover. */
  detail?: string;
  warning?: boolean;
}

/** One tag per kind of grant the version asks for (the kinds the install confirm lists). */
function capabilityTags(c: extension.Capabilities): CapabilityTag[] {
  const tags: CapabilityTag[] = [];
  if (c.network.assetEndpoint) {
    tags.push({ key: "assetEndpoint", label: "extension.capability.assetEndpoint", icon: Network });
  }
  if (c.credentials === "read") {
    tags.push({ key: "credentials", label: "extension.capability.credentials", icon: ShieldAlert, warning: true });
  }
  if (c.fs.read?.length) {
    tags.push({ key: "fsRead", label: "extension.capability.fsRead", icon: FileText, detail: c.fs.read.join("\n") });
  }
  if (c.fs.write?.length) {
    tags.push({ key: "fsWrite", label: "extension.capability.fsWrite", icon: FilePen, detail: c.fs.write.join("\n") });
  }
  if (c.http.allowlist?.length) {
    tags.push({ key: "http", label: "extension.capability.http", icon: Globe, detail: c.http.allowlist.join("\n") });
  }
  if (c.tunnel) {
    tags.push({ key: "tunnel", label: "extension.capability.tunnel", icon: Router });
  }
  return tags;
}

const REASON_LABEL: Record<string, string> = {
  hostABI: "extension.store.reason.hostABI",
  minAppVersion: "extension.store.reason.minAppVersion",
  source: "extension.store.reason.source",
};

interface ExtensionStoreProps {
  /**
   * Installs or updates the card's extension (store install flow). Resolves once
   * it has landed — the store then reloads to show the new state — and rejects
   * when it did not.
   */
  onInstall: (card: StoreCard) => Promise<void>;
}

/**
 * Settings → Extensions → Store: the official index, verified, as a card grid.
 * Opening it shows the last index at once and refreshes it from the network.
 */
export function ExtensionStore({ onInstall }: ExtensionStoreProps) {
  const { t, i18n } = useTranslation();
  const lang = i18n.language;
  const [state, setState] = useState<extstore_svc.State | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setRefreshing(true);
    try {
      setState(await RefreshStore(lang));
    } catch (e) {
      toast.error(String(e));
    } finally {
      setRefreshing(false);
    }
  }, [lang]);

  useEffect(() => {
    void (async () => {
      try {
        setState(await ListStore(lang));
      } catch (e) {
        toast.error(String(e));
        return;
      }
      await refresh();
    })();
  }, [lang, refresh]);

  const install = async (card: StoreCard) => {
    setBusy(card.name);
    try {
      await onInstall(card);
      setState(await ListStore(lang));
    } catch (e) {
      toast.error(`${t("extension.installError")}: ${String(e)}`);
    } finally {
      setBusy(null);
    }
  };

  const cards = useMemo(() => {
    const all = state?.extensions ?? [];
    const q = query.trim();
    if (!q) return all;
    return all.filter((c) => pinyinMatch(c.displayName, q) || pinyinMatch(c.name, q) || pinyinMatch(c.description, q));
  }, [state, query]);

  const loaded = !!state && (!!state.error || state.updatedAt > 0);

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("extension.store.title")}</CardTitle>
        <CardDescription>{t("extension.store.description")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex gap-2">
          <div className="relative flex-1">
            <Search className="absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" aria-hidden />
            <Input
              className="pl-8"
              placeholder={t("extension.store.searchPlaceholder")}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
          <Button variant="outline" onClick={() => void refresh()} disabled={refreshing} className="gap-1.5">
            <RefreshCw className={cn("h-3.5 w-3.5", refreshing && "animate-spin")} aria-hidden />
            {t("extension.store.refresh")}
          </Button>
        </div>

        {state && !state.error && state.updatedAt > 0 && (
          <p data-testid="ext-store-status" className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <ShieldCheck className="h-3.5 w-3.5 text-success" aria-hidden />
            <span>
              {t("extension.store.updatedAt", { time: new Date(state.updatedAt).toLocaleString(lang) })}
              {" · "}
              {t("extension.store.verified")}
            </span>
          </p>
        )}

        {!loaded ? (
          <StoreSkeleton />
        ) : state.error ? (
          <StoreError error={state.error} retrying={refreshing} onRetry={() => void refresh()} />
        ) : state.extensions.length === 0 ? (
          <EmptyBlock icon={Puzzle} text={t("extension.store.empty")} />
        ) : cards.length === 0 ? (
          <EmptyBlock icon={SearchX} text={t("extension.store.noResults", { query: query.trim() })} />
        ) : (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(20rem,1fr))] gap-3">
            {cards.map((c) => (
              <StoreCardView key={c.name} card={c} busy={busy === c.name} onInstall={() => void install(c)} />
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function StoreCardView({ card, busy, onInstall }: { card: StoreCard; busy: boolean; onInstall: () => void }) {
  const { t } = useTranslation();
  const unavailable = card.action === "unavailable";
  const reason = card.unavailable;

  return (
    <div
      data-testid={`ext-store-card-${card.name}`}
      className="flex flex-col gap-3 rounded-lg border border-border bg-card p-4"
    >
      <div className={cn("flex items-start gap-3", unavailable && "opacity-60")}>
        <div className="h-9 w-9 shrink-0 rounded-md bg-muted flex items-center justify-center">
          <EntityIcon icon={card.icon} fallback={Puzzle} className="h-4 w-4" aria-hidden />
        </div>
        <div className="min-w-0 space-y-0.5">
          <p className="flex items-baseline gap-2">
            <span className="truncate font-medium text-sm" title={card.name}>
              {card.displayName}
            </span>
            <span className="shrink-0 font-mono text-xs text-muted-foreground">v{card.version}</span>
          </p>
          {card.description && <p className="line-clamp-2 text-xs text-muted-foreground">{card.description}</p>}
        </div>
      </div>

      <CapabilityTags capabilities={card.capabilities} dim={unavailable} />

      <div className="mt-auto flex items-center justify-between gap-2">
        {unavailable && reason ? (
          <p className="flex min-w-0 items-center gap-1 text-xs text-destructive">
            <AlertCircle className="h-3.5 w-3.5 shrink-0" aria-hidden />
            <span className="truncate">
              {t(REASON_LABEL[reason.reason], {
                hostABI: reason.hostABI,
                version: reason.minAppVersion,
                type: reason.sourceType,
              })}
            </span>
          </p>
        ) : (
          <p className="text-xs text-muted-foreground">
            {formatBytes(card.size)}
            {card.action === "update" &&
              ` · ${t("extension.store.installedVersion", { version: card.installedVersion })}`}
          </p>
        )}
        <StatusButton card={card} busy={busy} onInstall={onInstall} />
      </div>
    </div>
  );
}

function StatusButton({ card, busy, onInstall }: { card: StoreCard; busy: boolean; onInstall: () => void }) {
  const { t } = useTranslation();
  const spinner = <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />;
  switch (card.action) {
    case "install":
      return (
        <Button variant="outline" size="sm" className="gap-1.5" disabled={busy} onClick={onInstall}>
          {busy ? spinner : <Download className="h-3.5 w-3.5" aria-hidden />}
          {t("extension.store.install")}
        </Button>
      );
    case "update":
      return (
        <Button size="sm" className="gap-1.5" disabled={busy} onClick={onInstall}>
          {busy ? spinner : <ArrowUpCircle className="h-3.5 w-3.5" aria-hidden />}
          {t("extension.store.updateTo", { version: card.version })}
        </Button>
      );
    case "installed":
      return (
        <Button variant="outline" size="sm" className="gap-1.5" disabled>
          <Check className="h-3.5 w-3.5" aria-hidden />
          {t("extension.store.installed")}
        </Button>
      );
    default:
      return (
        <Button variant="outline" size="sm" disabled>
          {t("extension.store.unavailable")}
        </Button>
      );
  }
}

function CapabilityTags({ capabilities, dim }: { capabilities: extension.Capabilities; dim: boolean }) {
  const { t } = useTranslation();
  const tags = capabilityTags(capabilities);
  if (tags.length === 0) return null;
  return (
    <div className={cn("flex flex-wrap gap-1.5", dim && "opacity-60")}>
      {tags.map(({ key, label, icon: Icon, detail, warning }) => (
        <span
          key={key}
          title={detail}
          data-warning={warning || undefined}
          className={cn(
            "inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 text-xs",
            warning ? "bg-warning/15 text-warning font-medium" : "bg-muted text-muted-foreground"
          )}
        >
          <Icon className="h-3 w-3" aria-hidden />
          {t(label)}
        </span>
      ))}
    </div>
  );
}

const ERROR_COPY: Record<string, { title: string; desc: string }> = {
  fetch: { title: "extension.store.fetchFailed", desc: "extension.store.fetchFailedDesc" },
  signature: { title: "extension.store.signatureInvalid", desc: "extension.store.signatureInvalidDesc" },
  format: { title: "extension.store.unknownFormat", desc: "extension.store.unknownFormatDesc" },
  invalid: { title: "extension.store.invalid", desc: "extension.store.invalidDesc" },
};

function StoreError({
  error,
  retrying,
  onRetry,
}: {
  error: extstore_svc.StateError;
  retrying: boolean;
  onRetry: () => void;
}) {
  const { t } = useTranslation();
  const setActiveTab = useSettingsUiStore((s) => s.setActiveTab);
  const copy = ERROR_COPY[error.kind];
  const Icon = error.kind === "signature" ? ShieldX : AlertCircle;
  const mirrorRelated = error.kind === "fetch" || error.kind === "signature";

  return (
    <div
      role="alert"
      className="flex flex-col items-center gap-3 rounded-lg border border-destructive/40 p-8 text-center"
    >
      <Icon className="h-8 w-8 text-destructive" aria-hidden />
      <div className="space-y-1">
        <p className="font-medium text-sm">{t(copy.title)}</p>
        <p className="text-xs text-muted-foreground">{t(copy.desc)}</p>
      </div>
      <p className="max-w-full break-all rounded bg-muted px-2 py-1 font-mono text-xs text-muted-foreground select-text">
        {error.message}
      </p>
      <div className="flex gap-2">
        {error.kind === "format" ? (
          <Button size="sm" onClick={() => setActiveTab("about")}>
            {t("extension.store.checkUpdate")}
          </Button>
        ) : (
          <>
            {mirrorRelated && (
              <Button variant="outline" size="sm" onClick={revealDownloadMirrorSetting}>
                {t("extension.store.changeMirror")}
              </Button>
            )}
            <Button size="sm" className="gap-1.5" disabled={retrying} onClick={onRetry}>
              {retrying && <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />}
              {t("extension.store.retry")}
            </Button>
          </>
        )}
      </div>
    </div>
  );
}

function EmptyBlock({ icon: Icon, text }: { icon: ComponentType<{ className?: string }>; text: string }) {
  return (
    <div className="py-10 text-center text-muted-foreground">
      <Icon className="h-8 w-8 mx-auto mb-2 opacity-50" />
      <p className="text-sm">{text}</p>
    </div>
  );
}

/** First-load placeholder in the shape of the card grid. */
function StoreSkeleton() {
  return (
    <div
      data-testid="ext-store-skeleton"
      aria-busy="true"
      className="grid grid-cols-[repeat(auto-fill,minmax(20rem,1fr))] gap-3"
    >
      {Array.from({ length: 4 }, (_, i) => (
        <div key={i} className="flex flex-col gap-3 rounded-lg border border-border p-4 animate-pulse">
          <div className="flex gap-3">
            <div className="h-9 w-9 rounded-md bg-muted" />
            <div className="flex-1 space-y-2">
              <div className="h-3.5 w-1/2 rounded bg-muted" />
              <div className="h-3 w-4/5 rounded bg-muted" />
            </div>
          </div>
          <div className="flex gap-1.5">
            <div className="h-5 w-20 rounded-sm bg-muted" />
            <div className="h-5 w-16 rounded-sm bg-muted" />
          </div>
          <div className="flex justify-between">
            <div className="h-3 w-12 rounded bg-muted" />
            <div className="h-8 w-16 rounded-md bg-muted" />
          </div>
        </div>
      ))}
    </div>
  );
}
