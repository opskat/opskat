import { rmSync } from "node:fs";

export default function globalTeardown() {
  const dataDir = process.env.OPSKAT_DATA_DIR;
  if (dataDir && dataDir.includes("opskat-e2e-")) {
    rmSync(dataDir, { recursive: true, force: true });
  }
}
