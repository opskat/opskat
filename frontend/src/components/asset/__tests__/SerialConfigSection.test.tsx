import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import { createRef } from "react";
import { vi } from "vitest";
import { buildSerialConfig, parseSerialConfig, SERIAL_DEFAULTS } from "@/components/asset/SerialConfigSection.config";
import { SerialConfigSection } from "@/components/asset/SerialConfigSection";
import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";
import { asset_entity } from "../../../../wailsjs/go/models";

vi.mock("../../../../wailsjs/go/serial/Serial", () => ({ ListSerialPorts: () => Promise.resolve([]) }));

const fakeCtx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => p };

describe("buildSerialConfig (锁旧 handleSubmit/handleTestSerial 字节一致)", () => {
  it("flow_control=none 时省略该键", () => {
    expect(
      buildSerialConfig({
        portPath: "/dev/ttyUSB0",
        baudRate: 115200,
        dataBits: 8,
        stopBits: "1",
        parity: "none",
        flowControl: "none",
      })
    ).toBe('{"port_path":"/dev/ttyUSB0","baud_rate":115200,"data_bits":8,"stop_bits":"1","parity":"none"}');
  });
  it("flow_control=hardware 时追加该键(末位)", () => {
    expect(
      buildSerialConfig({
        portPath: "/dev/ttyS0",
        baudRate: 9600,
        dataBits: 7,
        stopBits: "2",
        parity: "even",
        flowControl: "hardware",
      })
    ).toBe(
      '{"port_path":"/dev/ttyS0","baud_rate":9600,"data_bits":7,"stop_bits":"2","parity":"even","flow_control":"hardware"}'
    );
  });
});

describe("parseSerialConfig (锁旧 loadSerialConfig)", () => {
  it("回填全字段", () => {
    expect(
      parseSerialConfig(
        '{"port_path":"/dev/ttyS0","baud_rate":9600,"data_bits":7,"stop_bits":"2","parity":"even","flow_control":"hardware"}'
      )
    ).toEqual({
      portPath: "/dev/ttyS0",
      baudRate: 9600,
      dataBits: 7,
      stopBits: "2",
      parity: "even",
      flowControl: "hardware",
    });
  });
  it("缺字段用默认", () => {
    expect(parseSerialConfig("{}")).toEqual(SERIAL_DEFAULTS);
  });
  it("非法 JSON 回退默认", () => {
    expect(parseSerialConfig("nope")).toEqual(SERIAL_DEFAULTS);
  });
});

describe("SerialConfigSection ref 契约", () => {
  it("编辑态:buildConfig 与 buildTestConfig 同形,password 为空", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "serial",
      Config: '{"port_path":"/dev/ttyUSB0","baud_rate":115200,"data_bits":8,"stop_bits":"1","parity":"none"}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<SerialConfigSection ref={ref} editAsset={editAsset} ctx={fakeCtx} onValidityChange={() => {}} />);
    const built = await ref.current!.buildConfig(fakeCtx);
    expect(built).toEqual({
      configJSON: '{"port_path":"/dev/ttyUSB0","baud_rate":115200,"data_bits":8,"stop_bits":"1","parity":"none"}',
      sshTunnelId: 0,
    });
    const tc = await ref.current!.buildTestConfig!(fakeCtx);
    expect(tc).toEqual({ assetType: "serial", configJSON: built.configJSON, password: "" });
  });

  it("创建态(无端口):上报 canSave/canTest=false + formMissingSerialPort", () => {
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<SerialConfigSection ref={ref} ctx={fakeCtx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({
      canTest: false,
      canSave: false,
      saveDisabledReason: "asset.formMissingSerialPort",
    });
  });

  it("编辑态(有端口):上报 canSave/canTest=true,无 reason", () => {
    const editAsset = new asset_entity.Asset({ Type: "serial", Config: '{"port_path":"/dev/ttyS0"}' });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<SerialConfigSection ref={ref} editAsset={editAsset} ctx={fakeCtx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" });
  });
});
