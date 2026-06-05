import { describe, it, expect } from "vitest";
import {
  buildSerialConfig,
  parseSerialConfig,
  SERIAL_DEFAULTS,
} from "@/components/asset/SerialConfigSection.config";

describe("buildSerialConfig (锁旧 handleSubmit/handleTestSerial 字节一致)", () => {
  it("flow_control=none 时省略该键", () => {
    expect(
      buildSerialConfig({ portPath: "/dev/ttyUSB0", baudRate: 115200, dataBits: 8, stopBits: "1", parity: "none", flowControl: "none" })
    ).toBe('{"port_path":"/dev/ttyUSB0","baud_rate":115200,"data_bits":8,"stop_bits":"1","parity":"none"}');
  });
  it("flow_control=hardware 时追加该键(末位)", () => {
    expect(
      buildSerialConfig({ portPath: "/dev/ttyS0", baudRate: 9600, dataBits: 7, stopBits: "2", parity: "even", flowControl: "hardware" })
    ).toBe('{"port_path":"/dev/ttyS0","baud_rate":9600,"data_bits":7,"stop_bits":"2","parity":"even","flow_control":"hardware"}');
  });
});

describe("parseSerialConfig (锁旧 loadSerialConfig)", () => {
  it("回填全字段", () => {
    expect(
      parseSerialConfig('{"port_path":"/dev/ttyS0","baud_rate":9600,"data_bits":7,"stop_bits":"2","parity":"even","flow_control":"hardware"}')
    ).toEqual({ portPath: "/dev/ttyS0", baudRate: 9600, dataBits: 7, stopBits: "2", parity: "even", flowControl: "hardware" });
  });
  it("缺字段用默认", () => {
    expect(parseSerialConfig("{}")).toEqual(SERIAL_DEFAULTS);
  });
  it("非法 JSON 回退默认", () => {
    expect(parseSerialConfig("nope")).toEqual(SERIAL_DEFAULTS);
  });
});
