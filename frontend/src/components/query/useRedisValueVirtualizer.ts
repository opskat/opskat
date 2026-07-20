import { useRef } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";

const VALUE_ROW_HEIGHT = 30;

export function useRedisValueVirtualizer(count: number) {
  const scrollRef = useRef<HTMLDivElement>(null);

  // react-virtual returns a mutable instance during render, which is incompatible with React Compiler semantics.
  // eslint-disable-next-line react-hooks/incompatible-library
  const virtualizer = useVirtualizer({
    count,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => VALUE_ROW_HEIGHT,
    overscan: 20,
  });

  return { scrollRef, virtualizer };
}
