import { describe, expect, it } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { IndexList } from "../IndexList";
import type { DesignerIndex } from "../model";
import { dbdriver } from "../../../../../wailsjs/go/models";
import { fakeDriverWith } from "@/__tests__/dbDriverFixtures";

function index(patch: Partial<DesignerIndex>): DesignerIndex {
  return {
    name: "idx",
    type: "normal",
    columns: [{ name: "a", expression: false, descending: false, prefixLength: 0 }],
    include: [],
    options: {},
    comment: "",
    readOnlyReason: "",
    undroppable: false,
    ...patch,
  };
}

const rowOf = (name: string) => screen.getByText(name).closest('[role="row"]') as HTMLElement;

const withIndexEditing = (editing: Partial<dbdriver.IndexEditing>) =>
  new dbdriver.Descriptor({
    ...fakeDriverWith(["alter_table"]),
    indexEditing: { types: [], nameScope: "table", ...editing },
  });

describe("IndexList", () => {
  it("类型取驱动声明的名称;未声明时内置类型用宿主文案,其余显示原样", () => {
    const descriptor = withIndexEditing({ types: [{ id: "unique", label: "唯一键" }] });
    render(
      <IndexList
        descriptor={descriptor}
        indexes={[
          index({ name: "i1", type: "unique" }),
          index({ name: "i2", type: "fulltext" }),
          index({ name: "i3", type: "bitmap" }),
        ]}
      />
    );
    expect(within(rowOf("i1")).getByText("唯一键")).toBeInTheDocument();
    expect(within(rowOf("i2")).getByText("query.designer.indexType.fulltext")).toBeInTheDocument();
    expect(within(rowOf("i3")).getByText("bitmap")).toBeInTheDocument();
  });

  it("列按次序显示,降序带箭头,表达式是虚线框,前缀长度与 INCLUDE 列写出来", () => {
    render(
      <IndexList
        descriptor={fakeDriverWith([])}
        indexes={[
          index({
            name: "i1",
            columns: [
              { name: "lower(note)", expression: true, descending: false, prefixLength: 0 },
              { name: "created_at", expression: false, descending: true, prefixLength: 0 },
              { name: "title", expression: false, descending: false, prefixLength: 8 },
            ],
            include: ["status"],
          }),
        ]}
      />
    );
    const parts = within(rowOf("i1")).getAllByTestId("index-column");
    expect(parts.map((p) => p.textContent)).toEqual(["lower(note)", "created_at", "title(8)"]);
    expect(parts[0]).toHaveAttribute("data-expression", "true");
    expect(parts[0].className).toContain("border-dashed");
    expect(parts[1]).not.toHaveAttribute("data-expression");
    expect(within(parts[1]).getByLabelText("query.designer.descending")).toBeInTheDocument();
    expect(within(parts[0]).queryByLabelText("query.designer.descending")).not.toBeInTheDocument();
    expect(rowOf("i1")).toHaveTextContent("INCLUDE (status)");
  });

  it("只读索引显示锁与原因", () => {
    render(
      <IndexList
        descriptor={fakeDriverWith([])}
        indexes={[index({ name: "i1", readOnlyReason: "表达式索引,只能删除" }), index({ name: "i2" })]}
      />
    );
    expect(within(rowOf("i1")).getByLabelText("query.designer.indexReadOnly")).toBeInTheDocument();
    expect(within(rowOf("i1")).getByText("表达式索引,只能删除")).toBeInTheDocument();
    expect(within(rowOf("i2")).queryByLabelText("query.designer.indexReadOnly")).not.toBeInTheDocument();
  });

  it("驱动声明的选项按枚举 / 开关通用渲染,声明了索引注释时有注释列", () => {
    const descriptor = withIndexEditing({
      comments: true,
      options: [
        { key: "method", label: "方法", type: "enum", enum: ["BTREE", "HASH"] },
        { key: "clustered", label: "聚集", type: "switch" },
      ],
    });
    render(
      <IndexList
        descriptor={descriptor}
        indexes={[
          index({ name: "i1", options: { method: "HASH", clustered: true }, comment: "按状态查" }),
          index({ name: "i2" }),
        ]}
      />
    );
    for (const header of ["方法", "聚集", "query.designer.column.comment"]) {
      expect(screen.getByRole("columnheader", { name: header })).toBeInTheDocument();
    }
    expect(within(rowOf("i1")).getByText("HASH")).toBeInTheDocument();
    expect(within(rowOf("i1")).getByText("query.designer.yes")).toBeInTheDocument();
    expect(within(rowOf("i1")).getByText("按状态查")).toBeInTheDocument();
    expect(within(rowOf("i2")).getByText("query.designer.optionDefault")).toBeInTheDocument();
  });

  it("没声明索引注释、索引也没有注释时没有注释列", () => {
    render(<IndexList descriptor={fakeDriverWith([])} indexes={[index({ name: "i1" })]} />);
    expect(screen.queryByRole("columnheader", { name: "query.designer.column.comment" })).not.toBeInTheDocument();
  });

  it("索引不能在设计器里改时顶部说明,并留出更新驱动的入口", () => {
    render(
      <IndexList
        descriptor={fakeDriverWith([])}
        indexes={[index({ name: "i1" })]}
        notEditableNotice
        updateDriverAction={<button>go-update</button>}
      />
    );
    expect(screen.getByText("query.designer.indexesNotEditable")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "go-update" })).toBeInTheDocument();
  });
});
