import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { notifyCopied, notifySuccess } from "@/lib/notify";
import yaml from "js-yaml";
import { Button } from "@opskat/ui";
import { Copy, History, Loader2, Pencil, Save, Trash2, X } from "lucide-react";
import { useEtcdStore } from "@/stores/etcdStore";
import { CodeEditor, type CodeEditorLanguage } from "@/components/CodeEditor";
import type { etcd_svc } from "../../../wailsjs/go/models";

export interface EtcdKeyDetailProps {
  assetId: number;
  selectedKey: string | null;
  /** 删除走父层的 ConfirmDialog；返回 true 表示用户确认，false 表示取消。 */
  onRequestDelete?: (key: string) => Promise<boolean>;
  /** 保存（put）走父层的 ConfirmDialog；返回 true 表示用户确认，false 表示取消。 */
  onRequestSave?: (key: string) => Promise<boolean>;
  /** 删除成功后通知父层清空选中态 / 重新加载树等。 */
  onDeleted?: (key: string) => void;
  /** 保存成功后通知父层（可用来重新拉树 / 刷新缓存）。 */
  onSaved?: (key: string) => void;
}

type Format = "raw" | "json" | "yaml";

const HISTORY_MAX_STEPS = 20;

function buildGetRequest(assetId: number, key: string, revision = 0): etcd_svc.ExecRequest {
  return {
    AssetID: assetId,
    Op: "get",
    Key: key,
    Value: "",
    Prefix: false,
    Limit: 0,
    Revision: revision,
    LeaseID: 0,
    Args: {} as Record<string, unknown>,
    ApprovalID: "",
    Source: "query",
  } as unknown as etcd_svc.ExecRequest;
}

function buildDelRequest(assetId: number, key: string): etcd_svc.ExecRequest {
  return {
    AssetID: assetId,
    Op: "del",
    Key: key,
    Value: "",
    Prefix: false,
    Limit: 0,
    Revision: 0,
    LeaseID: 0,
    Args: {} as Record<string, unknown>,
    ApprovalID: "",
    Source: "query",
  } as unknown as etcd_svc.ExecRequest;
}

function buildPutRequest(assetId: number, key: string, value: string): etcd_svc.ExecRequest {
  return {
    AssetID: assetId,
    Op: "put",
    Key: key,
    Value: value,
    Prefix: false,
    Limit: 0,
    Revision: 0,
    LeaseID: 0,
    Args: {} as Record<string, unknown>,
    ApprovalID: "",
    Source: "query",
  } as unknown as etcd_svc.ExecRequest;
}

function tryJsonParse(s: string): { ok: true; value: unknown } | { ok: false } {
  if (!s) return { ok: true, value: "" };
  try {
    return { ok: true, value: JSON.parse(s) };
  } catch {
    return { ok: false };
  }
}

/**
 * 把 raw 值按 fmt 转成 (显示文本, monaco 语言)。
 * 关键点:**tab 的选择直接决定语言**,即使解析失败也用对应语言交给 monaco 上色,
 * 这样切换 tab 一定有视觉差异。
 *   - raw: 嗅探一次,合法 JSON → 以 json 高亮(不重排版),否则 plaintext
 *   - json: 永远 language=json;能 parse 就 pretty-print,不能就显示原文(monaco 也会按 JSON 语法尽力 tokenize)
 *   - yaml: 永远 language=yaml;能 parse JSON 就转 yaml dump,否则显示原文
 */
function formatValue(raw: string, fmt: Format): { text: string; lang: CodeEditorLanguage } {
  if (fmt === "raw") {
    return tryJsonParse(raw).ok ? { text: raw, lang: "json" } : { text: raw, lang: "plaintext" };
  }
  if (fmt === "json") {
    const parsed = tryJsonParse(raw);
    return parsed.ok ? { text: JSON.stringify(parsed.value, null, 2), lang: "json" } : { text: raw, lang: "json" };
  }
  // yaml
  const parsed = tryJsonParse(raw);
  if (parsed.ok) {
    try {
      return { text: yaml.dump(parsed.value, { lineWidth: 120 }), lang: "yaml" };
    } catch {
      /* fallthrough */
    }
  }
  return { text: raw, lang: "yaml" };
}

function leaseDisplay(lease: unknown, fallback: string): { text: string; muted: boolean } {
  if (lease === undefined || lease === null || lease === 0 || lease === "0") return { text: fallback, muted: true };
  if (typeof lease === "number") return { text: lease.toString(16), muted: false };
  return { text: String(lease), muted: false };
}

