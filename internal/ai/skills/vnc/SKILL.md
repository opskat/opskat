---
name: vnc
description: "VNC remote desktop assets. Config fields for put_asset; no command surface — exec is not supported for this type."
---

# VNC assets

VNC assets are opened as an interactive desktop session in the app. There is **no command
surface**: `exec` is not supported for this type, and there is nothing to script.

## Asset config (for put_asset)

| field | type | required | notes |
|---|---|---|---|
| `host` | string | yes | Hostname or IP |
| `port` | number | no | Defaults to 5900 |
| `username` | string | no | Only needed if the VNC server requires one |
| `password` | string | no | Stored encrypted; never echoed back |
| `file_ssh_asset_id` | number | no | SSH asset backing the SSH/SFTP file-transfer channel; omit to disable file transfer |

Example:

    put_asset(name="lab-desktop", type="vnc", config={"host":"10.0.1.20","password":"s3cret"})
