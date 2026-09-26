export interface RememberableItem {
  command: string;
  // 仅分类过的扩展审批项（后端 ClassifyFunc）带：check_policy 分类出的动作，以及"始终允许"
  // 实际落库的 <action>:<resource-glob>（资源已按 grant 的 glob 规则转义）。
  action?: string;
  remember_pattern?: string;
}

// 「记住」编辑器的初始值：分类过的扩展审批记住的是分类（落库为 ext:<type>:<remember_pattern>），
// 不是命令串；其余审批记住的就是命令串本身。按 action 判断是否分类（与 losesAction 同一个
// 判据）：opsctl 事件对未分类的审批项照样发出 remember_pattern，只是空串。
export function rememberPrefill(item: RememberableItem): string {
  return item.action ? (item.remember_pattern ?? "") : item.command;
}

// 分类审批的编辑值必须保留 "<action>:" 前缀——只能改资源段，动作被删掉或换掉就成了
// 用户没被问过的授权。后端 ParseApprovalResponse 同样校验，这里是为了当场提示而不是提交后被拒。
export function losesAction(item: RememberableItem, value: string): boolean {
  return !!item.action && !value.startsWith(`${item.action}:`);
}

export function hasRememberPatternErrors(items: RememberableItem[], values: string[]): boolean {
  return items.some((item, i) => losesAction(item, values[i]));
}
