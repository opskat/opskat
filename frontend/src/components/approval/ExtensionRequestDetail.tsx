import { TruncatedText } from "./TruncatedText";

interface ExtensionRequestDetailProps {
  /** 后端 formatExtensionRequestDetail 的输出：{"tool": ..., "args": {...}} 的缩进 JSON。 */
  detail: string;
  className?: string;
}

// 扩展请求的「工具 + 参数」：逐个参数展示，超长值（如 bulk 请求体）走 TruncatedText 截断，
// 其余一律原样。detail 由后端生成，解析失败就是后端的 bug，让它抛出而不是悄悄换种展示。
export function ExtensionRequestDetail({ detail, className = "" }: ExtensionRequestDetailProps) {
  const { tool, args } = JSON.parse(detail) as { tool?: string; args?: Record<string, unknown> };
  return (
    <div className={`select-text font-mono whitespace-pre-wrap break-all ${className}`}>
      {tool && <div data-testid="approval-tool">{tool}</div>}
      {Object.entries(args ?? {}).map(([key, value]) => (
        <div key={key} className="mt-0.5">
          <span className="text-muted-foreground">{key}: </span>
          <TruncatedText
            testId={`approval-arg-${key}`}
            text={typeof value === "string" ? value : JSON.stringify(value)}
          />
        </div>
      ))}
    </div>
  );
}
