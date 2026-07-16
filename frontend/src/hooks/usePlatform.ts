import { useEffect, useState } from "react";
import { Environment } from "../../wailsjs/runtime/runtime";

export type Platform = "darwin" | "windows" | "other";

export function usePlatform(): Platform {
  const [platform, setPlatform] = useState<Platform>("other");

  useEffect(() => {
    Environment().then((env) => {
      if (env.platform === "darwin") setPlatform("darwin");
      else if (env.platform === "windows") setPlatform("windows");
      else setPlatform("other");
    });
  }, []);

  return platform;
}
