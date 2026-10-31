// frontend/src/extension/slots.ts
// The slots a page may declare. The host defines them; internal/extreg.PageSlots is
// the Go side's independent copy, and extreg's TestPageSlotsMatchFrontend compares the two.
export const PAGE_SLOTS = ["asset.connect"] as const;

export type PageSlot = (typeof PAGE_SLOTS)[number];

/** The page opened by double-clicking an asset of the extension's type. */
export const SLOT_ASSET_CONNECT: PageSlot = "asset.connect";
