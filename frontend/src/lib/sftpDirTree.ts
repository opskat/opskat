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

export function isSftpTreeEntryRow<T extends SftpTreeRow>(
  row: T
): row is T & { entry: sftp_svc.FileEntry; state: "entry" } {
  return row.state === "entry" && row.entry !== null;
}

/** 折叠链上的一段：自己的绝对路径、名字与展开/加载态，供该段独立展开或换根。 */
export interface SftpTreeChainSegment {
  path: string;
  name: string;
  entry: sftp_svc.FileEntry;
  expanded: boolean;
  loading: boolean;
}

export interface SftpTreeDisplayRow extends SftpTreeRow {
  /** 折叠出的单子目录链(长度 >= 2)时携带每一段;否则为 null,渲染与普通行一致。 */
  chain: SftpTreeChainSegment[] | null;
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

/**
 * 把无文件的单子目录链折叠成一行:depth-N 父行的唯一直接子行若是目录且不带任何兄弟
 * (包括加载中/空/失败占位行),就并入父行的 chain,链继续沿同样规则往下试探,直到碰到
 * 0/多个直接子行、子行是文件、或子行本身未展开(没有可供合并的下一行)为止。链上除最后
 * 一段外必然都已展开(否则 flattenSftpTree 根本不会产出它们的子行)。合并后的行以链末段
 * 的 path/name/entry/expanded/loading 呈现,depth 取链首段的原始 depth;末段自己的子行
 * (若有)在输出里整体上提一级,与链被压成一行的深度对齐。
 */
export function collapseSingleChildChains(rows: SftpTreeRow[]): SftpTreeDisplayRow[] {
  const out: SftpTreeDisplayRow[] = [];

  const directChildIndices = (idx: number): number[] => {
    const parentDepth = rows[idx].depth;
    const children: number[] = [];
    let j = idx + 1;
    while (j < rows.length && rows[j].depth > parentDepth) {
      if (rows[j].depth === parentDepth + 1) children.push(j);
      j++;
    }
    return children;
  };

  const toSegment = (idx: number): SftpTreeChainSegment => {
    const row = rows[idx];
    // isSoleDirLink() 只把满足链条件的行送进这里,entry 必然非空。
    return {
      path: row.path,
      name: row.name,
      entry: row.entry as sftp_svc.FileEntry,
      expanded: row.expanded,
      loading: row.loading,
    };
  };

  const isSoleDirChild = (children: number[]): boolean =>
    children.length === 1 && rows[children[0]].state === "entry" && !!rows[children[0]].entry?.isDir;

  const processSiblings = (indices: number[], outDepth: number) => {
    for (const idx of indices) processChain(idx, outDepth);
  };

  const processChain = (startIdx: number, outDepth: number) => {
    const chainIdxs = [startIdx];
    let children = directChildIndices(startIdx);
    while (isSoleDirChild(children)) {
      chainIdxs.push(children[0]);
      children = directChildIndices(children[0]);
    }
    const lastRow = rows[chainIdxs[chainIdxs.length - 1]];
    out.push({
      ...lastRow,
      depth: outDepth,
      chain: chainIdxs.length > 1 ? chainIdxs.map(toSegment) : null,
    });
    processSiblings(children, outDepth + 1);
  };

  const topLevel = rows.map((_, idx) => idx).filter((idx) => rows[idx].depth === 0);
  processSiblings(topLevel, 0);
  return out;
}
