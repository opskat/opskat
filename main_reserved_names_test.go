package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/repository/custom_type_repo"

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

// 反方向的保留：组合根把自定义类型查询注入 assettype，扩展类型与已有自定义类型标识重名时被拒。
func TestRegisterReservedTypeNamesGuardsExtensionTypesAgainstCustomTypes(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := gdb.AutoMigrate(&custom_type_entity.CustomType{}, &asset_entity.Asset{}); err != nil {
		t.Fatal(err)
	}
	db.SetDefault(gdb)
	old := custom_type_repo.CustomType()
	custom_type_repo.RegisterCustomType(custom_type_repo.New())
	t.Cleanup(func() {
		custom_type_repo.RegisterCustomType(old)
		assettype.SetCustomTypeSlugLookup(nil)
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	registerReservedTypeNames()
	if err := custom_type_svc.CustomType().Save(context.Background(), &custom_type_entity.CustomType{
		Name: "Acme Store", Slug: "acme-store", Icon: "gauge-circle", ExecMode: custom_type_entity.ExecModeHTTP,
		Fields: []custom_type_entity.Field{{Name: "host", Required: true}},
		HTTP:   &custom_type_entity.HTTPConfig{BaseURL: "https://{{host}}"},
	}); err != nil {
		t.Fatal(err)
	}

	err = assettype.RegisterExtensionType(assettype.ExtensionTypeSpec{
		Type: "acme-store", ExtensionName: "acme", ConfigFields: []string{"endpoint"},
	})
	if err == nil {
		assettype.Unregister("acme-store")
		t.Fatal("an extension type named like an existing custom type must be refused")
	}
	if !strings.Contains(err.Error(), `"acme-store"`) {
		t.Errorf("error %q does not name the custom type", err)
	}
}
