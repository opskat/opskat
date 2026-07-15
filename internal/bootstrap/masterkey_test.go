package bootstrap

import "testing"

// TestMasterKeyOpts 覆盖 NoKeychain 的四种组合：便携与否 × 是否用
// --data-dir / OPSKAT_DATA_DIR 覆盖数据目录。NoKeychain 必须跟随
// "实际使用的数据目录是不是便携目录"，而不是"可执行文件旁边有没有 data 目录"：
// 便携的 opsctl 指向已安装数据目录时若跳过凭据管理器，会读不到 key 而重新
// 生成一个，把错误的 master.key 写进已安装目录。
func TestMasterKeyOpts(t *testing.T) {
	const portable = "/opt/opskat/data"

	tests := []struct {
		name           string
		portableDir    string
		dataDir        string
		wantNoKeychain bool
	}{
		{
			name:           "非便携且未覆盖：走凭据管理器",
			portableDir:    "",
			dataDir:        "/home/u/.config/opskat",
			wantNoKeychain: false,
		},
		{
			name:           "便携且未覆盖：数据目录即便携目录，跳过凭据管理器",
			portableDir:    portable,
			dataDir:        portable,
			wantNoKeychain: true,
		},
		{
			name:           "便携但覆盖到已安装目录：仍走凭据管理器",
			portableDir:    portable,
			dataDir:        "/home/u/.config/opskat",
			wantNoKeychain: false,
		},
		{
			name:           "非便携但覆盖数据目录：仍走凭据管理器",
			portableDir:    "",
			dataDir:        "/tmp/custom",
			wantNoKeychain: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := portableDir
			t.Cleanup(func() { portableDir = orig })
			portableDir = func() string { return tt.portableDir }

			got := masterKeyOpts(Options{MasterKey: "explicit-key"}, tt.dataDir)

			if got.NoKeychain != tt.wantNoKeychain {
				t.Errorf("NoKeychain = %v, 期望 %v", got.NoKeychain, tt.wantNoKeychain)
			}
			if got.DataDir != tt.dataDir {
				t.Errorf("DataDir = %q, 期望 %q", got.DataDir, tt.dataDir)
			}
			if got.Explicit != "explicit-key" {
				t.Errorf("Explicit = %q, 期望透传 %q", got.Explicit, "explicit-key")
			}
		})
	}
}