/**
 * 内容类型启发式（前端纯计算，不走任何 magic-byte 检测，因为 etcd value 是 string）：
 *   - 空串 → "(empty)"
 *   - JSON.parse 成功 → "application/json"
 *   - 其它 → "text/plain"
 * 与设计稿一致；如未来要支持 "application/yaml" / "application/x-protobuf" 等，需要在
 * 调用方提供 hint 或在 server-side 加 sniffer。
 */
function detectContentType(raw: string): { label: string; key: "empty" | "json" | "text" } {
  if (!raw) return { label: "", key: "empty" };
  return tryJsonParse(raw).ok ? { label: "application/json", key: "json" } : { label: "text/plain", key: "text" };
}

/** 用 UTF-8 字节数估算；TextEncoder 在浏览器/happy-dom 都可用。 */
function valueSize(raw: string): string {
  if (!raw) return "0 B";
  try {
    const bytes = new TextEncoder().encode(raw).length;
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  } catch {
    return `${raw.length} ch`;
  }
}

/** 按 "/" 切分成 breadcrumb segments，最后一段高亮（与设计稿一致）。 */
function breadcrumbSegments(key: string): { sep: string; name: string; isLast: boolean }[] {
  if (!key) return [];
  const parts = key.split("/");
  const result: { sep: string; name: string; isLast: boolean }[] = [];
  let lastNonEmptyIdx = -1;
  for (let i = 0; i < parts.length; i++) {
    if (parts[i]) lastNonEmptyIdx = i;
  }
  for (let i = 0; i < parts.length; i++) {
    if (i === 0 && parts[i] === "") {
      // 路径以 / 开头
      continue;
    }
    if (parts[i] === "") continue;
    result.push({ sep: "/", name: parts[i], isLast: i === lastNonEmptyIdx });
  }
  return result;
}

