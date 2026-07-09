import type { AssetTypeDefinition } from "./types";
import { registry } from "./_register";
export { registerAssetType } from "./_register";

export function getAssetType(type: string): AssetTypeDefinition | undefined {
  // Try direct lookup first
  let def = registry.get(type);
  // If not found, try alias resolution
  if (!def) {
    def = getBuiltinTypes().find((d) => d.aliases.includes(type));
  }
  return def;
}

export function isBuiltinType(type: string): boolean {
  return registry.has(type);
}

export function getBuiltinTypes(): AssetTypeDefinition[] {
  return [...registry.values()];
}

// Side-effect imports — register all built-in types
import "./ssh";
import "./database";
import "./redis";
import "./mongodb";
import "./kafka";
import "./k8s";
import "./serial";
import "./local";
import "./etcd";

export type { AssetTypeDefinition, DetailInfoCardProps, PolicyDefinition, PolicyFieldDef } from "./types";
