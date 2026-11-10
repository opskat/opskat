import { describe, expect, it } from "vitest";
import { buildTextDiffBlocks } from "@/lib/textDiffBlocks";
import { takeMergeBlock } from "../merge-take";

// 与合并窗一致：remote 作 original、local 作 modified。
function blocksOf(local: string, remote: string) {
  return buildTextDiffBlocks(remote, local);
}

describe("takeMergeBlock", () => {
  const local = "a\nlocal-1\nb\nc\nlocal-2\nd\n";
  const remote = "a\nremote-1\nb\nc\nremote-2\nd\n";

  it("replaces only the chosen block in the final draft with the remote lines", () => {
    const blocks = blocksOf(local, remote);
    expect(blocks).toHaveLength(2);

    const final = takeMergeBlock({ final: local, local, remote, block: blocks[1], side: "remote" });

    expect(final).toBe("a\nlocal-1\nb\nc\nremote-2\nd\n");
  });

  it("switches a block back to local after it was taken from remote", () => {
    const blocks = blocksOf(local, remote);
    const tookRemote = takeMergeBlock({ final: local, local, remote, block: blocks[0], side: "remote" });

    const back = takeMergeBlock({ final: tookRemote!, local, remote, block: blocks[0], side: "local" });

    expect(back).toBe(local);
  });

  it("keeps manual edits elsewhere in the final draft", () => {
    const blocks = blocksOf(local, remote);
    const edited = "a\nlocal-1\nb\nhand-written\nc\nlocal-2\nd\n";

    const final = takeMergeBlock({ final: edited, local, remote, block: blocks[1], side: "remote" });

    expect(final).toBe("a\nlocal-1\nb\nhand-written\nc\nremote-2\nd\n");
  });

  it("applies blocks that exist on one side only", () => {
    const localOnly = "a\nadded-locally\nb\n";
    const remoteOnly = "a\nb\nadded-remotely\n";
    const blocks = blocksOf(localOnly, remoteOnly);
    expect(blocks.map((block) => block.kind)).toEqual(["insert", "delete"]);

    const dropLocalInsert = takeMergeBlock({
      final: localOnly,
      local: localOnly,
      remote: remoteOnly,
      block: blocks[0],
      side: "remote",
    });
    expect(dropLocalInsert).toBe("a\nb\n");

    const addRemoteLine = takeMergeBlock({
      final: localOnly,
      local: localOnly,
      remote: remoteOnly,
      block: blocks[1],
      side: "remote",
    });
    expect(addRemoteLine).toBe("a\nadded-locally\nb\nadded-remotely\n");
  });

  it("keeps CRLF line endings of the final draft", () => {
    const crlfLocal = "a\r\nlocal\r\nb\r\n";
    const crlfRemote = "a\r\nremote\r\nb\r\n";
    const [block] = blocksOf(crlfLocal, crlfRemote);

    expect(takeMergeBlock({ final: crlfLocal, local: crlfLocal, remote: crlfRemote, block, side: "remote" })).toBe(
      "a\r\nremote\r\nb\r\n"
    );
  });

  it("returns null when the lines around the block were edited by hand and the block cannot be located", () => {
    const blocks = blocksOf(local, remote);
    const boundaryEdited = "a\nlocal-1\nb\nC-edited\nlocal-2\nd-edited\n";

    expect(takeMergeBlock({ final: boundaryEdited, local, remote, block: blocks[1], side: "remote" })).toBeNull();
  });
});
