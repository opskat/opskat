import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";
import { toast } from "sonner";
import { OSSPresignDialog } from "../OSSPresignDialog";
import { OSSPresignGet, OSSPresignPut } from "../../../../wailsjs/go/oss/OSS";

beforeEach(() => {
  vi.mocked(OSSPresignGet)
    .mockReset()
    .mockResolvedValue("https://signed/get" as never);
  vi.mocked(OSSPresignPut)
    .mockReset()
    .mockResolvedValue("https://signed/put" as never);
});

function open() {
  render(<OSSPresignDialog open onOpenChange={vi.fn()} assetId={7} bucket="b" objectKey="docs/a.txt" />);
}

describe("OSSPresignDialog", () => {
  it("generates a GET url with the selected expiry", async () => {
    open();
    fireEvent.click(screen.getByTestId("oss-share-expiry-86400"));
    fireEvent.click(screen.getByTestId("oss-share-generate"));
    await waitFor(() =>
      expect(OSSPresignGet).toHaveBeenCalledWith({ assetId: 7, bucket: "b", key: "docs/a.txt", expirySecs: 86400 })
    );
    expect((await screen.findByTestId<HTMLTextAreaElement>("oss-share-url")).value).toBe("https://signed/get");
  });

  it("uses OSSPresignPut when the PUT method is selected", async () => {
    open();
    fireEvent.click(screen.getByTestId("oss-share-method-put"));
    fireEvent.click(screen.getByTestId("oss-share-generate"));
    await waitFor(() =>
      expect(OSSPresignPut).toHaveBeenCalledWith({ assetId: 7, bucket: "b", key: "docs/a.txt", expirySecs: 3600 })
    );
  });

  it("clears a stale url when the method changes", async () => {
    open();
    fireEvent.click(screen.getByTestId("oss-share-generate"));
    await screen.findByDisplayValue("https://signed/get");
    fireEvent.click(screen.getByTestId("oss-share-method-put"));
    expect(screen.getByTestId<HTMLTextAreaElement>("oss-share-url").value).toBe("");
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
    vi.mocked(OSSPresignPut)
      .mockReset()
      .mockResolvedValue("https://signed/put" as never);

    open();
    fireEvent.click(screen.getByTestId("oss-share-generate"));
    await waitFor(() => expect(OSSPresignGet).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId("oss-share-method-put"));

    await act(async () => {
      resolveGet("https://signed/get");
    });

    expect(screen.getByTestId<HTMLTextAreaElement>("oss-share-url").value).toBe("");
  });

  it("clears a stale url when the expiry changes", async () => {
    open();
    fireEvent.click(screen.getByTestId("oss-share-generate"));
    await screen.findByDisplayValue("https://signed/get");
    fireEvent.click(screen.getByTestId("oss-share-expiry-86400"));
    expect(screen.getByTestId<HTMLTextAreaElement>("oss-share-url").value).toBe("");
  });

  it("surfaces a toast.error when generation fails", async () => {
    vi.mocked(OSSPresignGet).mockReset().mockRejectedValue(new Error("boom"));
    const errorSpy = vi.spyOn(toast, "error").mockImplementation(() => "" as never);

    open();
    fireEvent.click(screen.getByTestId("oss-share-generate"));

    await waitFor(() => expect(errorSpy).toHaveBeenCalled());
    expect(screen.getByTestId<HTMLTextAreaElement>("oss-share-url").value).toBe("");

    errorSpy.mockRestore();
  });
});
