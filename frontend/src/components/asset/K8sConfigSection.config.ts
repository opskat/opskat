import {
  CONNECTION_DEFAULTS,
  buildProxyJSON,
  parseConnectionFields,
  type ConnectionFormFields,
  type ProxyConfigJSON,
} from "./proxyConfig";

export interface K8sFormState extends ConnectionFormFields {
  kubeconfig: string;
  showKubeconfig: boolean;
  namespace: string;
  context: string;
}

export const K8S_DEFAULTS: K8sFormState = {
  kubeconfig: "",
  showKubeconfig: false,
  namespace: "",
  context: "",
  ...CONNECTION_DEFAULTS,
};

/**
 * 保存序列化(键序锁旧 save 分支: kubeconfig → namespace → context,proxy 为尾键)。
 * kubeconfigCiphertext 由调用方预解析(新值加密 or 编辑保留旧密文);
 * proxyPassword 由 resolveSaveProxyPassword(save=密文)或 state.proxyPassword(test 不适用 K8s)预解析。
 * 纯函数 — 无副作用,可直接做 golden 测试。
 * **不含 ssh_asset_id** — SSH 隧道走 Asset 顶层字段;隧道与代理互斥,按 connectionType 二选一。
 */
export function buildK8sConfig(state: K8sFormState, kubeconfigCiphertext: string, proxyPassword = ""): string {
  const cfg: Record<string, unknown> = {};
  if (kubeconfigCiphertext) cfg.kubeconfig = kubeconfigCiphertext;
  if (state.namespace) cfg.namespace = state.namespace;
  if (state.context) cfg.context = state.context;
  const proxy = buildProxyJSON(state, proxyPassword);
  if (proxy) cfg.proxy = proxy;
  return JSON.stringify(cfg);
}

/**
 * 编辑态回填(镜像旧 loadK8sConfig:解析 namespace/context)+ 派生连接方式。
 * kubeconfig 密文从不预填;connectionType 由 assetTunnelId(asset 顶层)与 proxy 派生。
 */
export function parseK8sConfig(configJSON: string, assetTunnelId = 0): K8sFormState {
  try {
    const cfg = JSON.parse(configJSON || "{}") as {
      namespace?: string;
      context?: string;
      proxy?: ProxyConfigJSON;
    };
    return {
      kubeconfig: "",
      showKubeconfig: false,
      namespace: cfg.namespace || "",
      context: cfg.context || "",
      ...parseConnectionFields(cfg.proxy, assetTunnelId),
    };
  } catch {
    return { ...K8S_DEFAULTS };
  }
}
