---
name: rdp
description: "Windows remote desktop assets. Config fields for put_asset; no command surface — exec is not supported for this type."
---

# RDP assets

RDP assets are opened as an interactive desktop session in the app. There is **no command
surface**: `exec` is not supported for this type, and there is nothing to script.

## Asset config (for put_asset)

| field | type | required | notes |
|---|---|---|---|
| `host` | string | yes | Hostname or IP |
| `port` | number | no | Defaults to 3389 |
| `username` | string | yes | Local or domain account |
| `password` | string | no | Stored encrypted; never echoed back |
| `domain` | string | no | Windows domain; omit for local accounts |
| `width` | number | no | Initial desktop width, defaults to 1280 |
| `height` | number | no | Initial desktop height, defaults to 720 |
| `clipboard` | string | no | `"true"` / `"false"`, defaults to true |

Example:

    put_asset(name="win-jump", type="rdp", config={"host":"10.0.1.9","username":"admin","domain":"CORP"})
