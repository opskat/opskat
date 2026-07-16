export interface NodeInfo {
  name: string;
  status: string;
  roles: string[];
  version: string;
  cpu: string;
  memory: string;
  os: string;
  arch: string;
}

export interface NamespaceInfo {
  name: string;
  status: string;
}

export interface NamespaceResourcesData {
  namespace: string;
  pods: number;
  deployments: number;
  services: number;
  config_maps: number;
  secrets: number;
  pvcs: number;
  service_accounts: number;
}

export interface ClusterInfo {
  version: string;
  platform: string;
  nodes: NodeInfo[];
  namespaces: NamespaceInfo[];
}

export type InnerTabId =
  | "overview"
  | `node:${string}`
  | `ns:${string}`
  | `ns-res:${string}:${string}`
  | `pod:${string}:${string}`
  | `svc:${string}:${string}`
  | `cm:${string}:${string}`
  | `secret:${string}:${string}`
  | `log:${string}:${string}`
  | `log-deploy:${string}:${string}`;

export interface InnerTab {
  id: InnerTabId;
  label: string;
}

export interface ResourceTypeDef {
  key: keyof NamespaceResourcesData;
  labelKey: string;
  icon: React.FC<{ className?: string; style?: React.CSSProperties }>;
}

export interface PodListItem {
  name: string;
  namespace: string;
  status: string;
  node_name: string;
  pod_ip: string;
  age: string;
  ready: string;
  restart_count: number;
}

export interface DeploymentListItem {
  name: string;
  namespace: string;
  ready: string;
  up_to_date: number;
  available: number;
  age: string;
  pods: PodListItem[];
}

export interface ServicePortItem {
  name: string;
  port: number;
  target_port: string;
  node_port: number;
  protocol: string;
}

export interface ServiceListItem {
  name: string;
  namespace: string;
  type: string;
  cluster_ip: string;
  ports: ServicePortItem[];
  age: string;
}

export interface ConfigMapListItem {
  name: string;
  namespace: string;
  data: Record<string, string>;
  age: string;
}

export interface SecretListItem {
  name: string;
  namespace: string;
  type: string;
  data: Record<string, string>;
  age: string;
}

export interface ContainerDetail {
  name: string;
  image: string;
  state: string;
  ready: boolean;
  restart_count: number;
}

export interface ConditionDetail {
  type: string;
  status: string;
  reason: string;
  message: string;
}

export interface EventDetail {
  type: string;
  reason: string;
  message: string;
  first_time: string;
  last_time: string;
  count: number;
}

export interface PodDetail {
  name: string;
  namespace: string;
  status: string;
  node_name: string;
  pod_ip: string;
  host_ip: string;
  creation_time: string;
  age: string;
  ready: string;
  restart_count: number;
  qos_class: string;
  containers: ContainerDetail[];
  conditions: ConditionDetail[];
  events: EventDetail[];
  labels: Record<string, string>;
  annotations: Record<string, string>;
  yaml: string;
}
