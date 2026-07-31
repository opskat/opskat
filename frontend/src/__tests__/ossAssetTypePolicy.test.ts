import { describe, expect, it } from "vitest";
import { getBuiltinTypes } from "@/lib/assetTypes";

// AssetDetail.tsx only renders the policy editor card when `def.policy` is truthy
// (`if (!def?.policy) return null;`). OSS used to register `policy: undefined`, so the
// asset form had no policy editing area at all. This locks in that the registered
// definition now carries a real PolicyDefinition shaped like the other command-style
// types (allow_list / deny_list), with the "oss" policyType the backend policy kind
// (`policy.PolicyKindOSS = "oss"`) expects.
describe("oss asset type policy definition", () => {
  it("registers a policy definition so the asset form's policy editor renders for oss", () => {
    const oss = getBuiltinTypes().find((def) => def.type === "oss");
    expect(oss?.policy).toBeDefined();
    expect(oss?.policy?.policyType).toBe("oss");
    expect(oss?.policy?.fields.map((f) => f.key).sort()).toEqual(["allow_list", "deny_list"]);
  });
});
