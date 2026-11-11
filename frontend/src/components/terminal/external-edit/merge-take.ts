import { buildTextDiffBlocks, type TextDiffBlock } from "@/lib/textDiffBlocks";

// 与 buildTextDiffBlocks 的分行规则一致：先归一化换行再按 \n 切分。
function splitLines(value: string): string[] {
  const normalized = value.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
  return normalized.length === 0 ? [] : normalized.split("\n");
}

// 块在某一侧的位置：前面有多少行、自身占哪些行。缺失的一侧行数为 0，
// buildTextDiffBlocks 此时把 start/end 记为「前面的行数」。
function sideSpan(block: TextDiffBlock, side: "original" | "modified") {
  const present = side === "original" ? block.kind !== "insert" : block.kind !== "delete";
  const start = side === "original" ? block.originalStartLine : block.modifiedStartLine;
  const end = side === "original" ? block.originalEndLine : block.modifiedEndLine;
  return present ? { before: start - 1, length: end - start + 1 } : { before: start, length: 0 };
}

// from 中未改动的行下标 → 它在 to 中的行下标
function unchangedLineMap(from: string, to: string): Map<number, number> {
  const map = new Map<number, number>();
  let i = 0;
  let j = 0;
  for (const block of buildTextDiffBlocks(from, to)) {
    const original = sideSpan(block, "original");
    const modified = sideSpan(block, "modified");
    while (i < original.before) map.set(i++, j++);
    i = original.before + original.length;
    j = modified.before + modified.length;
  }
  const fromLength = splitLines(from).length;
  while (i < fromLength) map.set(i++, j++);
  return map;
}

/**
 * 把合并窗里某个差异块（remote=original、local=modified）在最终稿中的那一段换成本地或远端的内容。
 * 最终稿可能已被手动编辑：以块两侧未改动的行为锚点，在最终稿里定位这一段；
 * 锚点本身被改动而无法定位时返回 null，由调用方提示用户手动编辑。
 */
export function takeMergeBlock({
  final,
  local,
  remote,
  block,
  side,
}: {
  final: string;
  local: string;
  remote: string;
  block: TextDiffBlock;
  side: "local" | "remote";
}): string | null {
  const localLines = splitLines(local);
  const finalLines = splitLines(final);
  const localSpan = sideSpan(block, "modified");
  const toFinal = unchangedLineMap(local, final);

  const prevAnchor = localSpan.before - 1;
  const nextAnchor = localSpan.before + localSpan.length;
  const prevInFinal = prevAnchor < 0 ? -1 : toFinal.get(prevAnchor);
  const nextInFinal = nextAnchor >= localLines.length ? finalLines.length : toFinal.get(nextAnchor);
  if (prevInFinal === undefined || nextInFinal === undefined) return null;

  const [sourceLines, span] =
    side === "local" ? [localLines, localSpan] : [splitLines(remote), sideSpan(block, "original")];
  const chosen = sourceLines.slice(span.before, span.before + span.length);

  const eol = final.includes("\r\n") ? "\r\n" : "\n";
  return [...finalLines.slice(0, prevInFinal + 1), ...chosen, ...finalLines.slice(nextInFinal)].join(eol);
}
