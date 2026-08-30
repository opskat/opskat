import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { render, cleanup, within, screen } from "@testing-library/react";
import { asset_entity, system } from "../../../../../wailsjs/go/models";
import { SSHDetailInfoCard } from "../SSHDetailInfoCard";
import { DatabaseDetailInfoCard } from "../DatabaseDetailInfoCard";
import { RedisDetailInfoCard } from "../RedisDetailInfoCard";
import { MongoDBDetailInfoCard } from "../MongoDBDetailInfoCard";
import { K8sDetailInfoCard } from "../K8sDetailInfoCard";
import { SerialDetailInfoCard } from "../SerialDetailInfoCard";
import { EtcdDetailInfoCard } from "../EtcdDetailInfoCard";
import { KafkaDetailInfoCard } from "../KafkaDetailInfoCard";
import { OSSDetailInfoCard } from "../OSSDetailInfoCard";
import { VNCDetailInfoCard } from "../VNCDetailInfoCard";

afterEach(() => {
  cleanup();
});

// SSHDetailInfoCard 的 Agent 详情读取绑定;仅 agent 认证调用。
let agentDetail: Partial<system.AgentAssetDetail> | null = null;
let agentDetailError: string | null = null;

vi.mock("../../../../../wailsjs/go/system/System", () => ({
  GetAgentAssetDetail: (id: number, fp: string) =>
    agentDetailError
      ? Promise.reject(new Error(agentDetailError))
      : Promise.resolve({
          source_id: id,
          source_name: "System Agent",
          fingerprint: fp,
          availability: "ok",
          type: "ssh-ed25519",
          comment: "work key",
          ...agentDetail,
        }),
  GetAgentSource: vi.fn(() => {
    throw new Error("GetAgentSource 不应被资产详情调用(会暴露端点)");
  }),
}));

beforeEach(() => {
  agentDetail = null;
  agentDetailError = null;
});

function makeAsset(type: string, config: Record<string, unknown>): asset_entity.Asset {
  return new asset_entity.Asset({
    ID: 1,
    Name: "test-asset",
    Type: type,
    Config: JSON.stringify(config),
    CmdPolicy: "",
    GroupID: 0,
    Icon: "",
    Tags: "",
    Description: "",
    SortOrder: 0,
    sshTunnelId: 0,
    Status: 1,
    Createtime: 0,
    Updatetime: 0,
  });
}

function makeAssetWithTunnel(type: string, config: Record<string, unknown>, sshTunnelId: number): asset_entity.Asset {
  const asset = makeAsset(type, config);
  asset.sshTunnelId = sshTunnelId;
  return asset;
}

const noopTunnel = vi.fn(() => null);

