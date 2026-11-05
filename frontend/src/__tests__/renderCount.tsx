import { Profiler, type ReactElement } from "react";

/** Wraps a tree in a Profiler and counts commits; stub out store-subscribing children so a commit means the root re-rendered. */
export function profiled(element: ReactElement) {
  const counter = { commits: 0 };
  const tree = (
    <Profiler
      id="probe"
      onRender={() => {
        counter.commits += 1;
      }}
    >
      {element}
    </Profiler>
  );
  return { tree, counter };
}
