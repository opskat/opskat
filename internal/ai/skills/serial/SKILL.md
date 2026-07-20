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
