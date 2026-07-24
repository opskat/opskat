---
name: serial
description: "Send commands to a serial console device (switch, firewall) via exec. Requires an already-connected desktop serial session."
---

# Serial assets

## Command syntax

Pass the console command verbatim as `command`:

- `display version`
- `show ip interface brief`
- `display current-configuration`

## Notes

- The serial session **must already be connected by the user** in a terminal
  tab. There is no way to open one from here.
- Output is collected until the line goes quiet (2s) or a 15s cap is reached.
- Serial assets are unavailable from `opsctl`; they require the desktop app.
- The `scope` parameter is not used by serial assets.

## Asset config (for put_asset)

No host/port/username/credentials — a serial asset addresses a local device path instead.

| field | type | required | notes |
|---|---|---|---|
| `port_path` | string | yes | e.g. `"COM3"` on Windows, `"/dev/ttyUSB0"` / `"/dev/cu.usbserial-XYZ"` on Linux/macOS |
| `baud_rate` | number | yes | e.g. `9600`, `115200` |
| `data_bits` | number | no | `5`-`8`, defaults to `8` |
| `stop_bits` | string | no | `"1"` (default), `"1.5"`, or `"2"` |
| `parity` | string | no | `"none"` (default), `"odd"`, `"even"`, `"mark"`, or `"space"` |
| `flow_control` | string | no | `"none"` or `"hardware"` (RTS/CTS); empty means no flow control |
