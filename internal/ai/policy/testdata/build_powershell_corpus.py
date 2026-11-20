#!/usr/bin/env python3
"""把两个 PowerShell 版本的分析结果合并成 gzip 测试数据：build_fixture.py <ps51.json> <ps7.json> <out.json.gz>"""
import gzip, json, sys

a = json.load(open(sys.argv[1], encoding="utf-8-sig"))
b = json.load(open(sys.argv[2], encoding="utf-8-sig"))


def slim(r):
    cmds = []
    for c in r["commands"] or []:
        cmds.append({
            "text": c["text"], "name": c["name"], "op": c["op"],
            "elements": [{"type": e["type"], "text": e["text"], "param": e["param"]} for e in (c["elements"] or [])],
        })
    return {"errors": r["errors"], "commands": cmds, "issues": r["issues"] or []}


entries = []
for x, y in zip(a, b):
    assert x["command"] == y["command"]
    entries.append({"command": x["command"], "ps51": slim(x), "ps7": slim(y)})
doc = {
    "note": "Generated on Windows 11 by analyze_powershell_corpus.ps1 (Windows PowerShell 5.1.26100 and PowerShell 7.6.10) "
            "over the corpus from gen_powershell_corpus.py; do not edit by hand.",
    "entries": entries,
}
with gzip.open(sys.argv[3], "wt", encoding="utf-8") as f:
    json.dump(doc, f, ensure_ascii=False, separators=(",", ":"))
print(len(entries))
