import { describe, expect, it } from "vitest";
import {
  addFilterCondition,
  addSortCriterion,
  createFilterCondition,
  createSortCriterion,
  removeFilterItemsByColumn,
  toFilterRequest,
  toSortRequest,
  toggleFilterNegator,
  toggleFilterJoin,
  toggleSortDirection,
  type TableFilterItem,
} from "@/lib/tableFilter";

describe("table filter helpers", () => {
  it("adds the next unused column in display order", () => {
    const items = [createFilterCondition("email", "email")];
    const next = addFilterCondition(items, ["id", "email", "name"], "mysql");

    expect(next).toHaveLength(2);
    expect(next[1]).toMatchObject({ kind: "condition", column: "id", operator: "=", enabled: true });
  });

  it("toggles joins between AND and OR", () => {
    const items = [createFilterCondition("email", "email", { join: "and" })];

    expect(toggleFilterJoin(items, "email")[0]).toMatchObject({ join: "or" });
    expect(toggleFilterJoin(toggleFilterJoin(items, "email"), "email")[0]).toMatchObject({ join: "and" });
  });

  it("removes filters for a column from nested groups", () => {
    const items: TableFilterItem[] = [
      createFilterCondition("name", "name", { value: "A" }),
      {
        kind: "group",
        id: "group-1",
        join: "and",
        items: [
          createFilterCondition("name-nested", "name", { value: "B" }),
          createFilterCondition("email-nested", "email", { value: "a@example.com" }),
        ],
      },
    ];

    expect(removeFilterItemsByColumn(items, "name")).toEqual([
      {
        kind: "group",
        id: "group-1",
        join: "and",
        items: [createFilterCondition("email-nested", "email", { value: "a@example.com" })],
      },
    ]);
  });

  it("toggles filter negators for conditions and groups", () => {
    const items: TableFilterItem[] = [
      createFilterCondition("name", "name", { value: "A" }),
      { kind: "group", id: "group-1", join: "and", items: [] },
    ];

    const negatedCondition = toggleFilterNegator(items, "name");
    expect(negatedCondition[0]).toMatchObject({ operator: "!=" });

    const negatedGroup = toggleFilterNegator(items, "group-1")[1];
    expect(negatedGroup).toMatchObject({ kind: "group", negated: true });
  });

  it("adds sort criteria by unused field order and toggles direction", () => {
    const first = addSortCriterion([], ["id", "email", "name"], "mysql", "sort-id");
    const second = addSortCriterion(first, ["id", "email", "name"], "mysql", "sort-email");

    expect(second).toEqual([createSortCriterion("sort-id", "id"), createSortCriterion("sort-email", "email")]);
    expect(toggleSortDirection(second, "sort-id")[0]).toMatchObject({ dir: "desc" });
  });

  it("turns filter items into the structured request the driver receives", () => {
    const items: TableFilterItem[] = [
      createFilterCondition("name", "name", { value: "A", join: "or" }),
      createFilterCondition("gone", "deleted_at", { value: null, operator: "!=" }),
      createFilterCondition("blank", "email", { operator: "contains" }),
      createFilterCondition("off", "age", { operator: "between", value: [18, 30], enabled: false }),
      {
        kind: "group",
        id: "group-1",
        join: "and",
        negated: true,
        items: [
          createFilterCondition("status", "status", { operator: "in_list", value: "a, b" }),
          { kind: "group", id: "group-2", join: "or", enabled: false, items: [] },
        ],
      },
    ];

    // 请求里只有条件本身:没有界面用的 id,也没有任何 SQL。没填的值标成 unset,与填了 NULL 区分开。
    expect(toFilterRequest(items)).toEqual([
      { kind: "condition", join: "or", disabled: false, column: "name", operator: "=", value: "A" },
      { kind: "condition", join: "and", disabled: false, column: "deleted_at", operator: "!=", value: null },
      { kind: "condition", join: "and", disabled: false, column: "email", operator: "contains", unset: true },
      { kind: "condition", join: "and", disabled: true, column: "age", operator: "between", value: [18, 30] },
      {
        kind: "group",
        join: "and",
        disabled: false,
        negated: true,
        items: [
          { kind: "condition", join: "and", disabled: false, column: "status", operator: "in_list", value: "a, b" },
          { kind: "group", join: "or", disabled: true, negated: false, items: [] },
        ],
      },
    ]);
    expect(JSON.parse(JSON.stringify(toFilterRequest(items)))[1]).toHaveProperty("value", null);
  });

  it("turns sort criteria into the structured request the driver receives", () => {
    expect(
      toSortRequest([createSortCriterion("sort-id", "id", "asc"), createSortCriterion("sort-name", "name", "desc")])
    ).toEqual([
      { column: "id", dir: "asc" },
      { column: "name", dir: "desc" },
    ]);
  });
});
