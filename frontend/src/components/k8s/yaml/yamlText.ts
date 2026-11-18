import yaml from "js-yaml";

export interface YamlSyntaxError {
  /** The parser's reason, without its code excerpt. */
  message: string;
  /** 1-based position in the text. */
  line: number;
  column: number;
}

export type ParsedYaml = { ok: true; value: Record<string, unknown> } | { ok: false; error: YamlSyntaxError };

/** Parses one YAML document that must be a mapping (a Kubernetes object). */
export function parseYamlObject(text: string): ParsedYaml {
  try {
    const value = yaml.load(text);
    if (value === null || typeof value !== "object" || Array.isArray(value)) {
      return { ok: false, error: { message: "expected a YAML mapping (key: value)", line: 1, column: 1 } };
    }
    return { ok: true, value: value as Record<string, unknown> };
  } catch (e) {
    if (e instanceof yaml.YAMLException) {
      return {
        ok: false,
        error: { message: e.reason, line: (e.mark?.line ?? 0) + 1, column: (e.mark?.column ?? 0) + 1 },
      };
    }
    throw e;
  }
}

export function dumpYaml(value: unknown): string {
  return yaml.dump(value, { lineWidth: -1, noRefs: true });
}

/** The object as the edit dialog shows it: without `status` (the cluster owns it) and `managedFields`. */
export function editableYaml(object: Record<string, unknown>): string {
  const { status: _status, ...rest } = object;
  const metadata = { ...(rest.metadata as Record<string, unknown> | undefined) };
  delete metadata.managedFields;
  return dumpYaml({ ...rest, metadata });
}
