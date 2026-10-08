import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { create } from "zustand";
import { toast } from "sonner";
import { EventsOn } from "../../../wailsjs/runtime/runtime";
import type { extstore_svc } from "../../../wailsjs/go/models";
import type { CardInstall, StoreCard, StoreProgress } from "./ExtensionStore";

interface StoreInstallsState {
  /** Store installs in flight or failed, by extension name. */
  installs: Record<string, CardInstall>;
  setInstall: (name: string, next: CardInstall | null) => void;
  /** Applies an ext:store-progress report to the extension's running install. */
  progress: (p: StoreProgress) => void;
}

/**
 * The one set of store installs: the store cards and the Installed list's update
 * button both read it, so an install started in one view shows in the other and
 * survives switching between them.
 */
export const useStoreInstallState = create<StoreInstallsState>((set) => ({
  installs: {},
  setInstall: (name, next) =>
    set((s) => {
      const { [name]: _drop, ...rest } = s.installs;
      return { installs: next ? { ...rest, [name]: next } : rest };
    }),
  progress: (p) =>
    set((s) =>
      s.installs[p.name]?.status === "running"
        ? { installs: { ...s.installs, [p.name]: { status: "running", phase: p.phase, done: p.done, total: p.total } } }
        : s
    ),
}));

/**
 * Store installs (useStoreInstallState) plus an install action: onInstall runs the
 * store install flow, onLanded runs after an install actually landed.
 */
export function useStoreInstalls(
  onInstall: (card: StoreCard) => Promise<extstore_svc.InstallResult>,
  onLanded: () => Promise<void>
) {
  const { t } = useTranslation();
  const installs = useStoreInstallState((s) => s.installs);

  // Unsubscribe with this handler's own cancel: EventsOff(name) would also drop
  // the other mounted user of this event. Applying a report is idempotent, so two
  // mounted users applying the same one is harmless.
  useEffect(() => {
    const off = EventsOn("ext:store-progress", useStoreInstallState.getState().progress);
    return () => off?.();
  }, []);

  const install = async (card: StoreCard) => {
    const { setInstall } = useStoreInstallState.getState();
    setInstall(card.name, { status: "running", done: 0, total: card.size });
    let result: extstore_svc.InstallResult;
    try {
      result = await onInstall(card);
    } catch (e) {
      setInstall(card.name, null);
      toast.error(`${t("extension.installError")}: ${String(e)}`);
      return;
    }
    if (result.error) {
      setInstall(card.name, { status: "failed", error: result.error });
      return;
    }
    setInstall(card.name, null);
    if (result.canceled) return;
    // The install landed; failing to re-read the store afterwards is not an install error.
    await onLanded().catch((e) => toast.error(String(e)));
  };

  return { installs, install };
}
