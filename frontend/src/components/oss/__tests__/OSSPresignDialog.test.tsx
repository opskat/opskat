import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";
import { toast } from "sonner";
import { OSSPresignDialog } from "../OSSPresignDialog";
import { OSSPresignGet, OSSPresignPut } from "../../../../wailsjs/go/oss/OSS";

beforeEach(() => {
  vi.mocked(OSSPresignGet).mockReset().mockResolvedValue("https://signed/get" as never);
  vi.mocked(OSSPresignPut).mockReset().mockResolvedValue("https://signed/put" as never);
});

function open() {
  render(<OSSPresignDialog open onOpenChange={vi.fn()} assetId={7} bucket="b" objectKey="docs/a.txt" />);
}

describe("OSSPresignDialog", () => {
  it("auto-generates a GET url on open with the default 1h expiry", async () => {
    open();
    await waitFor(() =>
      expect(OSSPresignGet).toHaveBeenCalledWith({ assetId: 7, bucket: "b", key: "docs/a.txt", expirySecs: 3600 })
    );
    expect((await screen.findByTestId("oss-share-url")).textContent).toBe("https://signed/get");
  });

  it("regenerates with the selected expiry", async () => {
    open();
    await screen.findByTestId("oss-share-url");
    fireEvent.click(screen.getByTestId("oss-share-expiry-86400"));
    await waitFor(() =>
      expect(OSSPresignGet).toHaveBeenLastCalledWith({ assetId: 7, bucket: "b", key: "docs/a.txt", expirySecs: 86400 })
    );
  });

  it("uses OSSPresignPut when the PUT method is selected", async () => {
    open();
    await screen.findByTestId("oss-share-url");
    fireEvent.click(screen.getByTestId("oss-share-method-put"));
    await waitFor(() =>
      expect(OSSPresignPut).toHaveBeenCalledWith({ assetId: 7, bucket: "b", key: "docs/a.txt", expirySecs: 3600 })
    );
    expect((await screen.findByTestId("oss-share-url")).textContent).toBe("https://signed/put");
  });

  it("drops a stale presign response after the method changes mid-flight", async () => {
    let resolveGet!: (url: string) => void;
    vi.mocked(OSSPresignGet)
      .mockReset()
      .mockReturnValue(
        new Promise((r) => {
          resolveGet = r;
        }) as never
      );
    vi.mocked(OSSPresignPut).mockReset().mockResolvedValue("https://signed/put" as never);

    open(); // fires GET (pending)
    await waitFor(() => expect(OSSPresignGet).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId("oss-share-method-put")); // fires PUT → resolves
    await screen.findByText("https://signed/put");

    await act(async () => {
      resolveGet("https://signed/get"); // stale → guarded out by reqId
    });
    expect(screen.getByTestId("oss-share-url").textContent).toBe("https://signed/put");
  });

  it("surfaces a toast.error when generation fails and shows no url", async () => {
    vi.mocked(OSSPresignGet).mockReset().mockRejectedValue(new Error("boom"));
    const errorSpy = vi.spyOn(toast, "error").mockImplementation(() => "" as never);

    open();
    await waitFor(() => expect(errorSpy).toHaveBeenCalled());
    expect(screen.queryByTestId("oss-share-url")).toBeNull();

    errorSpy.mockRestore();
  });

  it("copies the generated url with the primary copy button", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });

    open();
    await screen.findByTestId("oss-share-url");
    fireEvent.click(screen.getByTestId("oss-share-copy"));
    expect(writeText).toHaveBeenCalledWith("https://signed/get");
  });
});
