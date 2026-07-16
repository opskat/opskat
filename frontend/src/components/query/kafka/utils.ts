import type { KafkaConnectorSummary, KafkaSchemaReference, KafkaTopicConfigMutation } from "@/stores/kafkaStore";

export function parseSchemaReferences(value: string): KafkaSchemaReference[] | undefined {
  const text = value.trim();
  if (!text) return undefined;
  const parsed = JSON.parse(text);
  if (!Array.isArray(parsed)) {
    throw new Error("schema references must be a JSON array");
  }
  return parsed as KafkaSchemaReference[];
}

export function formatSchema(value: string): string {
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value || "-";
  }
}

export function formatConnectorConfig(config?: Record<string, string>): string {
  if (config && Object.keys(config).length > 0) {
    return JSON.stringify(config, null, 2);
  }
  return JSON.stringify(
    {
      "connector.class": "",
      "tasks.max": "1",
    },
    null,
    2
  );
}

export function parseConnectorConfigObject(value: string): Record<string, string> {
  const parsed = parseOptionalJsonObject(value);
  if (!parsed) {
    throw new Error("connector config must be a JSON object");
  }
  return Object.fromEntries(Object.entries(parsed).map(([key, item]) => [key, String(item)]));
}

export function formatConnectorTaskSummary(connector: KafkaConnectorSummary): string {
  const total = connector.taskCount || 0;
  const failed = connector.failedTaskCount || 0;
  if (!total) return "-";
  if (!failed) return String(total);
  return `${total} / ${failed}`;
}

export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export function parseOptionalJsonObject(value: string): Record<string, string> | undefined {
  const text = value.trim();
  if (!text) return undefined;
  const parsed = JSON.parse(text);
  if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") {
    throw new Error("configs must be a JSON object");
  }
  return parsed as Record<string, string>;
}

export function parseConfigUpdates(value: string): KafkaTopicConfigMutation[] {
  const text = value.trim();
  if (!text) return [];
  const parsed = JSON.parse(text);
  if (!Array.isArray(parsed)) {
    throw new Error("config updates must be a JSON array");
  }
  return parsed as KafkaTopicConfigMutation[];
}

export function parseIntegerArray(value: string): number[] | undefined {
  const text = value.trim();
  if (!text) return undefined;
  const parsed = JSON.parse(text);
  if (!Array.isArray(parsed) || parsed.some((item) => !Number.isInteger(item))) {
    throw new Error("partitions must be a JSON array of integers");
  }
  return parsed as number[];
}

export function parseRequiredNumber(value: string): number {
  const n = Number(value.trim());
  if (!Number.isInteger(n)) {
    throw new Error("value must be an integer");
  }
  return n;
}
