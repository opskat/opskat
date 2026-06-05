import { describe, expect, it } from "vitest";
import { computeContextMenuPosition } from "@opskat/ui";

describe("computeContextMenuPosition", () => {
  it("opens down and right when there is space", () => {
    expect(
      computeContextMenuPosition({
        anchorX: 20,
        anchorY: 30,
        width: 100,
        height: 80,
        viewportWidth: 500,
        viewportHeight: 400,
      })
    ).toMatchObject({ left: 24, top: 34, horizontal: "right", vertical: "down" });
  });

  it("opens left when the right side would overflow and the left side fits", () => {
    expect(
      computeContextMenuPosition({
        anchorX: 280,
        anchorY: 30,
        width: 100,
        height: 80,
        viewportWidth: 300,
        viewportHeight: 400,
      })
    ).toMatchObject({ left: 176, top: 34, horizontal: "left", vertical: "down" });
  });

  it("opens up when the bottom would overflow and the top fits", () => {
    expect(
      computeContextMenuPosition({
        anchorX: 20,
        anchorY: 280,
        width: 100,
        height: 80,
        viewportWidth: 500,
        viewportHeight: 300,
      })
    ).toMatchObject({ left: 24, top: 196, horizontal: "right", vertical: "up" });
  });

  it("keeps the default direction when neither side fully fits", () => {
    expect(
      computeContextMenuPosition({
        anchorX: 40,
        anchorY: 40,
        width: 100,
        height: 100,
        viewportWidth: 120,
        viewportHeight: 120,
      })
    ).toMatchObject({ left: 44, top: 44, horizontal: "right", vertical: "down" });
  });
});
