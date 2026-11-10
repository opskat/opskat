import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { AsyncState } from "../AsyncState";

describe("AsyncState", () => {
  it("shows a loading indicator, not the empty state or content, while the request runs", () => {
    render(
      <AsyncState status="loading" empty="nothing here">
        <div>content</div>
      </AsyncState>
    );
    expect(screen.getByRole("status")).toBeInTheDocument();
    expect(screen.queryByText("nothing here")).not.toBeInTheDocument();
    expect(screen.queryByText("content")).not.toBeInTheDocument();
  });

  it("shows the failure reason and a retry action instead of the empty state when the request fails", () => {
    const onRetry = vi.fn();
    render(<AsyncState status="error" error="dial tcp: connection refused" onRetry={onRetry} empty="nothing here" />);

    expect(screen.getByRole("alert")).toHaveTextContent("dial tcp: connection refused");
    expect(screen.queryByText("nothing here")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "action.retry" }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("omits the retry action when the failure cannot be retried", () => {
    render(<AsyncState status="error" error="boom" />);
    expect(screen.getByRole("alert")).toHaveTextContent("boom");
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("shows the empty state only after a successful request with an empty result", () => {
    render(
      <AsyncState status="ready" empty="nothing here">
        <div>content</div>
      </AsyncState>
    );
    expect(screen.getByText("nothing here")).toBeInTheDocument();
    expect(screen.queryByText("content")).not.toBeInTheDocument();
  });

  it("renders the content after a successful request with data", () => {
    render(
      <AsyncState status="ready">
        <div>content</div>
      </AsyncState>
    );
    expect(screen.getByText("content")).toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
