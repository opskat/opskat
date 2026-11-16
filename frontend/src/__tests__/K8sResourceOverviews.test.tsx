import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  ConfigMapOverview,
  IngressOverview,
  NodeOverview,
  PvcOverview,
  SecretOverview,
  ServiceOverview,
} from "@/components/k8s/detail/resources/overviews";
import type { K8sDetailProps } from "@/components/k8s/detail/registry";

const props = (typeKey: string, object: Record<string, unknown>, namespace = "default"): K8sDetailProps => ({
  assetId: 1,
  typeKey,
  namespace,
  name: (object.metadata as { name: string }).name,
  object,
});
const meta = (name: string, extra: Record<string, unknown> = {}) => ({
  name,
  creationTimestamp: "2026-01-02T03:04:05Z",
  ...extra,
});

describe("Service overview", () => {
  const object = {
    metadata: meta("web"),
    spec: {
      type: "LoadBalancer",
      clusterIP: "10.0.0.5",
      selector: { app: "web", tier: "fe" },
      externalIPs: ["1.2.3.4"],
      ports: [
        { name: "http", port: 80, targetPort: 8080, nodePort: 30080, protocol: "TCP" },
        { port: 53, targetPort: "dns", protocol: "UDP" },
      ],
    },
    status: { loadBalancer: { ingress: [{ ip: "9.9.9.9" }, { hostname: "lb.example.com" }] } },
  };

  it("shows type, cluster IP, selector, external IPs, load balancer ingress, created time and the ports table", () => {
    render(<ServiceOverview {...props("/services", object)} />);
    const root = screen.getByTestId("k8s-overview");
    for (const text of [
      "LoadBalancer",
      "10.0.0.5",
      "app=web,tier=fe",
      "1.2.3.4",
      "9.9.9.9, lb.example.com",
      "2026-01-02T03:04:05Z",
    ]) {
      expect(within(root).getByText(text)).toBeInTheDocument();
    }
    const rows = within(root).getAllByRole("row");
    expect(rows).toHaveLength(3); // header + 2 ports
    expect(
      within(rows[1])
        .getAllByRole("cell")
        .map((c) => c.textContent)
    ).toEqual(["http", "80", "8080", "30080", "TCP"]);
    expect(
      within(rows[2])
        .getAllByRole("cell")
        .map((c) => c.textContent)
    ).toEqual(["-", "53", "dns", "-", "UDP"]);
  });

  it("shows dashes for a service without cluster IP or ports", () => {
    render(<ServiceOverview {...props("/services", { metadata: meta("h"), spec: { type: "ClusterIP" } })} />);
    expect(screen.queryAllByRole("row")).toHaveLength(0);
    expect(screen.getByText("asset.k8sNone")).toBeInTheDocument();
  });
});

describe("Ingress overview", () => {
  const object = {
    metadata: meta("site"),
    spec: {
      ingressClassName: "nginx",
      rules: [
        {
          host: "a.example.com",
          http: {
            paths: [
              { path: "/", pathType: "Prefix", backend: { service: { name: "web", port: { number: 80 } } } },
              { path: "/api", pathType: "Prefix", backend: { service: { name: "api", port: { name: "http" } } } },
            ],
          },
        },
      ],
      tls: [{ hosts: ["a.example.com", "b.example.com"], secretName: "site-tls" }],
    },
    status: { loadBalancer: { ingress: [{ ip: "5.5.5.5" }] } },
  };

  it("shows class, rules as host / path / backend service:port, tls hosts and secret", () => {
    render(<IngressOverview {...props("networking.k8s.io/ingresses", object)} />);
    const root = screen.getByTestId("k8s-overview");
    expect(within(root).getByText("nginx")).toBeInTheDocument();
    expect(within(root).getByText("5.5.5.5")).toBeInTheDocument();
    const rows = within(root).getAllByRole("row");
    const cells = (i: number) =>
      within(rows[i])
        .getAllByRole("cell")
        .map((c) => c.textContent);
    expect(cells(1)).toEqual(["a.example.com", "/", "web:80"]);
    expect(cells(2)).toEqual(["a.example.com", "/api", "api:http"]);
    expect(cells(4)).toEqual(["a.example.com, b.example.com", "site-tls"]);
  });

  it("falls back to the legacy class annotation and shows * for host-less rules", () => {
    const legacy = {
      metadata: meta("old", { annotations: { "kubernetes.io/ingress.class": "traefik" } }),
      spec: {
        rules: [{ http: { paths: [{ path: "/x", backend: { service: { name: "s", port: { number: 1 } } } }] } }],
      },
    };
    render(<IngressOverview {...props("networking.k8s.io/ingresses", legacy)} />);
    expect(screen.getByText("traefik")).toBeInTheDocument();
    expect(screen.getByText("*")).toBeInTheDocument();
  });
});

describe("ConfigMap overview", () => {
  it("shows the key count, created time and every key with its value", () => {
    const object = { metadata: meta("cfg"), data: { "app.conf": "a=1\nb=2", empty: "" } };
    render(<ConfigMapOverview {...props("/configmaps", object)} />);
    expect(screen.getByText("app.conf")).toBeInTheDocument();
    expect(screen.getByText(/a=1\s+b=2/)).toBeInTheDocument();
    expect(screen.getByText("empty")).toBeInTheDocument();
    expect(screen.getByText("2026-01-02T03:04:05Z")).toBeInTheDocument();
  });

  it("says there is no data for an empty ConfigMap", () => {
    render(<ConfigMapOverview {...props("/configmaps", { metadata: meta("e") })} />);
    expect(screen.getByText("asset.k8sNone")).toBeInTheDocument();
  });
});

