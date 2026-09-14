import type { ExtraHeaderValue } from "./AIProviderForm";

/**
 * OpsKat 自己写在请求上的头。用户配了同名的只会被后端拒掉（见
 * internal/app/ai/provider.go 的 reservedHeaderNames），在这里先拦一道，
 * 让他不用等到保存失败才知道。
 */
export const RESERVED_HEADER_NAMES = ["authorization", "x-api-key", "anthropic-version", "content-type"];

/** 被更靠前的同名条目遮住的行下标。请求头名大小写不敏感，重名只有一条会生效。 */
export function shadowedHeaderIndexes(headers: ExtraHeaderValue[]): Set<number> {
  const seen = new Set<string>();
  const shadowed = new Set<number>();
  headers.forEach((header, index) => {
    const key = header.name.trim().toLowerCase();
    if (!key) return;
    if (seen.has(key)) shadowed.add(index);
    seen.add(key);
  });
  return shadowed;
}

/** 表单能否保存：任一条目重名或占用保留头就不行。 */
export function hasExtraHeaderError(headers: ExtraHeaderValue[]): boolean {
  const shadowed = shadowedHeaderIndexes(headers);
  return headers.some(
    (header, index) => shadowed.has(index) || RESERVED_HEADER_NAMES.includes(header.name.trim().toLowerCase())
  );
}
