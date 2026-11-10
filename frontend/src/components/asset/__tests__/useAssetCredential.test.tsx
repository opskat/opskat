import { describe, it, expect, vi } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { useAssetCredential } from "../useAssetCredential";
import { ListCredentialsByType } from "../../../../wailsjs/go/system/System";
import type { credential_entity } from "../../../../wailsjs/go/models";

describe("useAssetCredential managed password load", () => {
  it("is loading until the request settles, then ready with the list", async () => {
    vi.mocked(ListCredentialsByType).mockResolvedValueOnce([
      { id: 1, name: "p", type: "password" } as credential_entity.Credential,
    ]);
    const { result } = renderHook(() => useAssetCredential());
    expect(result.current.managedPasswordsLoad.status).toBe("loading");
    await waitFor(() => expect(result.current.managedPasswordsLoad.status).toBe("ready"));
    expect(result.current.managedPasswords).toHaveLength(1);
  });

  it("reports the failure reason and recovers on reload", async () => {
    vi.mocked(ListCredentialsByType).mockRejectedValueOnce(new Error("vault locked"));
    const { result } = renderHook(() => useAssetCredential());
    await waitFor(() => expect(result.current.managedPasswordsLoad.status).toBe("error"));
    expect(result.current.managedPasswordsLoad.error).toBe("vault locked");

    vi.mocked(ListCredentialsByType).mockResolvedValueOnce([]);
    act(() => result.current.managedPasswordsLoad.reload());
    await waitFor(() => expect(result.current.managedPasswordsLoad.status).toBe("ready"));
  });
});
