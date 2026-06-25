import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { KafkaConfigSection } from "@/components/asset/KafkaConfigSection";

const ctx = { isEdit: false, encryptPassword: vi.fn() };

describe("KafkaConfigSection tabs", () => {
  it("renders the six Kafka tabs", () => {
    render(<KafkaConfigSection ctx={ctx} onValidityChange={vi.fn()} />);
    expect(screen.getByTestId("config-tab-connection")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tunnel")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tls")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-schema_registry")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-connect")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-advanced")).toBeInTheDocument();
  });

  it("flags schema_registry invalid when enabled without URL", async () => {
    const onValidity = vi.fn();
    render(<KafkaConfigSection ctx={ctx} onValidityChange={onValidity} />);
    // 先填 brokers,让 connection 合法
    await userEvent.type(screen.getByPlaceholderText("192.168.100.50:9092"), "b1:9092");
    // 切到 Schema Registry 标签并启用开关
    await userEvent.click(screen.getByTestId("config-tab-schema_registry"));
    await userEvent.click(screen.getByTestId("kafka-sr-enabled"));
    expect(onValidity).toHaveBeenLastCalledWith(
      expect.objectContaining({ canSave: false, saveDisabledReason: "asset.kafkaSchemaRegistryURLRequired" })
    );
  });
});
