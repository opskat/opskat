import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import { Folder, Server, Database } from "lucide-react";
import { EntityIcon } from "@/components/asset/AssetIcon";

function svgClass(el: Element) {
  return el.querySelector("svg")?.getAttribute("class") ?? "";
}

function renderIcon(icon: string | undefined, fallback?: typeof Folder) {
  const { container } = render(<EntityIcon icon={icon} fallback={fallback} />);
  const svg = container.querySelector("svg") as SVGElement;
  return { svg, cls: svgClass(container), container };
}

function reference(Icon: typeof Folder) {
  const { container } = render(<Icon />);
  return container.querySelector("svg")!.getAttribute("class");
}

describe("EntityIcon fallback", () => {
  it("renders the caller fallback for a name outside the icon vocabulary, without a brand color", () => {
    const { cls, svg } = renderIcon("no-such-icon", Folder);
    expect(cls).toBe(reference(Folder));
    expect(svg.style.color).toBe("");
  });

  it("renders the caller fallback for an unknown name even when it carries a custom color", () => {
    const { cls } = renderIcon("no-such-icon#ff0000", Folder);
    expect(cls).toBe(reference(Folder));
  });

  it("still renders a recognized name, including server itself", () => {
    expect(renderIcon("database", Folder).cls).toBe(reference(Database));
    expect(renderIcon("server", Folder).cls).toBe(reference(Server));
  });

  it("still defaults to Server when the fallback is Server or unspecified", () => {
    expect(renderIcon("no-such-icon").cls).toBe(reference(Server));
    expect(renderIcon("", undefined).cls).toBe(reference(Server));
  });
});
