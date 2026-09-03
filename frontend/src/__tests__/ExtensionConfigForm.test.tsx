import { describe, it, expect, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { ExtensionConfigForm } from "@/components/asset/ExtensionConfigForm";

describe("ExtensionConfigForm", () => {
  it('renders format="textarea" as multi-line textarea', () => {
    const schema = {
      type: "object",
      properties: {
        caCert: { type: "string", format: "textarea", title: "CA Certificate" },
      },
    };
    render(<ExtensionConfigForm extensionName="test" configSchema={schema} value={{}} onChange={() => {}} />);
    const el = screen.getByLabelText("CA Certificate");
    expect(el.tagName.toLowerCase()).toBe("textarea");
  });

  it("textarea change fires onChange with merged value", () => {
    const schema = {
      type: "object",
      properties: {
        caCert: { type: "string", format: "textarea", title: "CA Certificate" },
      },
    };
    const onChange = vi.fn();
    render(
      <ExtensionConfigForm extensionName="test" configSchema={schema} value={{ other: "keep" }} onChange={onChange} />
    );
    const el = screen.getByLabelText("CA Certificate") as HTMLTextAreaElement;
    fireEvent.change(el, { target: { value: "-----BEGIN CERT-----" } });
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith({ other: "keep", caCert: "-----BEGIN CERT-----" });
  });

  it("emits an integer property as a number, not a string", () => {
    // The guest unmarshals this config into its Go struct, so a numeric property that
    // leaves the form as "5" fails every later tool call with
    // `cannot unmarshal string into Go struct field ... of type int`.
    const schema = {
      type: "object",
      properties: { maxNotes: { type: "integer", title: "Note limit" } },
    };
    const onChange = vi.fn();
    render(
      <ExtensionConfigForm
        extensionName="test"
        configSchema={schema}
        value={{ notebook: "keep" }}
        onChange={onChange}
      />
    );
    fireEvent.change(screen.getByLabelText("Note limit"), { target: { value: "5" } });
    expect(onChange).toHaveBeenCalledWith({ notebook: "keep", maxNotes: 5 });
  });

  it("emits nothing for an integer property the user left empty", () => {
    const schema = {
      type: "object",
      properties: { maxNotes: { type: "integer", title: "Note limit" } },
    };
    const onChange = vi.fn();
    render(
      <ExtensionConfigForm extensionName="test" configSchema={schema} value={{ maxNotes: 5 }} onChange={onChange} />
    );
    fireEvent.change(screen.getByLabelText("Note limit"), { target: { value: "" } });
    expect(onChange).toHaveBeenCalledWith({ maxNotes: undefined });
  });

  it('renders format="password" as masked input', () => {
    const schema = {
      type: "object",
      properties: {
        secret: { type: "string", format: "password", title: "Secret" },
      },
    };
    render(<ExtensionConfigForm extensionName="test" configSchema={schema} value={{}} onChange={() => {}} />);
    const el = screen.getByLabelText("Secret") as HTMLInputElement;
    expect(el.tagName.toLowerCase()).toBe("input");
    expect(el.type).toBe("password");
  });
});
