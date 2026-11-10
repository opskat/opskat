import { useState } from "react";
import { useAsyncList, type ListLoad } from "./useAsyncList";
import { ListCredentialsByType } from "../../../wailsjs/go/system/System";
import { asset_entity, credential_entity } from "../../../wailsjs/go/models";
import {
  CREDENTIAL_DEFAULTS,
  initCredentialFromConfig,
  type CredentialFragment,
  type CredentialState,
} from "./credentialConfig";

export interface UseAssetCredential {
  value: CredentialState;
  managedPasswords: credential_entity.Credential[];
  /** Load state of `managedPasswords`; pass to `PasswordSourceField` so a failed load isn't shown as "none". */
  managedPasswordsLoad: ListLoad;
  setPassword: (v: string) => void;
  setPasswordSource: (v: "inline" | "managed") => void;
  setPasswordCredentialId: (v: number) => void;
}

/** 密码/托管密码 section 共享:自持凭据子状态 + 加载可选密码凭据列表 + 编辑态回填。 */
export function useAssetCredential(
  editAsset?: asset_entity.Asset,
  initialCredentialConfig?: CredentialFragment
): UseAssetCredential {
  const [value, setValue] = useState<CredentialState>(() => {
    if (!editAsset) return { ...CREDENTIAL_DEFAULTS };
    if (initialCredentialConfig) return initCredentialFromConfig(initialCredentialConfig);
    try {
      return initCredentialFromConfig(JSON.parse(editAsset.Config || "{}"));
    } catch {
      return { ...CREDENTIAL_DEFAULTS };
    }
  });
  const { items: managedPasswords, load: managedPasswordsLoad } = useAsyncList<credential_entity.Credential>(() =>
    ListCredentialsByType("password")
  );

  const patch = (p: Partial<CredentialState>) => setValue((s) => ({ ...s, ...p }));
  return {
    value,
    managedPasswords,
    managedPasswordsLoad,
    setPassword: (v) => patch({ password: v }),
    setPasswordSource: (v) => patch({ passwordSource: v }),
    setPasswordCredentialId: (v) => patch({ passwordCredentialId: v }),
  };
}
