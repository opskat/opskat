import { describe, expect, it } from "vitest";
import { sftp_svc } from "../../wailsjs/go/models";
import {
  addExpandedPath,
  collapseSingleChildChains,
  flattenFolderTree,
  flattenSftpTree,
  folderRevealPaths,
  rebaseCurrentPath,
  SFTP_EXPANDED_PATH_LIMIT,
  type SftpTreeNode,
} from "../lib/sftpDirTree";

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

describe("collapseSingleChildChains", () => {
  it("merges a file-less single-child directory chain into one row carrying every segment", () => {
    const tree = {
      "/root": node([entry("a", true)]),
      "/root/a": node([entry("b", true)]),
      "/root/a/b": node([entry("c", true)]),
      "/root/a/b/c": node([entry("d", true), entry("file.txt")]),
    };
    const expanded = new Set(["/root/a", "/root/a/b", "/root/a/b/c"]);

    const rows = collapseSingleChildChains(flattenSftpTree(tree, expanded, "/root"));

    expect(rows).toHaveLength(3); // merged chain row + c's two children
    const [chainRow, dirChild, fileChild] = rows;
    expect(chainRow.depth).toBe(0);
    expect(chainRow.path).toBe("/root/a/b/c");
    expect(chainRow.chain?.map((seg) => seg.path)).toEqual(["/root/a", "/root/a/b", "/root/a/b/c"]);
    expect(chainRow.chain?.map((seg) => seg.name)).toEqual(["a", "b", "c"]);
    // c's own children render one level below the merged row, not at their original depth 3.
    expect(dirChild).toMatchObject({ path: "/root/a/b/c/d", depth: 1, chain: null });
    expect(fileChild).toMatchObject({ path: "/root/a/b/c/file.txt", depth: 1, chain: null });
  });

  it("does not merge a directory that holds more than one entry", () => {
    const tree = {
      "/root": node([entry("a", true)]),
      "/root/a": node([entry("b", true), entry("note.txt")]),
    };
    const expanded = new Set(["/root/a"]);

    const rows = collapseSingleChildChains(flattenSftpTree(tree, expanded, "/root"));

    expect(rows.map((row) => [row.path, row.depth, row.chain])).toEqual([
      ["/root/a", 0, null],
      ["/root/a/b", 1, null],
      ["/root/a/note.txt", 1, null],
    ]);
  });

  it("does not merge when the sole child is a file rather than a directory", () => {
    const tree = {
      "/root": node([entry("a", true)]),
      "/root/a": node([entry("only.txt")]),
    };
    const expanded = new Set(["/root/a"]);

    const rows = collapseSingleChildChains(flattenSftpTree(tree, expanded, "/root"));

    expect(rows.map((row) => row.chain)).toEqual([null, null]);
  });

  it("stops the chain at a directory that is not itself expanded", () => {
    const tree = {
      "/root": node([entry("a", true)]),
      "/root/a": node([entry("b", true)]),
    };
    const expanded = new Set(["/root/a"]); // "b" exists but was never expanded

    const rows = collapseSingleChildChains(flattenSftpTree(tree, expanded, "/root"));

    expect(rows).toHaveLength(1);
    expect(rows[0].chain?.map((seg) => seg.path)).toEqual(["/root/a", "/root/a/b"]);
  });

  it("leaves an ordinary multi-child directory as a plain row", () => {
    const tree = { "/root": node([entry("a", true), entry("readme.md")]) };

    const rows = collapseSingleChildChains(flattenSftpTree(tree, new Set(), "/root"));

    expect(rows.map((row) => [row.path, row.chain])).toEqual([
      ["/root/a", null],
      ["/root/readme.md", null],
    ]);
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

describe("flattenFolderTree", () => {
  it("starts at / and lists only the directories of each expanded layer", () => {
    const tree = {
      "/": node([entry("swap.img"), entry("var", true), entry("etc", true)]),
      "/etc": node([entry("hosts"), entry("nginx", true)]),
      "/var": node([entry("log", true)]),
    };

    const rows = flattenFolderTree(tree, new Set(["/", "/etc"]));

    expect(rows.map((row) => [row.path, row.name, row.depth])).toEqual([
      ["/", "/", 0],
      ["/etc", "etc", 1],
      ["/etc/nginx", "nginx", 2],
      ["/var", "var", 1],
    ]);
    expect(rows.map((row) => row.expanded)).toEqual([true, true, false, false]);
  });

  it("drops the arrow only for a directory whose loaded layer has no subdirectories", () => {
    const tree = {
      "/": node([entry("etc", true), entry("tmp", true), entry("var", true)]),
      "/tmp": node([entry("a.txt")]),
      "/var": node([entry("log", true)]),
    };

    const rows = flattenFolderTree(tree, new Set(["/"]));

    expect(rows.filter((row) => row.depth === 1).map((row) => [row.name, row.leaf])).toEqual([
      ["etc", false],
      ["tmp", true],
      ["var", false],
    ]);
  });

  it("shows a loading row under an expanded directory that has not loaded yet", () => {
    const tree = {
      "/": node([entry("var", true)]),
      "/var": node([], { loaded: false, loading: true }),
    };

    const rows = flattenFolderTree(tree, new Set(["/", "/var"]));

    expect(rows[1]).toMatchObject({ path: "/var", loading: true, state: "dir" });
    expect(rows[2]).toMatchObject({ state: "loading", depth: 2, path: "/var" });
  });

  it("shows a loading row for / itself before its first layer arrives", () => {
    const rows = flattenFolderTree({}, new Set(["/"]));

    expect(rows.map((row) => [row.state, row.path, row.depth])).toEqual([
      ["dir", "/", 0],
      ["loading", "/", 1],
    ]);
  });

  it("keeps a failed layer as an error row with its reason while siblings stay listed", () => {
    const tree = {
      "/": node([entry("root", true), entry("srv", true)]),
      "/root": node([], { loaded: false, error: "permission denied" }),
    };

    const rows = flattenFolderTree(tree, new Set(["/", "/root"]));

    expect(rows.map((row) => [row.state, row.path])).toEqual([
      ["dir", "/"],
      ["dir", "/root"],
      ["error", "/root"],
      ["dir", "/srv"],
    ]);
    expect(rows[2].message).toBe("permission denied");
  });

  it("keeps cached subdirectories visible and appends the error when a refresh of that layer fails", () => {
    const tree = {
      "/": node([entry("etc", true)]),
      "/etc": node([entry("nginx", true)], { error: "connection lost" }),
    };

    const rows = flattenFolderTree(tree, new Set(["/", "/etc"]));

    expect(rows.map((row) => [row.state, row.path])).toEqual([
      ["dir", "/"],
      ["dir", "/etc"],
      ["dir", "/etc/nginx"],
      ["error", "/etc"],
    ]);
  });
});

describe("folderRevealPaths", () => {
  it("returns / plus every ancestor above the current directory, not the directory itself", () => {
    expect(folderRevealPaths("/etc/nginx/conf.d")).toEqual(["/", "/etc", "/etc/nginx"]);
  });

  it("expands / itself when / is the current directory", () => {
    expect(folderRevealPaths("/")).toEqual(["/"]);
  });

  it("expands only / for a top-level directory", () => {
    expect(folderRevealPaths("/srv")).toEqual(["/"]);
  });
});

describe("rebaseCurrentPath", () => {
  it("leaves the current directory alone when the change is elsewhere or below it", () => {
    expect(rebaseCurrentPath("/srv/app", [{ from: "/srv/www", to: null }])).toBeNull();
    expect(rebaseCurrentPath("/srv/app", [{ from: "/srv/app/logs", to: "/srv/app/old" }])).toBeNull();
    expect(rebaseCurrentPath("/srv/app", [{ from: "/srv/ap", to: null }])).toBeNull();
  });

  it("moves to the parent of a deleted current directory or ancestor", () => {
    expect(rebaseCurrentPath("/srv/app", [{ from: "/srv/app", to: null }])).toBe("/srv");
    expect(rebaseCurrentPath("/srv/app/logs", [{ from: "/srv", to: null }])).toBe("/");
  });

  it("keeps climbing when several deleted directories are nested", () => {
    expect(
      rebaseCurrentPath("/a/b/c", [
        { from: "/a/b", to: null },
        { from: "/a", to: null },
      ])
    ).toBe("/");
  });

  it("follows a renamed or moved current directory or ancestor to its new place", () => {
    expect(rebaseCurrentPath("/srv/app", [{ from: "/srv/app", to: "/srv/api" }])).toBe("/srv/api");
    expect(rebaseCurrentPath("/srv/app/logs", [{ from: "/srv", to: "/opt/srv" }])).toBe("/opt/srv/app/logs");
  });
});
