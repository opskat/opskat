import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

vi.mock("@/lib/notify", () => ({ notifySuccess: vi.fn(), notifyCopied: vi.fn() }));

import { notifySuccess } from "@/lib/notify";
import { useKafkaStore, type KafkaTabState } from "@/stores/kafkaStore";
import { KafkaConnectView } from "@/components/query/kafka/KafkaConnectView";
import { TopicsView } from "@/components/query/kafka/TopicsView";
import { ACLsView } from "@/components/query/kafka/ACLsView";
import { KafkaPanel } from "@/components/query/KafkaPanel";

const TAB = "k1";
const initialStore = useKafkaStore.getState();

function freshState(patch: Partial<KafkaTabState> = {}): KafkaTabState {
  useKafkaStore.getState().ensureTab(TAB);
  return { ...useKafkaStore.getState().states[TAB], ...patch };
}

function mockActions(actions: Record<string, unknown>) {
  useKafkaStore.setState(actions as never);
}

beforeEach(() => {
  vi.mocked(notifySuccess).mockReset();
  useKafkaStore.setState({ ...initialStore, states: {} }, true);
});

describe("Kafka non-destructive actions run without a confirm", () => {
  const connectorState = () =>
    freshState({
      connectClusters: [{ name: "cl" }],
      selectedConnector: "c1",
      connectorDetail: { name: "c1", config: {}, tasks: [], status: { connector: { state: "RUNNING" }, tasks: [] } },
    } as never);

  it("pauses and resumes a connector immediately and reports success", async () => {
    const pauseConnector = vi.fn().mockResolvedValue(undefined);
    const resumeConnector = vi.fn().mockResolvedValue(undefined);
    mockActions({ pauseConnector, resumeConnector });
    render(<KafkaConnectView tabId={TAB} state={connectorState()} />);

    await userEvent.click(screen.getByRole("button", { name: "query.kafkaPauseConnector" }));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    await waitFor(() => expect(pauseConnector).toHaveBeenCalledWith(TAB, "c1"));
    await waitFor(() => expect(notifySuccess).toHaveBeenCalledTimes(1));

    await userEvent.click(screen.getByRole("button", { name: "query.kafkaResumeConnector" }));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    await waitFor(() => expect(resumeConnector).toHaveBeenCalledWith(TAB, "c1"));
    await waitFor(() => expect(notifySuccess).toHaveBeenCalledTimes(2));
  });

  it("does not report success when the connector action fails", async () => {
    const pauseConnector = vi.fn().mockRejectedValue(new Error("boom"));
    mockActions({ pauseConnector });
    render(<KafkaConnectView tabId={TAB} state={connectorState()} />);
    await userEvent.click(screen.getByRole("button", { name: "query.kafkaPauseConnector" }));
    await waitFor(() => expect(pauseConnector).toHaveBeenCalled());
    expect(notifySuccess).not.toHaveBeenCalled();
  });

  it("still confirms deleting a connector, with a verb button", async () => {
    const deleteConnector = vi.fn().mockResolvedValue(undefined);
    mockActions({ deleteConnector });
    const { container } = render(<KafkaConnectView tabId={TAB} state={connectorState()} />);
    await userEvent.click(container.querySelector("button.text-destructive") as HTMLElement);
    const dialog = await screen.findByRole("alertdialog");
    expect(deleteConnector).not.toHaveBeenCalled();
    expect(dialog).toHaveTextContent("action.delete");
  });

  it("creates a topic and reports success", async () => {
    const createTopic = vi.fn().mockResolvedValue(undefined);
    mockActions({ createTopic });
    render(<TopicsView tabId={TAB} state={freshState()} />);
    await userEvent.click(screen.getByRole("button", { name: "query.kafkaCreateTopic" }));
    await userEvent.type(screen.getByPlaceholderText("query.kafkaTopic"), "orders");
    const buttons = screen.getAllByRole("button", { name: "query.kafkaCreateTopic" });
    await userEvent.click(buttons[buttons.length - 1]);
    await waitFor(() => expect(createTopic).toHaveBeenCalled());
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    await waitFor(() => expect(notifySuccess).toHaveBeenCalledTimes(1));
  });

  it("keeps the create-topic dialog open and shows the full reason on failure", async () => {
    const long = "Topic replication factor 3 is larger than available brokers: 1";
    const createTopic = vi.fn().mockImplementation(async () => {
      useKafkaStore.setState((s) => ({ states: { ...s.states, [TAB]: { ...s.states[TAB], error: long } } }));
      throw new Error(long);
    });
    mockActions({ createTopic });
    const view = render(<TopicsView tabId={TAB} state={freshState()} />);
    await userEvent.click(screen.getByRole("button", { name: "query.kafkaCreateTopic" }));
    await userEvent.type(screen.getByPlaceholderText("query.kafkaTopic"), "orders");
    const buttons = screen.getAllByRole("button", { name: "query.kafkaCreateTopic" });
    await userEvent.click(buttons[buttons.length - 1]);
    await waitFor(() => expect(createTopic).toHaveBeenCalled());
    view.rerender(<TopicsView tabId={TAB} state={useKafkaStore.getState().states[TAB]} />);
    expect(await screen.findByRole("alert")).toHaveTextContent(long);
    expect(notifySuccess).not.toHaveBeenCalled();
  });

  it("creates an ACL without a confirm and reports success", async () => {
    const createACL = vi.fn().mockResolvedValue(undefined);
    mockActions({ createACL });
    render(<ACLsView tabId={TAB} state={freshState()} />);
    await userEvent.click(screen.getByRole("button", { name: "query.kafkaCreateACL" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByPlaceholderText("query.kafkaACLResourceName"), "t1");
    await userEvent.type(within(dialog).getByPlaceholderText("query.kafkaACLPrincipalPlaceholder"), "User:alice");
    await userEvent.click(within(dialog).getByRole("button", { name: "query.kafkaCreateACL" }));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    await waitFor(() => expect(createACL).toHaveBeenCalled());
    await waitFor(() => expect(notifySuccess).toHaveBeenCalledTimes(1));
  });

  it("sends a message without a confirm and reports success", async () => {
    const produceKafkaMessage = vi.fn().mockResolvedValue(undefined);
    mockActions({ produceKafkaMessage });
    const state = freshState({
      selectedTopic: "orders",
      topicDetail: { name: "orders", internal: false, partitionCount: 1, replicationFactor: 1, partitions: [] },
    } as never);
    render(<TopicsView tabId={TAB} state={state} />);
    await userEvent.click(screen.getByRole("button", { name: "query.kafkaSendMessage" }));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    await waitFor(() => expect(produceKafkaMessage).toHaveBeenCalledWith(TAB));
    await waitFor(() => expect(notifySuccess).toHaveBeenCalledTimes(1));
  });

  it("still confirms deleting a topic", async () => {
    const deleteTopic = vi.fn().mockResolvedValue(undefined);
    mockActions({ deleteTopic });
    const state = freshState({
      selectedTopic: "orders",
      topicDetail: { name: "orders", internal: false, partitionCount: 1, replicationFactor: 1, partitions: [] },
    } as never);
    const { container } = render(<TopicsView tabId={TAB} state={state} />);
    await userEvent.click(container.querySelector("button.text-destructive") as HTMLElement);
    await screen.findByRole("alertdialog");
    expect(deleteTopic).not.toHaveBeenCalled();
  });
});

describe("KafkaPanel error presentation", () => {
  it("shows the complete failure reason in an alert, not a truncated header line", () => {
    const long = "x".repeat(40) + " broker transport failure: " + "y".repeat(300);
    useKafkaStore.getState().ensureTab(TAB);
    useKafkaStore.setState((s) => ({ states: { ...s.states, [TAB]: { ...s.states[TAB], error: long } } }));
    mockActions({
      ensureTab: vi.fn(),
      loadOverview: vi.fn(),
      loadBrokers: vi.fn(),
      loadTopics: vi.fn(),
      loadConsumerGroups: vi.fn(),
    });
    render(<KafkaPanel tabId={TAB} />);
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent(long);
    expect(alert.querySelector(".truncate")).toBeNull();
  });
});

describe("Kafka admin actions report success and show failures where they happen", () => {
  const topicState = () =>
    freshState({
      activeView: "topics",
      selectedTopic: "orders",
      topicDetail: { name: "orders", internal: false, partitionCount: 1, replicationFactor: 1, partitions: [] },
    } as never);

  it("reports success after deleting a topic", async () => {
    const deleteTopic = vi.fn().mockResolvedValue(undefined);
    mockActions({ deleteTopic });
    const { container } = render(<TopicsView tabId={TAB} state={topicState()} />);
    await userEvent.click(container.querySelector("button.text-destructive") as HTMLElement);
    const confirm = await screen.findByRole("alertdialog");
    await userEvent.click(within(confirm).getByRole("button", { name: "action.delete" }));
    await waitFor(() => expect(deleteTopic).toHaveBeenCalledWith(TAB, "orders"));
    await waitFor(() => expect(notifySuccess).toHaveBeenCalledTimes(1));
  });

  it("shows why deleting a topic failed in the topic panel", async () => {
    const deleteTopic = vi.fn().mockRejectedValue(new Error("TOPIC_AUTHORIZATION_FAILED"));
    mockActions({ deleteTopic });
    const { container } = render(<TopicsView tabId={TAB} state={topicState()} />);
    await userEvent.click(container.querySelector("button.text-destructive") as HTMLElement);
    const confirm = await screen.findByRole("alertdialog");
    await userEvent.click(within(confirm).getByRole("button", { name: "action.delete" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("TOPIC_AUTHORIZATION_FAILED");
    expect(notifySuccess).not.toHaveBeenCalled();
  });

  it("reports success after increasing partitions", async () => {
    const increasePartitions = vi.fn().mockResolvedValue(undefined);
    mockActions({ increasePartitions });
    render(<TopicsView tabId={TAB} state={topicState()} />);
    await userEvent.click(screen.getByRole("button", { name: "query.kafkaIncreasePartitions" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "query.kafkaIncreasePartitions" }));
    const confirm = await screen.findByRole("alertdialog");
    await userEvent.click(within(confirm).getByRole("button", { name: "query.kafkaIncreasePartitions" }));
    await waitFor(() => expect(increasePartitions).toHaveBeenCalledWith(TAB, "orders", 2));
    await waitFor(() => expect(notifySuccess).toHaveBeenCalledTimes(1));
  });

  it("keeps the increase-partitions dialog open with the full reason when it fails", async () => {
    const increasePartitions = vi.fn().mockRejectedValue(new Error("INVALID_PARTITIONS: replica assignment"));
    mockActions({ increasePartitions });
    render(<TopicsView tabId={TAB} state={topicState()} />);
    await userEvent.click(screen.getByRole("button", { name: "query.kafkaIncreasePartitions" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "query.kafkaIncreasePartitions" }));
    const confirm = await screen.findByRole("alertdialog");
    await userEvent.click(within(confirm).getByRole("button", { name: "query.kafkaIncreasePartitions" }));
    await waitFor(() => expect(increasePartitions).toHaveBeenCalled());
    expect(await within(screen.getByRole("dialog")).findByRole("alert")).toHaveTextContent(
      "INVALID_PARTITIONS: replica assignment"
    );
    expect(notifySuccess).not.toHaveBeenCalled();
  });

  it("shows an invalid topic-config update in the dialog instead of failing silently", async () => {
    const alterTopicConfig = vi.fn().mockResolvedValue(undefined);
    mockActions({ alterTopicConfig });
    render(<TopicsView tabId={TAB} state={topicState()} />);
    await userEvent.click(screen.getByRole("button", { name: "query.kafkaUpdateConfig" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox"), "not json");
    await userEvent.click(within(dialog).getByRole("button", { name: "action.save" }));
    const confirm = await screen.findByRole("alertdialog");
    await userEvent.click(within(confirm).getByRole("button", { name: "action.save" }));
    expect(await within(screen.getByRole("dialog")).findByRole("alert")).toBeInTheDocument();
    expect(alterTopicConfig).not.toHaveBeenCalled();
    expect(notifySuccess).not.toHaveBeenCalled();
  });

  it("reports success after restarting a connector", async () => {
    const restartConnector = vi.fn().mockResolvedValue(undefined);
    mockActions({ restartConnector });
    const state = freshState({
      connectClusters: [{ name: "cl" }],
      selectedConnector: "c1",
      connectorDetail: { name: "c1", config: {}, tasks: [], status: { connector: { state: "RUNNING" }, tasks: [] } },
    } as never);
    render(<KafkaConnectView tabId={TAB} state={state} />);
    await userEvent.click(screen.getByRole("button", { name: "query.kafkaRestartConnector" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "query.kafkaRestartConnector" }));
    await waitFor(() => expect(restartConnector).toHaveBeenCalled());
    await waitFor(() => expect(notifySuccess).toHaveBeenCalledTimes(1));
  });
});

describe("KafkaPanel load failure", () => {
  it("offers a retry for the failed load and does not claim the list is empty", async () => {
    const refreshActiveView = vi.fn().mockResolvedValue(undefined);
    useKafkaStore.getState().ensureTab(TAB);
    useKafkaStore.setState((s) => ({
      states: { ...s.states, [TAB]: { ...s.states[TAB], activeView: "topics", error: "dial tcp: i/o timeout" } },
    }));
    mockActions({
      ensureTab: vi.fn(),
      loadOverview: vi.fn(),
      loadBrokers: vi.fn(),
      loadTopics: vi.fn(),
      loadConsumerGroups: vi.fn(),
      refreshActiveView,
    });
    render(<KafkaPanel tabId={TAB} />);
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("dial tcp: i/o timeout");
    expect(screen.queryByText("query.kafkaNoTopics")).not.toBeInTheDocument();
    await userEvent.click(within(alert).getByRole("button", { name: "action.retry" }));
    expect(refreshActiveView).toHaveBeenCalledWith(TAB);
  });
});
