import { registerDetailOverview } from "../registry";
import {
  ConfigMapOverview,
  IngressOverview,
  NodeOverview,
  PvcOverview,
  SecretOverview,
  ServiceOverview,
} from "./overviews";

registerDetailOverview("Service", ServiceOverview);
registerDetailOverview("Ingress", IngressOverview);
registerDetailOverview("ConfigMap", ConfigMapOverview);
registerDetailOverview("Secret", SecretOverview);
registerDetailOverview("Node", NodeOverview);
registerDetailOverview("PersistentVolumeClaim", PvcOverview);