describe("SSHDetailInfoCard", () => {
  it("renders SSH connection fields", () => {
    const asset = makeAsset("ssh", {
      host: "10.0.0.1",
      port: 22,
      username: "root",
      auth_type: "password",
      password: "secret",
    });
    const { getByText } = render(<SSHDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(getByText("10.0.0.1")).toBeInTheDocument();
    expect(getByText("22")).toBeInTheDocument();
    expect(getByText("root")).toBeInTheDocument();
  });

  it("renders jump host when present", () => {
    const tunnelFn = vi.fn((id?: number) => (id === 5 ? "jump-server" : null));
    const asset = makeAsset("ssh", {
      host: "10.0.0.1",
      port: 22,
      username: "root",
      auth_type: "key",
      jump_host_id: 5,
    });
    const { getByText } = render(<SSHDetailInfoCard asset={asset} sshTunnelName={tunnelFn} />);
    expect(getByText("jump-server")).toBeInTheDocument();
  });

  it("renders jump host from asset field when saved config omits jump_host_id", () => {
    const tunnelFn = vi.fn((id?: number) => (id === 7 ? "asset-level-jump" : null));
    const asset = makeAssetWithTunnel(
      "ssh",
      {
        host: "10.0.0.1",
        port: 22,
        username: "root",
        auth_type: "password",
      },
      7
    );
    const { getByText } = render(<SSHDetailInfoCard asset={asset} sshTunnelName={tunnelFn} />);
    expect(getByText("asset-level-jump")).toBeInTheDocument();
  });

  it("renders proxy section when present", () => {
    const asset = makeAsset("ssh", {
      host: "10.0.0.1",
      port: 22,
      username: "root",
      auth_type: "password",
      proxy: { type: "socks5", host: "proxy.example.com", port: 1080, username: "proxyuser" },
    });
    const { getByText } = render(<SSHDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(getByText("SOCKS5")).toBeInTheDocument();
    expect(getByText("proxy.example.com:1080")).toBeInTheDocument();
    expect(getByText("proxyuser")).toBeInTheDocument();
  });

  it("handles empty config without crashing", () => {
    const asset = makeAsset("ssh", {});
    const { container } = render(<SSHDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(container).toBeDefined();
  });
});

describe("SSHDetailInfoCard Agent 认证", () => {
  const AGENT_FP = "SHA256:AGENTDETAILFINGERPRINTAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";
  const agentAsset = () =>
    makeAsset("ssh", {
      host: "10.0.0.1",
      port: 22,
      username: "root",
      auth_type: "agent",
      agent_source_id: 3,
      agent_key_fingerprint: AGENT_FP,
    });

  it("可用:显示 认证类型/来源名/已存指纹/当前可用性/密钥类型/备注;不调用 GetAgentSource(不暴露端点)", async () => {
    agentDetail = { availability: "ok", type: "ssh-ed25519", comment: "dev key" };
    render(<SSHDetailInfoCard asset={agentAsset()} sshTunnelName={noopTunnel} />);

    expect(await screen.findByText("System Agent")).toBeInTheDocument();
    expect(screen.getByText(AGENT_FP)).toBeInTheDocument();
    // 认证类型在连接段与 Agent 段各出现一次。
    expect(screen.getAllByText("asset.authAgent").length).toBeGreaterThanOrEqual(2);
    expect(screen.getByText("agentSource.statusOk")).toBeInTheDocument(); // 可用
    expect(screen.getByText("ssh-ed25519")).toBeInTheDocument();
    expect(screen.getByText("dev key")).toBeInTheDocument();
  });

  it("身份缺失:显示指纹与'身份缺失',不显示过期的密钥类型/备注元数据", async () => {
    agentDetail = { availability: "missing", type: undefined, comment: undefined };
    render(<SSHDetailInfoCard asset={agentAsset()} sshTunnelName={noopTunnel} />);

    expect(await screen.findByText("System Agent")).toBeInTheDocument();
    expect(screen.getByText(AGENT_FP)).toBeInTheDocument();
    expect(screen.getByText("asset.agentIdentityMissing")).toBeInTheDocument();
    expect(screen.queryByText("ssh-ed25519")).not.toBeInTheDocument();
    expect(screen.queryByText("dev key")).not.toBeInTheDocument();
  });

  it("来源不可用:显示'不可用',不显示密钥类型/备注", async () => {
    agentDetail = { availability: "unavailable", type: undefined, comment: undefined };
    render(<SSHDetailInfoCard asset={agentAsset()} sshTunnelName={noopTunnel} />);

    expect(await screen.findByText("System Agent")).toBeInTheDocument();
    expect(screen.getByText("agentSource.statusUnavailable")).toBeInTheDocument();
    expect(screen.queryByText("ssh-ed25519")).not.toBeInTheDocument();
  });

  it("详情读取失败时仍显示已存指纹,不崩溃", async () => {
    agentDetailError = "source not found";
    render(<SSHDetailInfoCard asset={agentAsset()} sshTunnelName={noopTunnel} />);
    expect(await screen.findByText(AGENT_FP)).toBeInTheDocument();
  });
});

describe("DatabaseDetailInfoCard", () => {
  it("renders database connection fields", () => {
    const asset = makeAsset("database", {
      driver: "postgresql",
      host: "db.example.com",
      port: 5432,
      username: "admin",
      password: "secret",
      database: "mydb",
    });
    const { getByText } = render(<DatabaseDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(getByText("PostgreSQL")).toBeInTheDocument();
    expect(getByText("db.example.com:5432")).toBeInTheDocument();
    expect(getByText("admin")).toBeInTheDocument();
    expect(getByText("mydb")).toBeInTheDocument();
  });

  it("masks password field", () => {
    const asset = makeAsset("database", {
      driver: "mysql",
      host: "db.example.com",
      port: 3306,
      username: "admin",
      password: "supersecret",
    });
    const { container, queryByText } = render(<DatabaseDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    const view = within(container);
    expect(view.getByText("\u25CF\u25CF\u25CF\u25CF\u25CF\u25CF")).toBeInTheDocument();
    expect(queryByText("supersecret")).not.toBeInTheDocument();
  });

  it("shows SSH tunnel when present", () => {
    const tunnelFn = vi.fn((id?: number) => (id === 3 ? "bastion" : null));
    const asset = makeAsset("database", {
      driver: "mysql",
      host: "db.local",
      port: 3306,
      username: "root",
      ssh_asset_id: 3,
    });
    const { getByText } = render(<DatabaseDetailInfoCard asset={asset} sshTunnelName={tunnelFn} />);
    expect(getByText("bastion")).toBeInTheDocument();
  });

  it("shows SSH tunnel from asset field", () => {
    const tunnelFn = vi.fn((id?: number) => (id === 8 ? "db-bastion" : null));
    const asset = makeAssetWithTunnel(
      "database",
      {
        driver: "mysql",
        host: "db.local",
        port: 3306,
        username: "root",
      },
      8
    );
    const { getByText } = render(<DatabaseDetailInfoCard asset={asset} sshTunnelName={tunnelFn} />);
    expect(getByText("db-bastion")).toBeInTheDocument();
  });

  it("handles empty config without crashing", () => {
    const asset = makeAsset("database", {});
    const { container } = render(<DatabaseDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(container).toBeDefined();
  });
});

describe("RedisDetailInfoCard", () => {
  it("renders redis connection fields", () => {
    const asset = makeAsset("redis", {
      host: "redis.example.com",
      port: 6379,
      username: "default",
      password: "redispw",
      database: 2,
    });
    const { getByText } = render(<RedisDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(getByText("redis.example.com:6379")).toBeInTheDocument();
    expect(getByText("default")).toBeInTheDocument();
    expect(getByText("2")).toBeInTheDocument();
  });

  it("masks password field", () => {
    const asset = makeAsset("redis", {
      host: "redis.local",
      port: 6379,
      password: "topsecret",
    });
    const { container, queryByText } = render(<RedisDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    const view = within(container);
    expect(view.getByText("\u25CF\u25CF\u25CF\u25CF\u25CF\u25CF")).toBeInTheDocument();
    expect(queryByText("topsecret")).not.toBeInTheDocument();
  });

  it("handles empty config without crashing", () => {
    const asset = makeAsset("redis", {});
    const { container } = render(<RedisDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(container).toBeDefined();
  });

  it("shows SSH tunnel from asset field when saved config omits ssh_asset_id", () => {
    const tunnelFn = vi.fn((id?: number) => (id === 9 ? "redis-bastion" : null));
    const asset = makeAssetWithTunnel("redis", { host: "redis.local", port: 6379 }, 9);
    const { getByText } = render(<RedisDetailInfoCard asset={asset} sshTunnelName={tunnelFn} />);
    expect(getByText("redis-bastion")).toBeInTheDocument();
  });
});

describe("MongoDBDetailInfoCard", () => {
  it("renders MongoDB fields with host:port", () => {
    const asset = makeAsset("mongodb", {
      host: "mongo.example.com",
      port: 27017,
      username: "mongouser",
      password: "mongopw",
      database: "testdb",
      auth_source: "admin",
    });
    const { getByText } = render(<MongoDBDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(getByText("mongo.example.com:27017")).toBeInTheDocument();
    expect(getByText("mongouser")).toBeInTheDocument();
    expect(getByText("testdb")).toBeInTheDocument();
    expect(getByText("admin")).toBeInTheDocument();
  });

  it("shows URI when connection_uri is present", () => {
    const asset = makeAsset("mongodb", {
      connection_uri: "mongodb+srv://user:pass@cluster.example.com/mydb",
    });
    const { getByText } = render(<MongoDBDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(getByText("mongodb+srv://user:pass@cluster.example.com/mydb")).toBeInTheDocument();
  });

  it("masks password field", () => {
    const asset = makeAsset("mongodb", {
      host: "mongo.local",
      port: 27017,
      password: "secretpw",
    });
    const { container, queryByText } = render(<MongoDBDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    const view = within(container);
    expect(view.getByText("\u25CF\u25CF\u25CF\u25CF\u25CF\u25CF")).toBeInTheDocument();
    expect(queryByText("secretpw")).not.toBeInTheDocument();
  });

  it("handles empty config without crashing", () => {
    const asset = makeAsset("mongodb", {});
    const { container } = render(<MongoDBDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(container).toBeDefined();
  });

  it("shows SSH tunnel from asset field when saved config omits ssh_asset_id", () => {
    const tunnelFn = vi.fn((id?: number) => (id === 10 ? "mongo-bastion" : null));
    const asset = makeAssetWithTunnel("mongodb", { host: "mongo.local", port: 27017 }, 10);
    const { getByText } = render(<MongoDBDetailInfoCard asset={asset} sshTunnelName={tunnelFn} />);
    expect(getByText("mongo-bastion")).toBeInTheDocument();
  });
});

describe("SerialDetailInfoCard", () => {
  it("renders serial configuration fields", () => {
    const asset = makeAsset("serial", {
      port_path: "COM3",
      baud_rate: 115200,
      data_bits: 8,
      stop_bits: "1",
      parity: "none",
      flow_control: "hardware",
    });
    const { getByText } = render(<SerialDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(getByText("COM3")).toBeInTheDocument();
    expect(getByText("115200")).toBeInTheDocument();
    expect(getByText("8")).toBeInTheDocument();
    expect(getByText("1")).toBeInTheDocument();
    expect(getByText("none")).toBeInTheDocument();
    expect(getByText("hardware")).toBeInTheDocument();
  });

  it("hides flow control when set to none", () => {
    const asset = makeAsset("serial", {
      port_path: "COM4",
      baud_rate: 9600,
      flow_control: "none",
    });
    const { queryByText } = render(<SerialDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(queryByText("none")).not.toBeInTheDocument();
  });

  it("handles empty config without crashing", () => {
    const asset = makeAsset("serial", {});
    const { container } = render(<SerialDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(container).toBeDefined();
  });

  it("handles invalid JSON safely", () => {
    const asset = makeAsset("serial", {});
    asset.Config = "{";
    const { container } = render(<SerialDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(container).toBeEmptyDOMElement();
  });
});

describe("K8sDetailInfoCard", () => {
  it("shows SSH tunnel from asset field", () => {
    const tunnelFn = vi.fn((id?: number) => (id === 7 ? "k8s-bastion" : null));
    const asset = makeAssetWithTunnel("k8s", { kubeconfig: "apiVersion: v1" }, 7);
    const { getByText } = render(<K8sDetailInfoCard asset={asset} sshTunnelName={tunnelFn} />);
    expect(getByText("k8s-bastion")).toBeInTheDocument();
  });
});

describe("EtcdDetailInfoCard", () => {
  it("shows SSH tunnel from asset field", () => {
    const tunnelFn = vi.fn((id?: number) => (id === 9 ? "etcd-bastion" : null));
    const asset = makeAssetWithTunnel("etcd", { endpoints: ["10.0.0.10:2379"] }, 9);
    const { getByText } = render(<EtcdDetailInfoCard asset={asset} sshTunnelName={tunnelFn} />);
    expect(getByText("etcd-bastion")).toBeInTheDocument();
  });

  it("falls back to config ssh_asset_id", () => {
    const tunnelFn = vi.fn((id?: number) => (id === 4 ? "legacy-bastion" : null));
    const asset = makeAsset("etcd", { endpoints: ["10.0.0.10:2379"], ssh_asset_id: 4 });
    const { getByText } = render(<EtcdDetailInfoCard asset={asset} sshTunnelName={tunnelFn} />);
    expect(getByText("legacy-bastion")).toBeInTheDocument();
  });
});

describe("数据库族详情卡 proxy 展示", () => {
  const PROXY = { type: "socks5", host: "proxy.example.com", port: 1080, username: "proxyuser" };

  it("DatabaseDetailInfoCard 渲染 proxy", () => {
    const asset = makeAsset("database", { driver: "mysql", host: "db", port: 3306, username: "root", proxy: PROXY });
    const { getByText } = render(<DatabaseDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(getByText("SOCKS5")).toBeInTheDocument();
    expect(getByText("proxy.example.com:1080")).toBeInTheDocument();
    expect(getByText("proxyuser")).toBeInTheDocument();
  });

  it("RedisDetailInfoCard 渲染 proxy", () => {
    const asset = makeAsset("redis", { host: "r", port: 6379, proxy: PROXY });
    const { getByText } = render(<RedisDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(getByText("proxy.example.com:1080")).toBeInTheDocument();
  });

  it("MongoDBDetailInfoCard 渲染 proxy", () => {
    const asset = makeAsset("mongodb", { host: "m", port: 27017, proxy: PROXY });
    const { getByText } = render(<MongoDBDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(getByText("proxy.example.com:1080")).toBeInTheDocument();
  });

  it("EtcdDetailInfoCard 渲染 proxy", () => {
    const asset = makeAsset("etcd", { endpoints: ["10.0.0.10:2379"], proxy: PROXY });
    const { getByText } = render(<EtcdDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(getByText("proxy.example.com:1080")).toBeInTheDocument();
  });

  it("KafkaDetailInfoCard 渲染 proxy", () => {
    const asset = makeAsset("kafka", { brokers: ["b1:9092"], proxy: PROXY });
    const { getByText } = render(<KafkaDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(getByText("proxy.example.com:1080")).toBeInTheDocument();
  });

  it("无 proxy 不渲染代理段", () => {
    const asset = makeAsset("redis", { host: "r", port: 6379 });
    const { queryByText } = render(<RedisDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(queryByText("proxy.example.com:1080")).not.toBeInTheDocument();
  });
});

describe("VNCDetailInfoCard", () => {
  it("shows the configured encryption policy and the compatible server default", () => {
    const configured = makeAsset("vnc", {
      host: "vnc.example.com",
      port: 5900,
      encryption: "always_maximum",
    });
    const { rerender } = render(<VNCDetailInfoCard asset={configured} sshTunnelName={noopTunnel} />);
    expect(screen.getByText("vnc.encryptionPolicy")).toBeInTheDocument();
    expect(screen.getByText("vnc.encryptionAlwaysMaximum")).toBeInTheDocument();

    rerender(<VNCDetailInfoCard asset={makeAsset("vnc", { host: "legacy.example.com" })} sshTunnelName={noopTunnel} />);
    expect(screen.getByText("vnc.encryptionServer")).toBeInTheDocument();
  });
});

describe("OSSDetailInfoCard", () => {
  it("渲染非机密字段,secret 打码,provider 展示为本地化标签", () => {
    const asset = makeAsset("oss", {
      provider: "aliyun-oss",
      endpoint: "oss-cn-hangzhou.aliyuncs.com",
      region: "cn-hangzhou",
      access_key_id: "AKIA",
      secret_access_key: "ENC",
      use_path_style: false,
      use_ssl: true,
    });
    const { getByText, queryByText } = render(<OSSDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(getByText("oss-cn-hangzhou.aliyuncs.com")).toBeInTheDocument();
    expect(getByText("cn-hangzhou")).toBeInTheDocument();
    expect(getByText("AKIA")).toBeInTheDocument();
    expect(getByText("●●●●●●")).toBeInTheDocument(); // MASKED_SECRET
    expect(getByText("oss.form.providerAliyunOSS")).toBeInTheDocument(); // t mock 原样返回 key
    expect(queryByText("ENC")).not.toBeInTheDocument(); // 绝不渲染明文密文
  });

  it("托管凭证态(无 secret)不渲染 credential_id,也不渲染打码行", () => {
    const asset = makeAsset("oss", {
      provider: "minio",
      endpoint: "http://localhost:9000",
      access_key_id: "AKIA",
      credential_id: 42,
      use_path_style: true,
      use_ssl: false,
    });
    const { queryByText } = render(<OSSDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(queryByText("42")).not.toBeInTheDocument(); // credential_id 绝不渲染
    expect(queryByText("●●●●●●")).not.toBeInTheDocument();
  });

  it("handles empty config without crashing", () => {
    const asset = makeAsset("oss", {});
    const { container } = render(<OSSDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
    expect(container).toBeDefined();
  });
});
