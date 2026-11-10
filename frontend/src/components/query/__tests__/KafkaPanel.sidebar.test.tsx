import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";
import { KafkaPanel } from "../KafkaPanel";
import { useKafkaStore } from "@/stores/kafkaStore";

const TAB = "kafka-1";
const refreshActiveView = vi.fn();

beforeEach(() => {
  localStorage.clear();
  refreshActiveView.mockReset();
  useKafkaStore.setState({
    ensureTab: vi.fn(),
    loadOverview: vi.fn(),
    loadBrokers: vi.fn(),
    loadTopics: vi.fn(),
    loadConsumerGroups: vi.fn(),
    refreshActiveView,
  } as never);
});

describe("KafkaPanel sidebar", () => {
  it("is a draggable PanelSidebar with a refresh action in its header", () => {
    render(<KafkaPanel tabId={TAB} />);
    const sidebar = screen.getByTestId("kafka-sidebar");
    expect(screen.getByTestId("kafka-sidebar-resize")).toBeInTheDocument();
    fireEvent.click(within(sidebar).getByRole("button", { name: "query.refreshTree" }));
    expect(refreshActiveView).toHaveBeenCalledWith(TAB);
  });
});
