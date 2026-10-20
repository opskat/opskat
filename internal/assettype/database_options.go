package assettype

import (
	"fmt"
	"maps"
	"slices"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// 驱动专属字段（Descriptor.DriverFields）在入参里是 driver_options 对象，存在资产配置的同名键下。
const argDriverOptions = "driver_options"

// driverOptionsArg 取出入参里的 driver_options；没给（或为 null）返回 nil。
func driverOptionsArg(args map[string]any) (map[string]any, error) {
	v, ok := args[argDriverOptions]
	if !ok || v == nil {
		return nil, nil
	}
	opts, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", argDriverOptions)
	}
	return opts, nil
}

// encryptDriverOptions 返回口令类取值加密后的副本。空串口令不写入：更新时它表示「不改」。
func encryptDriverOptions(d dbdriver.Descriptor, opts map[string]any) (map[string]any, error) {
	out := maps.Clone(opts)
	for _, f := range d.DriverFields {
		if f.Type != dbdriver.FieldTypePassword {
			continue
		}
		plaintext, ok := out[f.Key].(string)
		if !ok {
			continue
		}
		if plaintext == "" {
			delete(out, f.Key)
			continue
		}
		encrypted, err := credential_svc.Default().Encrypt(plaintext)
		if err != nil {
			return nil, fmt.Errorf("encrypt driver option %q: %w", f.Key, err)
		}
		out[f.Key] = encrypted
	}
	return out, nil
}

// validateCreateDriverOptions 按驱动的声明校验创建入参里的 driver_options，返回补好默认值的完整取值。
func validateCreateDriverOptions(d dbdriver.Descriptor, args map[string]any) (map[string]any, error) {
	opts, err := driverOptionsArg(args)
	if err != nil {
		return nil, err
	}
	if err := d.CheckDriverOptions(opts); err != nil {
		return nil, err
	}
	return d.CompleteDriverOptions(opts)
}

// mergeDriverOptions 把更新入参里的 driver_options 叠加到已存的取值上，并校验合并结果。
func mergeDriverOptions(d dbdriver.Descriptor, stored, updates map[string]any) (map[string]any, error) {
	if err := d.CheckDriverOptions(updates); err != nil {
		return nil, err
	}
	encrypted, err := encryptDriverOptions(d, updates)
	if err != nil {
		return nil, err
	}
	merged := maps.Clone(stored)
	if merged == nil {
		merged = map[string]any{}
	}
	maps.Copy(merged, encrypted)
	return d.CompleteDriverOptions(merged)
}

// viewDriverOptions 是资产视图里的专属字段：口令类不给内容，只列出哪些已设置。
func viewDriverOptions(d dbdriver.Descriptor, opts map[string]any) (values map[string]any, secretsSet []string) {
	values = map[string]any{}
	for key, v := range opts {
		f, declared := d.DriverField(key)
		switch {
		case !declared:
			// 已存的键不在当前声明里（驱动升级删了它）：没有声明就不知道是不是口令，不展示。
		case f.Type == dbdriver.FieldTypePassword:
			secretsSet = append(secretsSet, key)
		default:
			values[key] = v
		}
	}
	slices.Sort(secretsSet)
	return values, secretsSet
}

// DriverOptionsHaveSecret 报告 config 的 driver_options 里是否带了口令类的明文。驱动取自 config 的
// driver，局部更新没带时取 stored（已存资产）的驱动；驱动不可用时没有谁能说出哪些是口令，返回
// false（这样的请求在落库时本来就会被拒）。
func DriverOptionsHaveSecret(config map[string]any, stored *asset_entity.Asset) bool {
	opts, err := driverOptionsArg(config)
	if err != nil || len(opts) == 0 {
		return false
	}
	id := ArgString(config, "driver")
	if id == "" && stored != nil {
		if cfg, err := stored.GetDatabaseConfig(); err == nil {
			id = string(cfg.Driver)
		}
	}
	drv, err := dbregistry.Lookup(asset_entity.DatabaseDriver(id))
	if err != nil {
		return false
	}
	for _, f := range drv.Descriptor().DriverFields {
		if f.Type == dbdriver.FieldTypePassword && configValuePresentString(opts[f.Key]) {
			return true
		}
	}
	return false
}

func configValuePresentString(v any) bool {
	s, ok := v.(string)
	return ok && s != ""
}
