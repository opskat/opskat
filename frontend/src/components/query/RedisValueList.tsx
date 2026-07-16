import { Loader2 } from "lucide-react";
import { Button } from "@opskat/ui";

export type RedisValueTranslator = (key: string, opts?: Record<string, unknown>) => string;

export function RedisValueCount({ loaded, total, t }: { loaded: number; total: number; t: RedisValueTranslator }) {
  const label = total >= 0 ? t("query.loadedOfTotal", { loaded, total }) : `${loaded}`;

  return <div className="shrink-0 px-2 py-1.5 text-xs text-muted-foreground">{label}</div>;
}

export function RedisLoadMoreFooter({
  loading,
  onLoadMore,
  t,
}: {
  loading: boolean;
  onLoadMore: () => void;
  t: RedisValueTranslator;
}) {
  return (
    <div className="border-t px-2 py-1.5">
      <Button variant="ghost" size="sm" className="h-7 w-full text-xs" onClick={onLoadMore} disabled={loading}>
        {loading ? <Loader2 className="mr-1 size-3 animate-spin" /> : null}
        {t("query.loadMore")}
      </Button>
    </div>
  );
}
