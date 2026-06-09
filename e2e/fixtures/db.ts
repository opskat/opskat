import { DatabaseSync } from "node:sqlite";
import { join } from "node:path";

export interface AssetRow {
  id: number;
  name: string;
  type: string;
  status: number;
}

// Opens the e2e temp opskat.db read-only and looks up an asset by name.
// Independent of the app's service layer — proves the row really hit disk.
// Uses Node's built-in node:sqlite (no native dependency).
export function findAssetByName(name: string): AssetRow | undefined {
  const dataDir = process.env.OPSKAT_DATA_DIR;
  if (!dataDir) throw new Error("OPSKAT_DATA_DIR not set");
  const db = new DatabaseSync(join(dataDir, "opskat.db"), { readOnly: true });
  try {
    db.exec("PRAGMA busy_timeout = 5000");
    const row = db
      .prepare("SELECT id, name, type, status FROM assets WHERE name = ?")
      .get(name);
    return row as AssetRow | undefined;
  } finally {
    db.close();
  }
}
