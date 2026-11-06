package backup_svc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
)

// 备份恢复是从设置页发起的，拒绝无效自定义类型的报错会直接展示给用户，要跟随界面语言。
func TestImport_InvalidCustomTypeErrorFollowsUILanguage(t *testing.T) {
	broken := func() *BackupData {
		return &BackupData{CustomTypes: []*custom_type_entity.CustomType{{
			Slug: "broken", Name: "Broken", ExecMode: custom_type_entity.ExecModeCommand,
			Fields: []custom_type_entity.Field{{Name: "note"}},
		}}}
	}
	cases := []struct {
		lang string
		want string
	}{
		{"en", "custom type broken in the backup is invalid"},
		{"zh-CN", "备份中的自定义类型 broken 无效"},
	}
	for _, tc := range cases {
		t.Run(tc.lang, func(t *testing.T) {
			ctx := aictx.WithPolicyLang(setupBackupTest(t), tc.lang)
			_, err := Import(ctx, broken(), &ImportOptions{ImportAssets: true, Mode: "merge"}, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			var verr *custom_type_entity.ValidationError
			assert.ErrorAs(t, err, &verr, "the underlying validation issues stay inspectable")
		})
	}
}
