import type { Platform } from "@/hooks/usePlatform";
import { Ban, CheckCircle2, Inbox, Loader2, XCircle } from "lucide-react";

/** SSH Agent 来源端点类型，与后端 sshagent.EndpointType 对齐。 */
export type AgentEndpointType = "environment" | "unix_socket" | "windows_named_pipe";

/** 来源行运行时状态：加载中 / 可用且包含身份 / 可用但为空 / 不可用 / 当前平台不支持。 */
export type AgentSourceRuntimeStatus = "loading" | "ok" | "empty" | "unavailable" | "unsupported";

export const AGENT_ENDPOINT_TYPES: AgentEndpointType[] = ["environment", "unix_socket", "windows_named_pipe"];

/** 当前平台可手动创建的端点类型；编辑导入数据时保留不兼容类型（规格：来源对话框）。 */
export function supportedEndpointTypes(platform: Platform): AgentEndpointType[] {
  if (platform === "windows") return ["environment", "windows_named_pipe"];
  return ["environment", "unix_socket"];
}

export function isEndpointSupported(type: AgentEndpointType, platform: Platform): boolean {
  return supportedEndpointTypes(platform).includes(type);
}

/**
 * 端点结构校验（与后端 Source.Validate 对齐的轻量前端镜像）：
 * 环境变量名语法 / Unix 绝对路径（~ 展开后）/ 本机 \\.\pipe\。
 * 保存不要求探测成功，但名称与端点结构未完成前禁用保存。
 */
export function isEndpointStructurallyValid(type: string, value: string): boolean {
  const v = value.trim();
  if (!v) return false;
  switch (type) {
    case "environment":
      return /^[A-Za-z_][A-Za-z0-9_]*$/.test(v);
    case "unix_socket":
      return /^~?\//.test(v);
    case "windows_named_pipe":
      return /^\\\\\.\\pipe\\/i.test(v) && v.length > 8;
    default:
      return false;
  }
}

/** 切换端点类型时预填的默认端点值。 */
export function endpointDefaultValue(type: AgentEndpointType): string {
  switch (type) {
    case "environment":
      return "SSH_AUTH_SOCK";
    case "windows_named_pipe":
      // 本机 OpenSSH 兼容 pipe（\\.\pipe\openssh-ssh-agent）
      return "\\\\.\\pipe\\openssh-ssh-agent";
    default:
      return "";
  }
}

/** 端点类型的中文/英文标签（凭据列表来源行与候选行共用）。 */
export function endpointKindLabel(type: string, t: Translate): string {
  switch (type) {
    case "environment":
      return t("agentSource.kindEnvironment");
    case "unix_socket":
      return t("agentSource.kindUnixSocket");
    case "windows_named_pipe":
      return t("agentSource.kindWindowsNamedPipe");
    default:
      return type;
  }
}

/** 运行时状态的图标/文案/色调映射。颜色只作辅助，标签文本始终区分五态。 */
export function agentStatusInfo(status: AgentSourceRuntimeStatus, t: Translate) {
  switch (status) {
    case "loading":
      return { label: t("agentSource.statusLoading"), Icon: Loader2, spin: true, tone: "text-primary" };
    case "ok":
      return { label: t("agentSource.statusOk"), Icon: CheckCircle2, spin: false, tone: "text-success" };
    case "empty":
      return { label: t("agentSource.statusEmpty"), Icon: Inbox, spin: false, tone: "text-warning" };
    case "unavailable":
      return { label: t("agentSource.statusUnavailable"), Icon: XCircle, spin: false, tone: "text-destructive" };
    case "unsupported":
      return { label: t("agentSource.statusUnsupported"), Icon: Ban, spin: false, tone: "text-warning" };
  }
}

type Translate = (key: string, options?: Record<string, unknown>) => string;

/** 从检测候选派生一个可编辑的默认名称（后端候选只含类型与端点）。 */
export function candidateDefaultName(type: AgentEndpointType, endpoint: string): string {
  switch (type) {
    case "environment":
      return endpoint || "SSH Agent";
    case "unix_socket": {
      const seg = endpoint.split("/").filter(Boolean).at(-1);
      return seg || endpoint || "SSH Agent";
    }
    case "windows_named_pipe":
      return "OpenSSH Agent";
    default:
      return "SSH Agent";
  }
}
