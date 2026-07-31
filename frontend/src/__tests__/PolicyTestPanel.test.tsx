import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { PolicyTestPanel } from "@/components/asset/PolicyTestPanel";

// PLACEHOLDER_MAP selects the input hint by policyType. Before OSS had an entry, the
// placeholder was simply undefined (i18next `t(undefined)` renders nothing useful) and the
// panel gave no guidance on the policy-string syntax OSS actually expects
// (`<action> <bucket>/<key>`, not the exec DSL — see spec §3.3/§4.1).
describe("PolicyTestPanel for oss", () => {
  it("selects the OSS policy-string placeholder instead of leaving it unset", () => {
    render(<PolicyTestPanel policyType="oss" buildPolicyJSON={() => "{}"} />);

    // react-i18next is mocked to the identity function in tests, so `t(key)` renders `key`
    // itself — asserting the placeholder equals the key confirms PLACEHOLDER_MAP actually
    // wires "oss" to asset.policyTestOSSPlaceholder.
    expect(screen.getByPlaceholderText("asset.policyTestOSSPlaceholder")).toBeInTheDocument();
  });
});
