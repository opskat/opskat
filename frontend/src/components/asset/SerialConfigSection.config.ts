export interface SerialFormState {
  portPath: string;
  baudRate: number;
  dataBits: number;
  stopBits: string;
  parity: string;
  flowControl: string;
}

export const SERIAL_DEFAULTS: SerialFormState = {
  portPath: "",
  baudRate: 115200,
  dataBits: 8,
  stopBits: "1",
  parity: "none",
  flowControl: "none",
};

/** 保存序列化:镜像旧 handleSubmit serial 分支(键序 port_path→baud_rate→data_bits→stop_bits→parity→[flow_control])。 */
export function buildSerialConfig(state: SerialFormState): string {
  const cfg: Record<string, unknown> = {
    port_path: state.portPath,
    baud_rate: state.baudRate,
    data_bits: state.dataBits,
    stop_bits: state.stopBits,
    parity: state.parity,
  };
  if (state.flowControl !== "none") cfg.flow_control = state.flowControl;
  return JSON.stringify(cfg);
}

/** 编辑態回填:镜像旧 loadSerialConfig。解析失败→默认值。 */
export function parseSerialConfig(configJSON: string): SerialFormState {
  try {
    const cfg = JSON.parse(configJSON || "{}");
    return {
      portPath: cfg.port_path || "",
      baudRate: cfg.baud_rate || 115200,
      dataBits: cfg.data_bits || 8,
      stopBits: cfg.stop_bits || "1",
      parity: cfg.parity || "none",
      flowControl: cfg.flow_control || "none",
    };
  } catch {
    return { ...SERIAL_DEFAULTS };
  }
}
