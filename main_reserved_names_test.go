package main

import (
	"slices"
	"testing"

	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
)

// 扩展类型与内置类型在同一张资产类型注册表里：自定义类型标识与扩展类型重名时要指出是哪个扩展，
// 与内置类型重名时不带扩展名；扩展卸载后它的类型名立即不再保留。
func TestReservedTypeNamesAttributeExtensionTypes(t *testing.T) {
	if err := assettype.RegisterExtensionType(assettype.ExtensionTypeSpec{
		Type:          "acme-store",
		ExtensionName: "acme",
		ConfigFields:  []string{"endpoint"},
	}); err != nil {
		t.Fatal(err)
	}
	unregistered := false
	t.Cleanup(func() {
		if !unregistered {
			assettype.Unregister("acme-store")
		}
	})

	firstMatch := func(names []custom_type_svc.ReservedName, name string) (custom_type_svc.ReservedName, bool) {
		i := slices.IndexFunc(names, func(r custom_type_svc.ReservedName) bool { return r.Name == name })
		if i < 0 {
			return custom_type_svc.ReservedName{}, false
		}
		return names[i], true
	}

	names := reservedTypeNames()
	if got, ok := firstMatch(names, "acme-store"); !ok || got.Extension != "acme" {
		t.Errorf("extension type: first match = %+v (found %v), want Extension \"acme\"", got, ok)
	}
	if got, ok := firstMatch(names, "ssh"); !ok || got.Extension != "" {
		t.Errorf("built-in type: first match = %+v (found %v), want no extension", got, ok)
	}

	assettype.Unregister("acme-store")
	unregistered = true
	if _, ok := firstMatch(reservedTypeNames(), "acme-store"); ok {
		t.Error("an unloaded extension's type must no longer be reserved")
	}
}
