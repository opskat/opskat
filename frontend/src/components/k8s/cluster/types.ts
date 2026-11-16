// Shapes returned by GetK8sPodDetail (internal/pkg/k8s/client.go), used by the legacy pod detail.

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
