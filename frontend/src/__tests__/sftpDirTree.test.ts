import { describe, expect, it } from "vitest";
import { sftp_svc } from "../../wailsjs/go/models";
import { addExpandedPath, flattenSftpTree, SFTP_EXPANDED_PATH_LIMIT, type SftpTreeNode } from "../lib/sftpDirTree";

function entry(name: string, isDir = false): sftp_svc.FileEntry {
  return { name, isDir, size: 1, modTime: 0 } as sftp_svc.FileEntry;
}

function node(entries: sftp_svc.FileEntry[], partial: Partial<SftpTreeNode> = {}): SftpTreeNode {
  return { entries, loaded: true, loading: false, error: null, ...partial };
}

describe("flattenSftpTree", () => {
  it("renders the root layer with directories first and depth 0", () => {
    const tree = { "/srv": node([entry("app.log"), entry("conf.d", true)]) };

    const rows = flattenSftpTree(tree, new Set(), "/srv");

    expect(rows.map((row) => [row.name, row.depth, row.state])).toEqual([
      ["conf.d", 0, "entry"],
      ["app.log", 0, "entry"],
    ]);
    expect(rows[0].path).toBe("/srv/conf.d");
  });

  it("inserts the children of an expanded directory one level deeper right below it", () => {
    const tree = {
      "/srv": node([entry("conf.d", true), entry("logs", true)]),
      "/srv/conf.d": node([entry("default.conf")]),
      "/srv/logs": node([entry("access.log")]),
    };

    const rows = flattenSftpTree(tree, new Set(["/srv/conf.d"]), "/srv");

    expect(rows.map((row) => [row.path, row.depth])).toEqual([
      ["/srv/conf.d", 0],
      ["/srv/conf.d/default.conf", 1],
      ["/srv/logs", 0],
    ]);
    expect(rows[0].expanded).toBe(true);
    expect(rows[2].expanded).toBe(false);
  });

  it("marks a directory that is still fetching its first layer with a loading child row", () => {
    const tree = {
      "/srv": node([entry("conf.d", true)]),
      "/srv/conf.d": node([], { loaded: false, loading: true }),
    };

    const rows = flattenSftpTree(tree, new Set(["/srv/conf.d"]), "/srv");

    expect(rows[0].loading).toBe(true);
    expect(rows[1]).toMatchObject({ state: "loading", depth: 1, path: "/srv/conf.d", entry: null });
  });

  it("keeps a failed layer visible as an error row carrying its reason", () => {
    const tree = {
      "/srv": node([entry("conf.d", true), entry("logs", true)]),
      "/srv/conf.d": node([], { loaded: false, error: "permission denied" }),
    };

    const rows = flattenSftpTree(tree, new Set(["/srv/conf.d"]), "/srv");

    expect(rows[1]).toMatchObject({ state: "error", depth: 1, path: "/srv/conf.d", message: "permission denied" });
    expect(rows[2].path).toBe("/srv/logs");
  });

  it("distinguishes an empty loaded layer from one that never loaded", () => {
    const tree = {
      "/srv": node([entry("conf.d", true)]),
      "/srv/conf.d": node([]),
    };

    const rows = flattenSftpTree(tree, new Set(["/srv/conf.d"]), "/srv");

    expect(rows[1]).toMatchObject({ state: "empty", depth: 1, path: "/srv/conf.d" });
  });

  it("renders cached entries while the layer refreshes in the background", () => {
    const tree = {
      "/srv": node([entry("conf.d", true)]),
      "/srv/conf.d": node([entry("default.conf")], { loading: true }),
    };

    const rows = flattenSftpTree(tree, new Set(["/srv/conf.d"]), "/srv");

    expect(rows.map((row) => row.state)).toEqual(["entry", "entry"]);
    expect(rows[0].loading).toBe(true);
    expect(rows[1].path).toBe("/srv/conf.d/default.conf");
  });

  it("keeps the cached entries and appends the reason when a background refresh fails", () => {
    const tree = {
      "/srv": node([entry("conf.d", true)]),
      "/srv/conf.d": node([entry("default.conf")], { error: "connection lost" }),
    };

    const rows = flattenSftpTree(tree, new Set(["/srv/conf.d"]), "/srv");

    expect(rows.map((row) => [row.path, row.state])).toEqual([
      ["/srv/conf.d", "entry"],
      ["/srv/conf.d/default.conf", "entry"],
      ["/srv/conf.d", "error"],
    ]);
  });

  it("does not descend into a collapsed directory that still holds a cache", () => {
    const tree = {
      "/srv": node([entry("conf.d", true)]),
      "/srv/conf.d": node([entry("default.conf")]),
    };

    expect(flattenSftpTree(tree, new Set(), "/srv")).toHaveLength(1);
  });
});

describe("addExpandedPath", () => {
  it("evicts the least recently expanded path once the cap is reached", () => {
    let expanded = new Set<string>();
    for (let i = 0; i < SFTP_EXPANDED_PATH_LIMIT; i += 1) {
      expanded = addExpandedPath(expanded, `/d${i}`);
    }

    const next = addExpandedPath(expanded, "/newest");

    expect(next.size).toBe(SFTP_EXPANDED_PATH_LIMIT);
    expect(next.has("/d0")).toBe(false);
    expect(next.has("/d1")).toBe(true);
    expect(next.has("/newest")).toBe(true);
  });

  it("re-expanding a remembered path makes it the most recently used one", () => {
    let expanded = new Set<string>();
    for (let i = 0; i < SFTP_EXPANDED_PATH_LIMIT; i += 1) {
      expanded = addExpandedPath(expanded, `/d${i}`);
    }

    expanded = addExpandedPath(expanded, "/d0");
    const next = addExpandedPath(expanded, "/newest");

    expect(next.has("/d0")).toBe(true);
    expect(next.has("/d1")).toBe(false);
  });

  it("returns a new set and leaves the previous one untouched", () => {
    const expanded = new Set<string>(["/a"]);

    const next = addExpandedPath(expanded, "/b");

    expect([...expanded]).toEqual(["/a"]);
    expect([...next]).toEqual(["/a", "/b"]);
  });
});
