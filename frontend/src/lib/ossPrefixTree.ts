// 懒前缀树纯模型：节点子前缀按层从 OSSListObjects(prefix).prefixes 填入，
// flatten 只负责把 tree + expanded 展平成带缩进的渲染行（借 redisKeyTree.flattenTree 的
// depth-walk / expandedSet 范式，但绝不一次性 buildKeyTree 全量建树）。

export interface OssPrefixNode {
  childPrefixes: string[];
  loaded: boolean;
  cursor: string;
  truncated: boolean;
}

export interface OssPrefixRow {
  depth: number;
  name: string;
  prefix: string;
  isExpanded: boolean;
  loaded: boolean;
}

/** "a/b/c/" -> "c"；"a/" -> "a"；"" -> ""。 */
export function prefixLeafName(prefix: string): string {
  const trimmed = prefix.endsWith("/") ? prefix.slice(0, -1) : prefix;
  const idx = trimmed.lastIndexOf("/");
  return idx >= 0 ? trimmed.slice(idx + 1) : trimmed;
}

export function flattenPrefixTree(
  tree: Record<string, OssPrefixNode>,
  expanded: Set<string>,
  rootPrefix = ""
): OssPrefixRow[] {
  const rows: OssPrefixRow[] = [];
  const walk = (parentPrefix: string, depth: number) => {
    const node = tree[parentPrefix];
    if (!node) return;
    for (const childPrefix of node.childPrefixes) {
      const isExpanded = expanded.has(childPrefix);
      const childNode = tree[childPrefix];
      rows.push({
        depth,
        name: prefixLeafName(childPrefix),
        prefix: childPrefix,
        isExpanded,
        loaded: childNode?.loaded ?? false,
      });
      // 只有 expanded 且子节点已懒填才继续下钻；expanded 但未 loaded 的节点先只画自己。
      if (isExpanded && childNode?.loaded) walk(childPrefix, depth + 1);
    }
  };
  walk(rootPrefix, 0);
  return rows;
}

export interface OssCrumb {
  label: string;
  prefix: string;
  isCurrent: boolean;
}

/** bucket + "a/b/" -> [bucket(""), a("a/"), b("a/b/")]，最后一段为当前。 */
export function crumbSegments(bucket: string, prefix: string): OssCrumb[] {
  const parts = prefix.split("/").filter(Boolean);
  const crumbs: OssCrumb[] = [{ label: bucket, prefix: "", isCurrent: parts.length === 0 }];
  let acc = "";
  parts.forEach((part, i) => {
    acc += `${part}/`;
    crumbs.push({ label: part, prefix: acc, isCurrent: i === parts.length - 1 });
  });
  return crumbs;
}
