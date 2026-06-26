import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Plus } from "lucide-react";
import { Textarea } from "@opskat/ui";
import { Field } from "@/components/asset/fields";

interface DescriptionBarProps {
  value: string;
  onChange: (v: string) => void;
}

/** 描述/备注:常态折成一行"添加备注",点击就地展开成文本框;编辑态有内容时直接展开。 */
export function DescriptionBar({ value, onChange }: DescriptionBarProps) {
  const { t } = useTranslation();
  // 用户主动点开后置 true;但只要已有内容就直接展开,避免父级在挂载后才填充 value
  // (AssetForm 编辑态经 effect 异步回填 description)时仍停留在折叠态、把已有备注藏在按钮后面。
  const [expanded, setExpanded] = useState(false);
  const showTextarea = expanded || !!value;

  if (!showTextarea) {
    return (
      <button
        type="button"
        data-testid="description-add"
        onClick={() => setExpanded(true)}
        className="flex w-fit items-center gap-1.5 text-[13px] text-muted-foreground hover:text-foreground"
      >
        <Plus className="h-3.5 w-3.5" />
        {t("asset.addDescription")}
      </button>
    );
  }

  return (
    <Field label={t("asset.description")}>
      <Textarea
        autoFocus
        data-testid="description-textarea"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onFocus={() => setExpanded(true)}
        rows={2}
      />
    </Field>
  );
}
