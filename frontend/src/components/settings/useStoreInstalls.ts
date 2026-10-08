import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { EventsOn } from "../../../wailsjs/runtime/runtime";
import type { extstore_svc } from "../../../wailsjs/go/models";
import type { CardInstall, StoreCard, StoreProgress } from "./ExtensionStore";

/**
 * Store installs in flight or failed, per extension: the install handler's
 * progress (ext:store-progress) and failure, shared by the store cards and the
 * Installed list's update button. onLanded runs after an install actually landed.
 */
export function useStoreInstalls(
  onInstall: (card: StoreCard) => Promise<extstore_svc.InstallResult>,
  onLanded: () => Promise<void>
) {
  const { t } = useTranslation();
  const [installs, setInstalls] = useState<Record<string, CardInstall>>({});

  const setInstall = useCallback((name: string, next: CardInstall | null) => {
    setInstalls((prev) => {
      const { [name]: _drop, ...rest } = prev;
      return next ? { ...rest, [name]: next } : rest;
    });
  }, []);

  // Unsubscribe with this handler's own cancel: EventsOff(name) would also drop
  // the other mounted user of this event.
  useEffect(() => {
    const off = EventsOn("ext:store-progress", (p: StoreProgress) => {
      setInstalls((prev) =>
        prev[p.name]?.status === "running"
          ? { ...prev, [p.name]: { status: "running", phase: p.phase, done: p.done, total: p.total } }
          : prev
      );
    });
    return () => off?.();
  }, []);

  const install = async (card: StoreCard) => {
    setInstall(card.name, { status: "running", done: 0, total: card.size });
    try {
      const result = await onInstall(card);
      if (result.error) {
        setInstall(card.name, { status: "failed", error: result.error });
        return;
      }
      setInstall(card.name, null);
      if (!result.canceled) await onLanded();
    } catch (e) {
      setInstall(card.name, null);
      toast.error(`${t("extension.installError")}: ${String(e)}`);
    }
  };

  return { installs, install };
}
