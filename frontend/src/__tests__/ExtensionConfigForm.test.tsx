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

  // 认证方式由下拉选择：每种方式用到的字段只在选中它时出现，其余字段始终显示。
  describe("auth method fields", () => {
    const schema = {
      type: "object",
      properties: {
        endpoint: { type: "string", title: "Endpoint" },
        authType: { type: "string", title: "Auth", enum: ["none", "basic", "apiKey"], default: "none" },
        username: { type: "string", title: "Username" },
        password: { type: "string", format: "password", title: "Password" },
        apiKey: { type: "string", format: "password", title: "API key" },
      },
    };
    const auth = {
      selector: "authType",
      groups: [
        { when: "basic", fields: ["username", "password"] },
        { when: "apiKey", fields: ["apiKey"] },
      ],
    };
    const shown = () =>
      ["Endpoint", "Username", "Password", "API key"].filter((label) => screen.queryByLabelText(label));

    it("shows only the fields of the method the dropdown selects", () => {
      const { rerender } = render(
        <ExtensionConfigForm configSchema={schema} auth={auth} value={{ authType: "none" }} onChange={() => {}} />
      );
      expect(shown()).toEqual(["Endpoint"]);
      expect(screen.getByRole("combobox")).toBeInTheDocument();

      rerender(
        <ExtensionConfigForm configSchema={schema} auth={auth} value={{ authType: "basic" }} onChange={() => {}} />
      );
      expect(shown()).toEqual(["Endpoint", "Username", "Password"]);

      rerender(
        <ExtensionConfigForm configSchema={schema} auth={auth} value={{ authType: "apiKey" }} onChange={() => {}} />
      );
      expect(shown()).toEqual(["Endpoint", "API key"]);
    });

    it("a type declaring no auth selector keeps every field", () => {
      render(<ExtensionConfigForm configSchema={schema} value={{ authType: "none" }} onChange={() => {}} />);
      expect(shown()).toEqual(["Endpoint", "Username", "Password", "API key"]);
    });
  });
});
