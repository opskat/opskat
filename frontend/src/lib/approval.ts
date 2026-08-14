export interface ApprovalCommandItem {
  command: string;
}

// edited_items is a semantic signal at the backend boundary: its presence means the
// user changed the proposed grant pattern, so the value is treated as user-authored
// instead of being narrowed as a system-generated approval subject. Only send the
// complete edited set when at least one command actually differs.
export function hasApprovalCommandEdits(items: ApprovalCommandItem[], commands: string[]): boolean {
  return items.some((item, index) => commands[index] !== item.command);
}
