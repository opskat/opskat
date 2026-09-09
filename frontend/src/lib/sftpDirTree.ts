// SFTP 目录树的纯模型：每层条目由 SFTPListDir 按目录懒填进 tree，flatten 只负责把
// tree + expanded 展平成带缩进的渲染行（借 ossPrefixTree.flattenPrefixTree 的
// 「tree record + expanded set + 每节点 loaded」范式，不像 redisKeyTree 那样一次性全量建树）。
import { sftp_svc } from "../../wailsjs/go/models";
import { getChildPath, sortEntries } from "@/components/terminal/file-manager/utils";

export interface SftpTreeNode {
  entries: sftp_svc.FileEntry[];
  /** 该层至少成功加载过一次；为 true 时即使正在后台刷新也继续渲染 entries。 */
  loaded: boolean;
  loading: boolean;
  error: string | null;
}

/** entry=真实条目行；其余三种是某个展开目录的子层占位行，用来区分「加载中 / 空目录 / 加载失败」。 */
export type SftpTreeRowState = "entry" | "loading" | "empty" | "error";

export interface SftpTreeRow {
  depth: number;
  /** entry 行是条目自身的绝对路径；占位行是所属目录的绝对路径（重试用它）。 */
  path: string;
  name: string;
  entry: sftp_svc.FileEntry | null;
  state: SftpTreeRowState;
  /** 目录 entry 行是否展开。 */
  expanded: boolean;
  /** 目录 entry 行的子层是否正在请求（含后台刷新）。 */
  loading: boolean;
  /** error 占位行的失败原因。 */
  message: string | null;
}

export interface SftpTreeEntryRow extends SftpTreeRow {
  entry: sftp_svc.FileEntry;
  state: "entry";
}

export function isSftpTreeEntryRow(row: SftpTreeRow): row is SftpTreeEntryRow {
  return row.state === "entry" && row.entry !== null;
}

/** 展开态按绝对路径记忆并跨换根保留，因此需要上限；超出后淘汰最久未展开的条目。 */
export const SFTP_EXPANDED_PATH_LIMIT = 200;

/**
 * 把 path 记为「最近展开」并加入展开集：Set 保持插入序，先删后插即把它移到队尾，
 * 超出上限时从队首（最久未展开）淘汰。返回新的 Set，便于直接作为 React state。
 */
export function addExpandedPath(expanded: Set<string>, path: string, limit = SFTP_EXPANDED_PATH_LIMIT): Set<string> {
  const next = new Set(expanded);
  next.delete(path);
  next.add(path);
  while (next.size > limit) {
    const oldest = next.values().next().value;
    if (oldest === undefined) break;
    next.delete(oldest);
  }
  return next;
}

function placeholderRow(dirPath: string, depth: number, state: SftpTreeRowState, message: string | null): SftpTreeRow {
  return { depth, path: dirPath, name: "", entry: null, state, expanded: true, loading: false, message };
}

/** 把 rootPath 这一层及其所有已展开子层展平成可渲染的行序（深度优先，父行在前）。 */
export function flattenSftpTree(
  tree: Record<string, SftpTreeNode>,
  expanded: Set<string>,
  rootPath: string
): SftpTreeRow[] {
  const rows: SftpTreeRow[] = [];

  const walk = (dirPath: string, depth: number, ancestors: Set<string>) => {
    const node = tree[dirPath];
    if (!node) return;
    for (const entry of sortEntries(node.entries)) {
      const childPath = getChildPath(dirPath, entry.name);
      const childNode = tree[childPath];
      const isExpanded = entry.isDir && expanded.has(childPath);
      rows.push({
        depth,
        path: childPath,
        name: entry.name,
        entry,
        state: "entry",
        expanded: isExpanded,
        loading: isExpanded && !!childNode?.loading,
        message: null,
      });
      if (!isExpanded || ancestors.has(childPath)) continue;
      const childDepth = depth + 1;
      if (childNode?.loaded) {
        // 已加载过的层永远先渲染缓存条目;刷新失败时把失败说明补在该层末尾,而不是吞掉。
        if (childNode.entries.length > 0) walk(childPath, childDepth, new Set([...ancestors, childPath]));
        else if (!childNode.error) rows.push(placeholderRow(childPath, childDepth, "empty", null));
        if (childNode.error) rows.push(placeholderRow(childPath, childDepth, "error", childNode.error));
        continue;
      }
      if (childNode?.error) {
        rows.push(placeholderRow(childPath, childDepth, "error", childNode.error));
        continue;
      }
      rows.push(placeholderRow(childPath, childDepth, "loading", null));
    }
  };

  walk(rootPath, 0, new Set([rootPath]));
  return rows;
}
