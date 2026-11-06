// The pinyin matcher (lazy dictionary load + per-string cache) lives in @opskat/ui; the
// app re-exports it so both share one implementation and one cache.
export { pinyinMatch } from "@opskat/ui";
