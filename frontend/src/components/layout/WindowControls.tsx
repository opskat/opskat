import React, { useState, useEffect } from "react";
import { Minus, Square, Copy, X } from "lucide-react";
import { WindowMinimise, WindowToggleMaximise, WindowIsMaximised, Quit } from "../../../wailsjs/runtime/runtime";
import { usePlatform } from "@/hooks/usePlatform";

export function WindowControls() {
  const platform = usePlatform();
  const [maximised, setMaximised] = useState(false);
  const isWindows = platform === "windows";

  useEffect(() => {
    if (!isWindows) return;
    WindowIsMaximised().then(setMaximised);
  }, [isWindows]);

  if (!isWindows) return null;

  const handleToggleMaximise = async () => {
    WindowToggleMaximise();
    const max = await WindowIsMaximised();
    setMaximised(max);
  };

  return (
    <div className="fixed top-0 right-0 z-50 flex" style={{ "--wails-draggable": "no-drag" } as React.CSSProperties}>
      <button
        className="flex h-8 w-11 items-center justify-center text-muted-foreground hover:bg-muted/80 hover:text-foreground transition-colors"
        onClick={() => WindowMinimise()}
      >
        <Minus className="h-3.5 w-3.5" />
      </button>
      <button
        className="flex h-8 w-11 items-center justify-center text-muted-foreground hover:bg-muted/80 hover:text-foreground transition-colors"
        onClick={handleToggleMaximise}
      >
        {maximised ? <Copy className="h-3 w-3" /> : <Square className="h-3 w-3" />}
      </button>
      <button
        className="flex h-8 w-11 items-center justify-center text-muted-foreground hover:bg-destructive hover:text-destructive-foreground transition-colors"
        onClick={() => Quit()}
      >
        <X className="h-3.5 w-3.5" />
      </button>
    </div>
  );
}
