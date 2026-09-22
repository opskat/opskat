import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Input } from "@opskat/ui";

/**
 * SSH keyboard-interactive 挑战表单：服务器的名称 / 说明 / 逐条提示按原文渲染，回显关闭的
 * 提示用密码框。终端连接进度页与 opsctl 转来的桌面 MFA 对话框共用。
 */
export function AuthChallengeForm({
  prompts,
  echo,
  name,
  instruction,
  visible,
  onSubmit,
  onCancel,
}: {
  prompts: string[];
  echo: boolean[];
  /** 服务器发送的挑战名称/说明（Agent 结构化 MFA;可能为空）。 */
  name?: string;
  instruction?: string;
  /** 仅当前活动且可见的连接操作自动聚焦;隐藏标签页不抢夺焦点。 */
  visible: boolean;
  onSubmit: (answers: string[]) => void;
  onCancel: () => void;
}) {
  const { t } = useTranslation();
  const [answers, setAnswers] = useState<string[]>(() => new Array(prompts.length).fill(""));
  const handleSubmit = () => {
    onSubmit(answers);
  };

  return (
    <div
      className="w-full max-w-xs space-y-3 mb-4"
      onKeyDown={(e) => {
        // Esc 取消当前操作:答案只存在于本地 state,取消即随卸载丢弃。
        if (e.key === "Escape") {
          e.preventDefault();
          onCancel();
        }
      }}
    >
      {/* 挑战名称与说明按普通文本渲染(不是 HTML),与提示同一信任边界。 */}
      {name && <div className="text-xs font-medium">{name}</div>}
      {instruction && <p className="text-xs text-muted-foreground whitespace-pre-wrap break-words">{instruction}</p>}
      {prompts.map((prompt, i) => (
        <div key={i} className="space-y-1">
          {/* 服务器提示按普通文本渲染(不是 HTML);label 与输入框正确关联。 */}
          <label htmlFor={`mfa-answer-${i}`} className="text-xs text-muted-foreground">
            {prompt}
          </label>
          <Input
            id={`mfa-answer-${i}`}
            type={echo[i] ? "text" : "password"}
            value={answers[i]}
            onChange={(e) => {
              const next = [...answers];
              next[i] = e.target.value;
              setAnswers(next);
            }}
            onKeyDown={(e) => e.key === "Enter" && handleSubmit()}
            className="h-8 text-sm"
            autoFocus={visible && i === 0}
          />
        </div>
      ))}
      <Button size="sm" onClick={handleSubmit} className="w-full">
        {t("action.submit")}
      </Button>
    </div>
  );
}
