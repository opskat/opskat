/** 密码/托管密码凭据子状态:由 useAssetCredential 持有,供 db 族和 SSH password-auth 复用。 */
export interface CredentialState {
  /** 明文(用户输入或揭示既有密码后解密回填);测试走 4th-arg,保存前加密。 */
  password: string;
  /** 编辑态既有密文;用户未改动时沿用。 */
  encryptedPassword: string;
  passwordSource: "inline" | "managed";
  passwordCredentialId: number;
}

export const CREDENTIAL_DEFAULTS: CredentialState = {
  password: "",
  encryptedPassword: "",
  passwordSource: "inline",
  passwordCredentialId: 0,
};

/** 注入 config 的凭据片段(credential_id 或 password 二选一,或都无)。 */
export type CredentialFragment = { credential_id?: number; password?: string };

/** 编辑态回填:镜像旧 load*Config credential 分支(credential_id→managed;否则 inline+既有密文)。 */
export function initCredentialFromConfig(cfg: { credential_id?: number; password?: string }): CredentialState {
  if (cfg.credential_id) {
    return { password: "", encryptedPassword: "", passwordSource: "managed", passwordCredentialId: cfg.credential_id };
  }
  return { password: "", encryptedPassword: cfg.password || "", passwordSource: "inline", passwordCredentialId: 0 };
}

/** 测试连接片段:镜像旧 applyTestPasswordSource(managed→credential_id;inline 未改但有既有密文→password)。 */
export function resolveTestCredential(s: CredentialState): CredentialFragment {
  if (s.passwordSource === "managed" && s.passwordCredentialId > 0) {
    return { credential_id: s.passwordCredentialId };
  }
  if (!s.password && s.encryptedPassword) {
    return { password: s.encryptedPassword };
  }
  return {};
}

/** 保存片段:镜像旧 save 分支 + encryptPasswordValue(managed→credential_id;否则明文加密 / 沿用既有密文)。
 *  加密失败由 encrypt 的 reject 透传给调用方(buildConfig→handleSubmit 统一 toast),不在此吞错。 */
export async function resolveSaveCredential(
  s: CredentialState,
  encrypt: (plain: string) => Promise<string>
): Promise<CredentialFragment> {
  if (s.passwordSource === "managed" && s.passwordCredentialId > 0) {
    return { credential_id: s.passwordCredentialId };
  }
  const encrypted = s.password ? await encrypt(s.password) : s.encryptedPassword;
  return encrypted ? { password: encrypted } : {};
}