export function EtcdKeyDetail({
  assetId,
  selectedKey,
  onRequestDelete,
  onRequestSave,
  onDeleted,
  onSaved,
}: EtcdKeyDetailProps) {
  const { t } = useTranslation();
  const exec = useEtcdStore((s) => s.exec);
  const [loading, setLoading] = useState(false);
  const [detail, setDetail] = useState<etcd_svc.EtcdKV | null>(null);
  const [err, setErr] = useState("");
  const [format, setFormat] = useState<Format>("raw");
  const [deleting, setDeleting] = useState(false);

  // 编辑模式
  const [editing, setEditing] = useState(false);
  const [editValue, setEditValue] = useState("");
  const [saving, setSaving] = useState(false);

  // 历史版本
  const [revision, setRevision] = useState(0);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyItems, setHistoryItems] = useState<etcd_svc.EtcdKV[]>([]);
  const historyContainerRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!selectedKey) return;
    let cancelled = false;
    setLoading(true);
    setErr("");
    setDetail(null);
    exec(buildGetRequest(assetId, selectedKey, revision))
      .then((res) => {
        if (cancelled) return;
        setDetail(res.kvs?.[0] ?? null);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        const msg = e instanceof Error ? e.message : String(e);
        setErr(msg);
        const key = revision > 0 ? "etcd.detail.historyFailed" : "etcd.error.connFailed";
        toast.error(`${t(key)}: ${msg}`);
      })
      .finally(() => {
        if (cancelled) return;
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [assetId, selectedKey, exec, t, revision]);

  // 切换 key 时复位状态
  useEffect(() => {
    setRevision(0);
    setHistoryOpen(false);
    setHistoryItems([]);
    setEditing(false);
    setEditValue("");
  }, [selectedKey]);

  // outside-click 关 history dropdown
  useEffect(() => {
    if (!historyOpen) return;
    function onDocMouseDown(e: MouseEvent) {
      const el = historyContainerRef.current;
      if (el && !el.contains(e.target as Node)) setHistoryOpen(false);
    }
    document.addEventListener("mousedown", onDocMouseDown);
    return () => document.removeEventListener("mousedown", onDocMouseDown);
  }, [historyOpen]);

  const formatted = useMemo(() => (detail ? formatValue(detail.value ?? "", format) : null), [detail, format]);
  const contentType = useMemo(() => detectContentType(detail?.value ?? ""), [detail]);
  const sizeText = useMemo(() => valueSize(detail?.value ?? ""), [detail]);
  const breadcrumbs = useMemo(() => breadcrumbSegments(detail?.key ?? selectedKey ?? ""), [detail, selectedKey]);

  const loadHistoryList = useCallback(async () => {
    if (!detail || !selectedKey) return;
    setHistoryLoading(true);
    const collected: etcd_svc.EtcdKV[] = [];
    const seen = new Set<number>();

    collected.push(detail);
    seen.add(Number(detail.modRevision));

    let probe = Number(detail.modRevision) - 1;
    try {
      for (let i = 0; i < HISTORY_MAX_STEPS && probe > 0; i++) {
        const res = await exec(buildGetRequest(assetId, selectedKey, probe));
        const kv = res.kvs?.[0];
        if (!kv) break;
        const mod = Number(kv.modRevision);
        if (seen.has(mod) || mod <= 0) break;
        collected.push(kv);
        seen.add(mod);
        probe = mod - 1;
      }
      setHistoryItems(collected);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      toast.error(`${t("etcd.detail.historyFailed")}: ${msg}`);
      setHistoryItems(collected.length > 0 ? collected : [detail]);
    } finally {
      setHistoryLoading(false);
    }
  }, [detail, selectedKey, assetId, exec, t]);

  function toggleHistory() {
    if (historyOpen) {
      setHistoryOpen(false);
      return;
    }
    setHistoryOpen(true);
    if (historyItems.length === 0) void loadHistoryList();
  }

  function pickHistoryItem(kv: etcd_svc.EtcdKV) {
    setRevision(Number(kv.modRevision) || 0);
    setHistoryOpen(false);
  }

  function pickLatest() {
    if (revision !== 0) {
      setRevision(0);
      toast(t("etcd.detail.historyCleared"));
    }
    setHistoryOpen(false);
  }

  async function copyToClipboard(value: string) {
    try {
      await navigator.clipboard.writeText(value);
      notifyCopied(t("etcd.detail.copied"));
    } catch {
      toast.error(t("etcd.detail.copyFailed"));
    }
  }

  function enterEdit() {
    if (!detail) return;
    // 编辑历史版本时不允许 save —— 给个 toast 提示并切回 latest 后再进入编辑
    if (revision > 0) {
      toast(t("etcd.detail.historyCleared"));
      setRevision(0);
      return;
    }
    setEditValue(detail.value ?? "");
    setEditing(true);
  }

  function cancelEdit() {
    setEditing(false);
    setEditValue("");
  }

  async function handleSave() {
    if (!detail || saving) return;
    if (onRequestSave) {
      const ok = await onRequestSave(detail.key);
      if (!ok) return;
    }
    setSaving(true);
    try {
      await exec(buildPutRequest(assetId, detail.key, editValue));
      notifySuccess(t("etcd.detail.saveSuccess"));
      setEditing(false);
      // 重新拉一遍 detail（revision 还是 0 / latest）
      const res = await exec(buildGetRequest(assetId, detail.key, 0));
      setDetail(res.kvs?.[0] ?? null);
      // 历史列表已脏，清空让下次展开重新加载
      setHistoryItems([]);
      onSaved?.(detail.key);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      toast.error(`${t("etcd.detail.saveFailed")}: ${msg}`);
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    if (!detail || deleting) return;
    if (onRequestDelete) {
      const ok = await onRequestDelete(detail.key);
      if (!ok) return;
    }
    setDeleting(true);
    try {
      await exec(buildDelRequest(assetId, detail.key));
      notifySuccess(t("etcd.detail.deleteSuccess", { key: detail.key }));
      onDeleted?.(detail.key);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      toast.error(`${t("etcd.detail.deleteFailed")}: ${msg}`);
    } finally {
      setDeleting(false);
    }
  }

  if (!selectedKey) {
    return <div className="p-3 text-xs text-muted-foreground">{t("etcd.detail.selectKey")}</div>;
  }
  if (loading) return <div className="p-3 text-xs text-muted-foreground">{t("etcd.detail.loading")}</div>;
  if (err) return <div className="p-3 text-xs text-destructive">{err}</div>;
  if (!detail) return <div className="p-3 text-xs text-muted-foreground">{t("etcd.detail.empty")}</div>;

  const leaseInfo = leaseDisplay(detail.lease, t("etcd.detail.metaLeaseNone"));

  return (
    <div className="flex h-full flex-col text-xs" data-testid="etcd-key-detail">
      {/* ── Header: 彩色 breadcrumb + 历史版本 banner + 复制 key 按钮 ── */}
      <div className="flex items-center gap-2 border-b px-4 py-2">
        <div className="flex min-w-0 flex-1 items-center gap-0.5 font-mono text-[13px]">
          <span className="text-muted-foreground/60">/</span>
          {breadcrumbs.map((seg, i) => (
            <span key={i} className="flex items-center gap-0.5">
              {i > 0 && <span className="text-muted-foreground/40">{seg.sep}</span>}
              <span
                className={
                  seg.isLast ? "font-semibold text-foreground" : i === 0 ? "text-warning" : "text-muted-foreground"
                }
              >
                {seg.name}
              </span>
            </span>
          ))}
        </div>
        {revision > 0 && (
          <span
            className="flex shrink-0 items-center gap-1 rounded bg-warning/10 px-1.5 py-0.5 font-mono text-[10px] text-warning"
            data-testid="etcd-detail-history-banner"
          >
            <History className="size-3" /> {t("etcd.detail.historyAt", { rev: revision })}
            <button type="button" className="ml-1 hover:text-foreground" onClick={pickLatest}>
              <X className="size-3" />
            </button>
          </span>
        )}
        <Button
          size="sm"
          variant="outline"
          className="shrink-0"
          onClick={() => void copyToClipboard(detail.key)}
          data-testid="etcd-detail-copy-key"
        >
          <Copy className="size-3" /> {t("etcd.detail.copyKey")}
        </Button>
      </div>

      {/* ── Meta row: MOD REV / CREATE REV / VERSION / LEASE / SIZE / VALUE TYPE ── */}
      <div className="flex flex-wrap items-start gap-x-6 gap-y-2 border-b px-4 py-2.5" data-testid="etcd-detail-meta">
        <MetaCol label={t("etcd.detail.metaModRev")} value={String(detail.modRevision ?? "—")} />
        <MetaCol label={t("etcd.detail.metaCreateRev")} value={String(detail.createRevision ?? "—")} />
        <MetaCol label={t("etcd.detail.metaVersion")} value={String(detail.version ?? "—")} />
        <MetaCol label={t("etcd.detail.metaLease")} value={leaseInfo.text} muted={leaseInfo.muted} />
        <MetaCol label={t("etcd.detail.metaSize")} value={sizeText} />
        <MetaCol
          label={t("etcd.detail.metaValueType")}
          value={contentType.key === "empty" ? t("etcd.detail.contentTypeEmpty") : contentType.label}
          accent={contentType.key === "json" ? "purple" : contentType.key === "text" ? "muted" : "muted"}
          testId="etcd-detail-content-type"
        />
      </div>

      {/* ── VALUE label + format tabs (编辑态隐藏 tabs，因为编辑只针对 raw value) ── */}
      <div className="flex items-center gap-2 border-b px-4 py-1.5">
        <span className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
          {t("etcd.detail.valueLabel")}
        </span>
        <div className="flex-1" />
        {!editing && (
          <div role="tablist" className="flex items-center gap-0.5 rounded-md border bg-muted/40 p-0.5">
            {(["raw", "json", "yaml"] as Format[]).map((f) => (
              <button
                key={f}
                role="tab"
                type="button"
                aria-selected={format === f}
                className={`rounded px-2.5 py-0.5 text-[11px] font-medium transition-colors ${
                  format === f
                    ? "bg-primary text-primary-foreground shadow-sm"
                    : "text-muted-foreground hover:bg-background hover:text-foreground"
                }`}
                onClick={() => setFormat(f)}
                data-testid={`etcd-detail-format-${f}`}
              >
                {t(`etcd.detail.format${f.charAt(0).toUpperCase() + f.slice(1)}`)}
              </button>
            ))}
          </div>
        )}
      </div>

      {/* ── Editor / Viewer (Monaco) ── */}
      <div className="min-h-0 flex-1 overflow-hidden border rounded m-2">
        {editing ? (
          <CodeEditor value={editValue} onChange={setEditValue} language="plaintext" testId="etcd-detail-edit-editor" />
        ) : (
          <CodeEditor
            value={formatted?.text ?? ""}
            language={formatted?.lang ?? "plaintext"}
            readOnly
            testId="etcd-detail-view-editor"
          />
        )}
      </div>

      {/* ── Action row ── */}
      <div className="flex flex-wrap items-center gap-2 border-t px-3 py-2">
        {editing ? (
          <>
            <Button
              size="sm"
              variant="default"
              onClick={() => void handleSave()}
              disabled={saving}
              data-testid="etcd-detail-save"
            >
              {saving ? <Loader2 className="size-3 animate-spin" /> : <Save className="size-3" />}
              {t("etcd.detail.save")}
            </Button>
            <Button size="sm" variant="outline" onClick={cancelEdit} disabled={saving}>
              <X className="size-3" /> {t("etcd.detail.cancel")}
            </Button>
          </>
        ) : (
          <>
            <Button size="sm" variant="default" onClick={enterEdit} data-testid="etcd-detail-edit">
              <Pencil className="size-3" /> {t("etcd.detail.edit")}
            </Button>
            <div ref={historyContainerRef} className="relative">
              <Button
                size="sm"
                variant="outline"
                onClick={toggleHistory}
                aria-expanded={historyOpen}
                data-testid="etcd-detail-history-toggle"
              >
                <History className="size-3" /> {t("etcd.detail.history")}
              </Button>
              {historyOpen && (
                <div
                  className="absolute bottom-full left-0 z-30 mb-1 max-h-72 w-72 overflow-auto rounded-md border bg-popover p-1 text-popover-foreground shadow-md"
                  data-testid="etcd-detail-history-dropdown"
                  role="listbox"
                >
                  {historyLoading && (
                    <div className="flex items-center gap-1 px-2 py-1.5 text-[11px] text-muted-foreground">
                      <Loader2 className="size-3 animate-spin" /> {t("etcd.detail.historyLoading")}
                    </div>
                  )}
                  {!historyLoading && (
                    <>
                      <button
                        type="button"
                        role="option"
                        aria-selected={revision === 0}
                        className={`flex w-full items-center justify-between gap-2 rounded px-2 py-1 text-left text-[11px] hover:bg-accent ${
                          revision === 0 ? "bg-accent text-accent-foreground" : ""
                        }`}
                        onClick={pickLatest}
                      >
                        <span>{t("etcd.detail.historyLatest")}</span>
                        {historyItems.length > 0 && (
                          <span className="font-mono text-[10px] text-muted-foreground">
                            rev {Number(historyItems[0].modRevision)}
                          </span>
                        )}
                      </button>
                      {historyItems.length > 1 ? (
                        historyItems.slice(1).map((kv) => {
                          const mod = Number(kv.modRevision);
                          const ver = Number(kv.version);
                          return (
                            <button
                              key={mod}
                              type="button"
                              role="option"
                              aria-selected={revision === mod}
                              className={`flex w-full items-center gap-2 rounded px-2 py-1 text-left text-[11px] hover:bg-accent ${
                                revision === mod ? "bg-accent text-accent-foreground" : ""
                              }`}
                              onClick={() => pickHistoryItem(kv)}
                            >
                              <History className="size-3 text-muted-foreground" />
                              <span className="font-mono">{t("etcd.detail.historyItem", { rev: mod, ver })}</span>
                            </button>
                          );
                        })
                      ) : (
                        <div className="px-2 py-1 text-[10px] italic text-muted-foreground">
                          {t("etcd.detail.historyEmpty")}
                        </div>
                      )}
                    </>
                  )}
                </div>
              )}
            </div>
            <Button
              size="sm"
              variant="outline"
              onClick={() => void copyToClipboard(detail.value ?? "")}
              data-testid="etcd-detail-copy-value"
            >
              <Copy className="size-3" /> {t("etcd.detail.copyValue")}
            </Button>
            <div className="flex-1" />
            <Button
              size="sm"
              variant="destructive"
              onClick={() => void handleDelete()}
              disabled={deleting}
              data-testid="etcd-detail-delete"
            >
              <Trash2 className="size-3" /> {t("etcd.detail.delete")}
            </Button>
          </>
        )}
      </div>
    </div>
  );
}

function MetaCol({
  label,
  value,
  muted,
  accent,
  testId,
}: {
  label: string;
  value: string;
  muted?: boolean;
  accent?: "purple" | "muted";
  testId?: string;
}) {
  const valueClass = accent === "purple" ? "text-syntax-number" : muted ? "text-muted-foreground" : "text-foreground";
  return (
    <div className="flex flex-col gap-0.5" data-testid={testId}>
      <span className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">{label}</span>
      <span className={`font-mono text-[12px] font-medium ${valueClass}`}>{value}</span>
    </div>
  );
}
