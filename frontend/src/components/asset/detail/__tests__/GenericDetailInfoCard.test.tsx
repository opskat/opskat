import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { GenericDetailInfoCard } from "@/components/asset/detail/GenericDetailInfoCard";
import { asset_entity, customtype } from "../../../../../wailsjs/go/models";
import { GetGenericAssetView, RevealGenericSecret } from "../../../../../wailsjs/go/customtype/CustomType";
import { MASKED_SECRET } from "../utils";

const mocks = vi.hoisted(() => ({ notifyCopied: vi.fn() }));
vi.mock("@/lib/notify", () => ({ notifyCopied: mocks.notifyCopied, notifySuccess: vi.fn() }));

const asset = new asset_entity.Asset({
  ID: 42,
  Name: "grafana-prod",
  Type: "generic",
  sshTunnelId: 5,
  Config: JSON.stringify({ custom_type: "grafana" }),
});

function view(overrides: Partial<customtype.GenericAssetView> = {}) {
  return new customtype.GenericAssetView({
    typeName: "Grafana",
    execMode: "http",
    usage: "Search dashboards with GET /api/search",
    actualAddress: "https://grafana.internal:3000",
    fields: [
      {
        name: "host",
        label: "主机",
        secret: false,
        value: "grafana.internal:3000",
        set: true,
        missing: false,
      },
      {
        name: "token",
        label: "Token",
        secret: true,
        set: true,
        credentialId: 3,
        credentialName: "grafana-prod-sa",
        missing: false,
      },
      { name: "webhook", label: "Webhook", secret: true, set: true, missing: false },
    ],
    ...overrides,
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: vi.fn().mockResolvedValue(undefined), readText: vi.fn() },
  });
});

describe("GenericDetailInfoCard", () => {
  it("shows plain values, masks inline secrets, names managed credentials, and shows address and tunnel", async () => {
    vi.mocked(GetGenericAssetView).mockResolvedValue(view());
    render(<GenericDetailInfoCard asset={asset} sshTunnelName={(id) => (id === 5 ? "bastion-prod" : null)} />);

    expect(await screen.findByText("grafana.internal:3000")).toBeInTheDocument();
    expect(screen.getByText("grafana-prod-sa")).toBeInTheDocument();
    expect(screen.getByTestId("generic-secret-value-webhook")).toHaveTextContent(MASKED_SECRET);
    expect(screen.getByText("https://grafana.internal:3000")).toBeInTheDocument();
    expect(screen.getByText("bastion-prod")).toBeInTheDocument();
    expect(screen.queryByTestId("generic-missing-banner")).toBeNull();
  });

  it("reveals an inline secret through the backend only on demand", async () => {
    vi.mocked(GetGenericAssetView).mockResolvedValue(view());
    vi.mocked(RevealGenericSecret).mockResolvedValue("plain-webhook");
    render(<GenericDetailInfoCard asset={asset} sshTunnelName={() => null} />);

    await userEvent.click(await screen.findByTestId("generic-reveal-webhook"));
    expect(RevealGenericSecret).toHaveBeenCalledWith(42, "webhook");
    expect(await screen.findByText("plain-webhook")).toBeInTheDocument();
    expect(screen.queryByTestId("generic-reveal-token")).toBeNull();
  });

  // 详情页切换资产时复用同一个组件实例:上一台资产的字段值和已查看的密钥明文不能留在页面上
  // 等新资产的数据回来。
  it("drops the previous asset's values and revealed secret as soon as another asset is shown", async () => {
    vi.mocked(GetGenericAssetView).mockResolvedValueOnce(view());
    vi.mocked(RevealGenericSecret).mockResolvedValue("plain-webhook");
    const { rerender } = render(<GenericDetailInfoCard asset={asset} sshTunnelName={() => null} />);
    await userEvent.click(await screen.findByTestId("generic-reveal-webhook"));
    expect(await screen.findByText("plain-webhook")).toBeInTheDocument();

    vi.mocked(GetGenericAssetView).mockReturnValueOnce(new Promise(() => {}));
    rerender(
      <GenericDetailInfoCard
        asset={new asset_entity.Asset({ ...asset, ID: 43, Name: "grafana-staging" })}
        sshTunnelName={() => null}
      />
    );

    expect(screen.queryByText("plain-webhook")).toBeNull();
    expect(screen.queryByText("grafana.internal:3000")).toBeNull();
  });

  it("warns about missing required fields and offers to fill them in", async () => {
    vi.mocked(GetGenericAssetView).mockResolvedValue(
      view({
        fields: [
          ...view().fields,
          { name: "datasource", label: "数据源 UID", secret: false, set: false, missing: true },
        ],
      })
    );
    const onEdit = vi.fn();
    render(<GenericDetailInfoCard asset={asset} sshTunnelName={() => null} onEdit={onEdit} />);

    const banner = await screen.findByTestId("generic-missing-banner");
    expect(banner).toHaveTextContent("asset.generic.missingBannerTitle");
    expect(banner).toHaveTextContent("数据源 UID");
    await userEvent.click(screen.getByTestId("generic-missing-fill"));
    expect(onEdit).toHaveBeenCalled();
  });

  it("lists copyable opsctl examples for the exec mode", async () => {
    vi.mocked(GetGenericAssetView).mockResolvedValue(view());
    render(<GenericDetailInfoCard asset={asset} sshTunnelName={() => null} />);

    const examples = await screen.findAllByTestId("generic-usage-example");
    const texts = examples.map((e) => e.textContent);
    expect(texts).toContain("opsctl exec grafana-prod -- GET /");
    expect(texts).toContain("opsctl secret get grafana-prod token");
    expect(texts).toContain("opsctl help grafana-prod");

    await userEvent.click(screen.getAllByTestId("generic-usage-copy")[0]);
    await waitFor(() =>
      expect(navigator.clipboard.writeText).toHaveBeenCalledWith("opsctl exec grafana-prod -- GET /")
    );
    expect(mocks.notifyCopied).toHaveBeenCalled();
  });

  it("uses a whole-command example for command types without a command template", async () => {
    vi.mocked(GetGenericAssetView).mockResolvedValue(
      view({ execMode: "command", hasCommandTemplate: false, actualAddress: undefined, fields: [view().fields[0]] })
    );
    render(<GenericDetailInfoCard asset={asset} sshTunnelName={() => null} />);
    const texts = (await screen.findAllByTestId("generic-usage-example")).map((e) => e.textContent);
    expect(texts[0]).toBe("opsctl exec grafana-prod -- '<command>'");
  });

  it("uses a command example for command types and quotes names with spaces", async () => {
    vi.mocked(GetGenericAssetView).mockResolvedValue(
      view({ execMode: "command", hasCommandTemplate: true, actualAddress: undefined, fields: [view().fields[0]] })
    );
    render(
      <GenericDetailInfoCard
        asset={new asset_entity.Asset({ ...asset, Name: "aws prod" })}
        sshTunnelName={() => null}
      />
    );
    const texts = (await screen.findAllByTestId("generic-usage-example")).map((e) => e.textContent);
    expect(texts[0]).toBe("opsctl exec 'aws prod' -- <args>");
    expect(texts.some((x) => x?.includes("secret get"))).toBe(false);
  });
});
