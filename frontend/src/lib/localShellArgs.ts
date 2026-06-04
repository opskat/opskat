export function parseLocalShellArgs(input: string): string[] {
  const args: string[] = [];
  let current = "";
  let quote: "'" | '"' | null = null;
  let started = false;

  for (let i = 0; i < input.length; i++) {
    const ch = input[i];

    if (quote) {
      if (ch === quote) {
        quote = null;
        started = true;
        continue;
      }
      if (quote === '"' && ch === "\\") {
        const next = input[i + 1];
        if (next === undefined) {
          throw new Error("unfinished escape");
        }
        if (next === '"' || next === "\\") {
          i++;
          current += next;
        } else {
          current += ch;
        }
        started = true;
        continue;
      }
      current += ch;
      started = true;
      continue;
    }

    if (ch === "'" || ch === '"') {
      quote = ch;
      started = true;
      continue;
    }
    if (ch === "\\") {
      const next = input[i + 1];
      if (next === undefined) {
        throw new Error("unfinished escape");
      }
      if (/\s/.test(next) || next === "'" || next === '"' || next === "\\") {
        i++;
        current += next;
      } else {
        current += ch;
      }
      started = true;
      continue;
    }
    if (/\s/.test(ch)) {
      if (started) {
        args.push(current);
        current = "";
        started = false;
      }
      continue;
    }
    current += ch;
    started = true;
  }

  if (quote) {
    throw new Error("unclosed quote");
  }
  if (started) {
    args.push(current);
  }
  return args;
}

export function formatLocalShellArgs(args?: string[]): string {
  return (args || []).map(formatLocalShellArg).join(" ");
}

function formatLocalShellArg(arg: string): string {
  if (/^[^\s"'\\]+$/.test(arg)) {
    return arg;
  }
  return `"${arg.replace(/(["\\])/g, "\\$1")}"`;
}
