import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Eye, EyeOff, Loader2 } from "lucide-react";
import { Button, Input } from "@opskat/ui";
import { cn } from "@opskat/ui";

interface SecretInputProps extends React.ComponentProps<typeof Input> {
  /** 受控显示态：父组件持有解密等逻辑时传入，配合 onRevealChange 使用。 */
  reveal?: boolean;
  /** 受控模式下用户点击眼睛时收到下一个显示态。 */
  onRevealChange?: (next: boolean) => void;
  /** 解密等异步期间在眼睛按钮显示加载图标并禁止切换。 */
  revealLoading?: boolean;
  /** 眼睛右侧的附加 action 按钮（如生成随机密码）。 */
  actions?: ReactNode;
  /** 外层 relative 容器的 className（如 flex 行内需要 flex-1 撑开）。 */
  wrapperClassName?: string;
}

/**
 * 共享秘密输入：持有原始 value，默认 type="password"，可访问的 Eye/EyeOff 切换同一原值。
 * 不做任何自动解密 / 记录 / 复制；PasswordSourceField 的按需解密经受控 reveal 由父组件驱动。
 */
export function SecretInput({
  reveal: revealProp,
  onRevealChange,
  revealLoading = false,
  actions,
  wrapperClassName,
  className,
  disabled,
  ...props
}: SecretInputProps) {
  const { t } = useTranslation();
  const [internalReveal, setInternalReveal] = useState(false);
  const controlled = revealProp !== undefined;
  const reveal = controlled ? revealProp : internalReveal;

  const toggleReveal = () => {
    if (revealLoading) return;
    const next = !reveal;
    if (!controlled) {
      setInternalReveal(next);
    }
    onRevealChange?.(next);
  };

  const hasActions = actions != null;

  return (
    <div className={cn("relative", wrapperClassName)}>
      <Input
        {...props}
        type={reveal ? "text" : "password"}
        disabled={disabled}
        className={cn(hasActions ? "pr-18" : "pr-9", className)}
      />
      <div className="absolute right-1 top-1/2 -translate-y-1/2 flex items-center gap-0.5">
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="h-7 w-7"
          disabled={disabled || revealLoading || (controlled && onRevealChange === undefined)}
          onClick={toggleReveal}
          aria-label={t(reveal ? "action.hideSecret" : "action.showSecret")}
          aria-pressed={reveal}
        >
          {revealLoading ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          ) : reveal ? (
            <EyeOff className="h-3.5 w-3.5" />
          ) : (
            <Eye className="h-3.5 w-3.5" />
          )}
        </Button>
        {actions}
      </div>
    </div>
  );
}
