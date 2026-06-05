export interface ContextMenuPositionInput {
  anchorX: number;
  anchorY: number;
  width: number;
  height: number;
  viewportWidth: number;
  viewportHeight: number;
  gap?: number;
  padding?: number;
}

export interface ContextMenuPosition {
  left: number;
  top: number;
  horizontal: "right" | "left";
  vertical: "down" | "up";
}

export function computeContextMenuPosition({
  anchorX,
  anchorY,
  width,
  height,
  viewportWidth,
  viewportHeight,
  gap = 4,
  padding = 8,
}: ContextMenuPositionInput): ContextMenuPosition {
  const right = anchorX + gap;
  const left = anchorX - width - gap;
  const down = anchorY + gap;
  const up = anchorY - height - gap;

  const fitsRight = right + width <= viewportWidth - padding;
  const fitsLeft = left >= padding;
  const fitsDown = down + height <= viewportHeight - padding;
  const fitsUp = up >= padding;

  const horizontal = !fitsRight && fitsLeft ? "left" : "right";
  const vertical = !fitsDown && fitsUp ? "up" : "down";

  return {
    left: horizontal === "left" ? left : right,
    top: vertical === "up" ? up : down,
    horizontal,
    vertical,
  };
}
