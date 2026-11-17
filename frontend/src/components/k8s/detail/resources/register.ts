import { registerDetailOverview } from "../registry";
import {
  ConfigMapOverview,
  IngressOverview,
  NodeOverview,
  PvcOverview,
  SecretOverview,
  ServiceOverview,
} from "./overviews";

registerDetailOverview("/services", ServiceOverview);
registerDetailOverview("networking.k8s.io/ingresses", IngressOverview);
registerDetailOverview("/configmaps", ConfigMapOverview);
registerDetailOverview("/secrets", SecretOverview);
registerDetailOverview("/nodes", NodeOverview);
registerDetailOverview("/persistentvolumeclaims", PvcOverview);