describe("Secret overview", () => {
  it("shows type and every key decoded from base64 in plain text with its byte length", () => {
    const object = {
      metadata: meta("s"),
      type: "kubernetes.io/basic-auth",
      data: { username: btoa("admin"), password: btoa("s3cr3t!!") },
    };
    render(<SecretOverview {...props("/secrets", object)} />);
    expect(screen.getByText("kubernetes.io/basic-auth")).toBeInTheDocument();
    expect(screen.getByText("admin")).toBeInTheDocument();
    expect(screen.getByText("5B")).toBeInTheDocument();
    expect(screen.getByText("s3cr3t!!")).toBeInTheDocument();
    expect(screen.getByText("8B")).toBeInTheDocument();
    expect(screen.queryByText(btoa("admin"))).toBeNull();
  });

  it("shows a value that is not valid base64 as-is", () => {
    render(
      <SecretOverview {...props("/secrets", { metadata: meta("s"), type: "Opaque", data: { raw: "not base64!" } })} />
    );
    expect(screen.getByText("not base64!")).toBeInTheDocument();
    expect(screen.getByText("11B")).toBeInTheDocument();
  });
});

describe("Node overview", () => {
  const object = {
    metadata: meta("n1", {
      labels: { "node-role.kubernetes.io/control-plane": "", "node-role.kubernetes.io/worker": "", app: "x" },
    }),
    spec: {
      unschedulable: true,
      taints: [
        { key: "dedicated", value: "gpu", effect: "NoSchedule" },
        { key: "node.kubernetes.io/unreachable", effect: "NoExecute" },
      ],
    },
    status: {
      conditions: [
        { type: "MemoryPressure", status: "False" },
        { type: "Ready", status: "True" },
      ],
      nodeInfo: {
        kubeletVersion: "v1.30.2",
        osImage: "Ubuntu 22.04",
        operatingSystem: "linux",
        architecture: "amd64",
        kernelVersion: "5.15.0",
        containerRuntimeVersion: "containerd://1.7.2",
      },
      capacity: { cpu: "8", memory: "16Gi", pods: "110" },
      allocatable: { cpu: "7800m", memory: "15Gi", pods: "100" },
      addresses: [
        { type: "InternalIP", address: "10.1.1.1" },
        { type: "Hostname", address: "n1" },
      ],
    },
  };

  it("shows status, roles, versions, capacity vs allocatable, addresses, taints and unschedulable", () => {
    render(<NodeOverview {...props("/nodes", object, "")} />);
    const root = screen.getByTestId("k8s-overview");
    for (const text of [
      "Ready",
      "control-plane, worker",
      "v1.30.2",
      "Ubuntu 22.04",
      "linux/amd64",
      "5.15.0",
      "containerd://1.7.2",
      "asset.k8sNodeUnschedulable",
      "10.1.1.1",
    ]) {
      expect(within(root).getAllByText(text).length).toBeGreaterThan(0);
    }
    const rows = within(root).getAllByRole("row");
    const cells = (i: number) =>
      within(rows[i])
        .getAllByRole("cell")
        .map((c) => c.textContent);
    expect(cells(1)).toEqual(["CPU", "8", "7800m"]);
    expect(cells(2)).toEqual(["asset.k8sMemory", "16Gi", "15Gi"]);
    expect(cells(3)).toEqual(["Pods", "110", "100"]);
    expect(within(root).getByText("dedicated=gpu:NoSchedule")).toBeInTheDocument();
    expect(within(root).getByText("node.kubernetes.io/unreachable:NoExecute")).toBeInTheDocument();
  });

  it("shows a schedulable node without taints as such", () => {
    const plain = { metadata: meta("n2"), spec: {}, status: { conditions: [{ type: "Ready", status: "False" }] } };
    render(<NodeOverview {...props("/nodes", plain, "")} />);
    expect(screen.getByText("NotReady")).toBeInTheDocument();
    expect(screen.queryByText("asset.k8sNodeUnschedulable")).toBeNull();
    expect(screen.getAllByText("asset.k8sNone").length).toBeGreaterThan(0);
  });
});

describe("PVC overview", () => {
  it("shows status, volume, capacity, access modes, storage class and volume mode", () => {
    const object = {
      metadata: meta("data"),
      spec: {
        volumeName: "pv-1",
        accessModes: ["ReadWriteOnce", "ReadOnlyMany"],
        storageClassName: "fast",
        volumeMode: "Filesystem",
        resources: { requests: { storage: "5Gi" } },
      },
      status: { phase: "Bound", capacity: { storage: "10Gi" } },
    };
    render(<PvcOverview {...props("/persistentvolumeclaims", object)} />);
    for (const text of ["Bound", "pv-1", "10Gi", "ReadWriteOnce, ReadOnlyMany", "fast", "Filesystem"]) {
      expect(screen.getByText(text)).toBeInTheDocument();
    }
  });

  it("falls back to the requested size while unbound", () => {
    const object = {
      metadata: meta("p"),
      spec: { resources: { requests: { storage: "5Gi" } } },
      status: { phase: "Pending" },
    };
    render(<PvcOverview {...props("/persistentvolumeclaims", object)} />);
    expect(screen.getByText("5Gi")).toBeInTheDocument();
  });
});
