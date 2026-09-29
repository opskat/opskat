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
    render(<ExtensionConfigForm configSchema={schema} value={{}} onChange={() => {}} />);
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
    render(<ExtensionConfigForm configSchema={schema} value={{ other: "keep" }} onChange={onChange} />);
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
    render(<ExtensionConfigForm configSchema={schema} value={{ notebook: "keep" }} onChange={onChange} />);
    fireEvent.change(screen.getByLabelText("Note limit"), { target: { value: "5" } });
    expect(onChange).toHaveBeenCalledWith({ notebook: "keep", maxNotes: 5 });
  });

  it("emits nothing for an integer property the user left empty", () => {
    const schema = {
      type: "object",
      properties: { maxNotes: { type: "integer", title: "Note limit" } },
    };
    const onChange = vi.fn();
    render(<ExtensionConfigForm configSchema={schema} value={{ maxNotes: 5 }} onChange={onChange} />);
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
    render(<ExtensionConfigForm configSchema={schema} value={{}} onChange={() => {}} />);
    const el = screen.getByLabelText("Secret") as HTMLInputElement;
    expect(el.tagName.toLowerCase()).toBe("input");
    expect(el.type).toBe("password");
  });

  it("shows enum option labels instead of raw values, and the select fills its column", () => {
    const schema = {
      type: "object",
      properties: {
        auth: { type: "string", title: "Auth", enum: ["none", "apiKey"], enumLabels: ["No auth", "API key"] },
      },
    };
    render(<ExtensionConfigForm configSchema={schema} value={{ auth: "apiKey" }} onChange={() => {}} />);
    const trigger = screen.getByRole("combobox");
    expect(trigger).toHaveTextContent("API key");
    expect(trigger).not.toHaveTextContent("apiKey");
    expect(trigger.className).toContain("w-full");
  });

  it("falls back to the raw option value when the schema declares no labels", () => {
    const schema = { type: "object", properties: { mode: { type: "string", title: "Mode", enum: ["fast", "safe"] } } };
    render(<ExtensionConfigForm configSchema={schema} value={{ mode: "safe" }} onChange={() => {}} />);
    expect(screen.getByRole("combobox")).toHaveTextContent("safe");
  });

  it("puts a field error under its field and marks only that field invalid", () => {
    const schema = {
      type: "object",
      properties: { username: { type: "string", title: "Username" }, host: { type: "string", title: "Host" } },
    };
    render(
      <ExtensionConfigForm
        configSchema={schema}
        value={{}}
        onChange={() => {}}
        fieldErrors={{ username: "username is required" }}
      />
    );
    expect(screen.getByText("username is required")).toBeInTheDocument();
    expect(screen.getByLabelText("Username")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByLabelText("Host")).not.toHaveAttribute("aria-invalid", "true");
  });
});
