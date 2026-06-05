export interface K8sFormState {
  kubeconfig: string;
  showKubeconfig: boolean;
  namespace: string;
  context: string;
  sshTunnelId: number;
}

export const K8S_DEFAULTS: K8sFormState = {
  kubeconfig: "",
  showKubeconfig: false,
  namespace: "",
  context: "",
  sshTunnelId: 0,
};

/**
 * 保存序列化(键序锁旧 save 分支: kubeconfig → namespace → context)。
 * kubeconfigCiphertext 由调用方预解析(新值加密 or 编辑保留旧密文)；
 * 纯函数 — 无副作用，可直接做 golden 测试。
 * **不含 ssh_asset_id** — SSH 隧道走 Asset 顶层字段。
 */
export function buildK8sConfig(state: K8sFormState, kubeconfigCiphertext: string): string {
  const cfg: Record<string, unknown> = {};
  if (kubeconfigCiphertext) cfg.kubeconfig = kubeconfigCiphertext;
  if (state.namespace) cfg.namespace = state.namespace;
  if (state.context) cfg.context = state.context;
  return JSON.stringify(cfg);
}

/**
 * 编辑态回填(镜像旧 loadK8sConfig):仅解析 namespace/context。
 * kubeconfig 密文从不预填；sshTunnelId 来自 asset 顶层而非 config。
 */
export function parseK8sConfig(configJSON: string): { namespace: string; context: string } {
  try {
    const cfg = JSON.parse(configJSON || "{}") as { namespace?: string; context?: string };
    return {
      namespace: cfg.namespace || "",
      context: cfg.context || "",
    };
  } catch {
    return { namespace: "", context: "" };
  }
}
