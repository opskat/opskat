package migrations

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
)

// 迁移建出的表必须能直接承载实体（列名、JSON 列），并对标识做唯一约束。
func TestMigration202609280001_CustomTypesTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:migration202609280001?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)

	m := migration202609280001()
	require.NoError(t, m.Migrate(db), "首次执行")
	require.NoError(t, m.Migrate(db), "重复执行不能报错")

	ct := &custom_type_entity.CustomType{
		Slug: "grafana", Name: "Grafana", Icon: "grafana", ExecMode: custom_type_entity.ExecModeHTTP,
		Fields:        []custom_type_entity.Field{{Name: "host", Required: true}},
		HTTP:          &custom_type_entity.HTTPConfig{BaseURL: "https://{{host}}"},
		Usage:         "# usage",
		DefaultPolicy: &policy.CommandPolicy{AllowList: []string{"GET *"}},
		Createtime:    1, Updatetime: 2,
	}
	require.NoError(t, db.Create(ct).Error)

	var got custom_type_entity.CustomType
	require.NoError(t, db.First(&got, ct.ID).Error)
	assert.Equal(t, *ct, got)

	dup := *ct
	dup.ID = 0
	assert.Error(t, db.Create(&dup).Error, "标识必须唯一")

	require.NoError(t, m.Rollback(db))
	assert.False(t, db.Migrator().HasTable("custom_types"))
}
