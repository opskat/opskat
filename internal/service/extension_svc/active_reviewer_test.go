package extension_svc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/extension_state_entity"
	"github.com/opskat/opskat/internal/pkg/appversion"
	"github.com/opskat/opskat/pkg/extension"
)

func TestActiveCommandReviewerRequiresLoadedReviewer(t *testing.T) {
	dir := t.TempDir()
	extDir := filepath.Join(dir, "rev")
	require.NoError(t, os.MkdirAll(extDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(extDir, "main.wasm"), []byte("wasm"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(extDir, "manifest.json"), []byte(`{"name":"rev"}`), 0o600))
	sum, err := extension.PackageSHA256(extDir, "main.wasm")
	require.NoError(t, err)

	mgr := extension.NewManager(dir, func(string) extension.HostProvider { return nil }, nopLogger())
	mgr.PutForTest(&extension.Extension{
		Name:          "rev",
		Dir:           extDir,
		PackageSHA256: sum,
		Manifest: &extension.Manifest{
			Name:         "rev",
			Version:      "1.0.0",
			Backend:      extension.ManifestBackend{Binary: "main.wasm"},
			Capabilities: extension.Capabilities{CommandReview: true},
			Reviewer: &extension.ReviewerDef{
				Questions:     []extension.ReviewQuestion{{ID: "destructive", I18n: extension.I18nNames{Name: map[string]string{"en": "Destroys data"}}}},
				MaxItems:      4,
				MaxCommandLen: 100,
			},
		},
		Plugin: &extension.Plugin{},
	})
	repo := &memStateRepo{rows: map[string]*extension_state_entity.ExtensionState{
		"rev": {Name: "rev", Enabled: true, Source: InstallOriginStore, PackageSHA256: sum},
	}}
	svc := &Service{manager: mgr, stateRepo: repo, logger: nopLogger()}

	got, err := svc.ActiveCommandReviewer(context.Background(), "rev")
	require.NoError(t, err)
	require.Equal(t, "rev", got.Name)
	require.Equal(t, 4, got.MaxItems)

	// 审核跑的是加载时读进来、已编译好的代码：比对的是那份字节的摘要，每次审核不再重读磁盘上的包。
	require.NoError(t, os.Remove(filepath.Join(extDir, "main.wasm")))
	_, err = svc.ActiveCommandReviewer(context.Background(), "rev")
	require.NoError(t, err)
	assert.True(t, svc.ListCommandReviewers(context.Background(), "en")[0].Usable)

	repo.rows["rev"].PackageSHA256 = "dead"
	_, err = svc.ActiveCommandReviewer(context.Background(), "rev")
	require.Error(t, err)
}

// writeReviewerPackage 写一个最小的已安装审核扩展目录，返回它的包摘要。
func writeReviewerPackage(t *testing.T, root, name string) string {
	t.Helper()
	extDir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(extDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(extDir, "main.wasm"), []byte("wasm"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(extDir, "manifest.json"),
		[]byte(`{"name":"`+name+`","version":"1.0.0","hostABI":"2.3","backend":{"runtime":"wasm","binary":"main.wasm"},"capabilities":{"commandReview":true}}`), 0o600))
	sum, err := extension.PackageSHA256(extDir, "main.wasm")
	require.NoError(t, err)
	return sum
}

func reviewerExtension(name, dir, sum string) *extension.Extension {
	return &extension.Extension{
		Name:          name,
		Dir:           dir,
		PackageSHA256: sum,
		Manifest: &extension.Manifest{
			Name:         name,
			Version:      "1.0.0",
			Backend:      extension.ManifestBackend{Binary: "main.wasm"},
			Capabilities: extension.Capabilities{CommandReview: true},
			Reviewer: &extension.ReviewerDef{
				Questions:     []extension.ReviewQuestion{{ID: "destructive", I18n: extension.I18nNames{Name: map[string]string{"en": "Destroys data"}}}},
				MaxItems:      4,
				MaxCommandLen: 100,
			},
		},
		Plugin: &extension.Plugin{},
	}
}

func TestActiveCommandReviewerLoadsOnDemand(t *testing.T) {
	root := t.TempDir()
	sum := writeReviewerPackage(t, root, "rev")
	mgr := extension.NewManager(root, func(string) extension.HostProvider { return nil }, nopLogger())
	repo := &memStateRepo{rows: map[string]*extension_state_entity.ExtensionState{
		"rev": {Name: "rev", Enabled: true, Source: InstallOriginStore, PackageSHA256: sum},
	}}
	var loads []string
	svc := &Service{manager: mgr, stateRepo: repo, logger: nopLogger(),
		reviewerLoader: func(_ context.Context, dir string) error {
			loads = append(loads, dir)
			mgr.PutForTest(reviewerExtension("rev", dir, sum))
			return nil
		}}

	got, err := svc.ActiveCommandReviewer(context.Background(), "rev")
	require.NoError(t, err)
	require.Equal(t, "rev", got.Name)
	require.Equal(t, []string{mgr.ExtDir("rev")}, loads)

	_, err = svc.ActiveCommandReviewer(context.Background(), "rev")
	require.NoError(t, err)
	require.Len(t, loads, 1, "已加载的审核器不再重复加载")
}

func TestActiveCommandReviewerOnDemandChecksBeforeRunningCode(t *testing.T) {
	cases := map[string]*extension_state_entity.ExtensionState{
		"disabled":        {Name: "rev", Enabled: false, Source: InstallOriginStore},
		"digest mismatch": {Name: "rev", Enabled: true, Source: InstallOriginStore, PackageSHA256: "dead"},
	}
	for name, state := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			sum := writeReviewerPackage(t, root, "rev")
			if state.PackageSHA256 == "" {
				state.PackageSHA256 = sum
			}
			mgr := extension.NewManager(root, func(string) extension.HostProvider { return nil }, nopLogger())
			repo := &memStateRepo{rows: map[string]*extension_state_entity.ExtensionState{"rev": state}}
			loaded := false
			svc := &Service{manager: mgr, stateRepo: repo, logger: nopLogger(),
				reviewerLoader: func(context.Context, string) error { loaded = true; return nil }}

			_, err := svc.ActiveCommandReviewer(context.Background(), "rev")
			require.Error(t, err)
			require.False(t, loaded, "检查不过时不能运行扩展代码")
		})
	}
}

func TestActiveCommandReviewerWithoutOnDemandRequiresLoaded(t *testing.T) {
	root := t.TempDir()
	sum := writeReviewerPackage(t, root, "rev")
	mgr := extension.NewManager(root, func(string) extension.HostProvider { return nil }, nopLogger())
	repo := &memStateRepo{rows: map[string]*extension_state_entity.ExtensionState{
		"rev": {Name: "rev", Enabled: true, Source: InstallOriginStore, PackageSHA256: sum},
	}}
	svc := &Service{manager: mgr, stateRepo: repo, logger: nopLogger()}
	_, err := svc.ActiveCommandReviewer(context.Background(), "rev")
	require.Error(t, err)
}

// 非正式版上本地安装的审核器可以用，只是标"未经签名"；正式版上不能用。
func TestActiveCommandReviewerAcceptsUnsignedOnNonRelease(t *testing.T) {
	root := t.TempDir()
	sum := writeReviewerPackage(t, root, "rev")
	mgr := extension.NewManager(root, func(string) extension.HostProvider { return nil }, nopLogger())
	mgr.PutForTest(reviewerExtension("rev", mgr.ExtDir("rev"), sum))
	repo := &memStateRepo{rows: map[string]*extension_state_entity.ExtensionState{
		"rev": {Name: "rev", Enabled: true, Source: InstallOriginLocal, PackageSHA256: sum},
	}}
	svc := &Service{manager: mgr, stateRepo: repo, logger: nopLogger()}

	useApp(t, appversion.Info{Kind: appversion.KindNightly, Version: "1.0.0-nightly.1"})
	_, err := svc.ActiveCommandReviewer(context.Background(), "rev")
	require.NoError(t, err)
	list := svc.ListCommandReviewers(context.Background(), "en")
	require.Len(t, list, 1)
	assert.True(t, list[0].Usable)
	assert.False(t, list[0].Signed)

	useApp(t, appversion.Info{Kind: appversion.KindRelease, Version: "1.0.0"})
	_, err = svc.ActiveCommandReviewer(context.Background(), "rev")
	require.Error(t, err)
	list = svc.ListCommandReviewers(context.Background(), "en")
	require.Len(t, list, 1)
	assert.False(t, list[0].Usable)
	assert.NotEmpty(t, list[0].Reason)
}

// writeManifest 在 root 下装一个只有 manifest 的扩展目录；commandReview 决定它是不是审核扩展。
func writeManifest(t *testing.T, root, name string, commandReview bool) {
	t.Helper()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	caps := `{}`
	if commandReview {
		caps = `{"commandReview":true}`
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"),
		[]byte(`{"name":"`+name+`","version":"1.0.0","hostABI":"2.3","backend":{"runtime":"wasm","binary":"main.wasm"},"capabilities":`+caps+`}`), 0o600))
}

// 没选审核扩展时：只装了一个启用中的就用它；一个都没有、或有好几个都要用户处理。
func TestCommandReviewerNamePicksTheOnlyEnabledReviewer(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "notebook", false)
	writeManifest(t, root, "rev-a", true)
	writeManifest(t, root, "rev-off", true)
	mgr := extension.NewManager(root, func(string) extension.HostProvider { return nil }, nopLogger())
	repo := &memStateRepo{rows: map[string]*extension_state_entity.ExtensionState{
		"rev-off": {Name: "rev-off", Enabled: false},
	}}
	svc := &Service{manager: mgr, stateRepo: repo, logger: nopLogger()}
	ctx := context.Background()

	name, err := svc.CommandReviewerName(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, "rev-a", name)

	// 选过的审核扩展被停用或卸载后，选择不再算数：设置里的选择没人去清，
	// 照它用就每次审核都失败，而只剩一个审核扩展时设置页不显示选择器，用户改不回来。
	name, err = svc.CommandReviewerName(ctx, "rev-off")
	require.NoError(t, err)
	assert.Equal(t, "rev-a", name, "选的已停用，用唯一启用中的")
	name, err = svc.CommandReviewerName(ctx, "gone")
	require.NoError(t, err)
	assert.Equal(t, "rev-a", name, "选的已卸载，用唯一启用中的")

	writeManifest(t, root, "rev-b", true)
	_, err = svc.CommandReviewerName(ctx, "")
	assert.ErrorIs(t, err, ErrSeveralCommandReviewers)
	name, err = svc.CommandReviewerName(ctx, "rev-b")
	require.NoError(t, err)
	assert.Equal(t, "rev-b", name, "选了启用中的就用它")
	_, err = svc.CommandReviewerName(ctx, "gone")
	assert.ErrorIs(t, err, ErrSeveralCommandReviewers, "选的已卸载、启用中的有好几个，要用户重选")

	empty := &Service{manager: extension.NewManager(t.TempDir(), func(string) extension.HostProvider { return nil }, nopLogger()), stateRepo: repo, logger: nopLogger()}
	_, err = empty.CommandReviewerName(ctx, "")
	assert.ErrorIs(t, err, ErrNoCommandReviewer)
}

// 设置页和权限模式卡片看到的审核状态：实际在用哪个审核扩展，没有可用的时说明原因。
func TestCommandReviewStateTellsWhichReviewerIsInUse(t *testing.T) {
	root := t.TempDir()
	sum := writeReviewerPackage(t, root, "rev")
	mgr := extension.NewManager(root, func(string) extension.HostProvider { return nil }, nopLogger())
	mgr.PutForTest(reviewerExtension("rev", mgr.ExtDir("rev"), sum))
	repo := &memStateRepo{rows: map[string]*extension_state_entity.ExtensionState{
		"rev": {Name: "rev", Enabled: true, Source: InstallOriginStore, PackageSHA256: sum},
	}}
	svc := &Service{manager: mgr, stateRepo: repo, logger: nopLogger()}
	ctx := context.Background()

	st := svc.CommandReviewState(ctx, "en", "")
	assert.Equal(t, "rev", st.Active, "没选、只有一个就用它")
	assert.Empty(t, st.Problem)
	require.Len(t, st.Reviewers, 1)

	st = svc.CommandReviewState(ctx, "en", "gone")
	assert.Equal(t, "rev", st.Active, "选的已卸载，用唯一的那个")
	assert.Empty(t, st.Selected, "已卸载的选择不再报告为选中")
	assert.Empty(t, st.Problem)

	repo.rows["rev"].PackageSHA256 = "dead"
	st = svc.CommandReviewState(ctx, "en", "")
	assert.Empty(t, st.Active)
	assert.Equal(t, "reviewer_unavailable", st.Problem, "唯一的那个检查不过")

	empty := &Service{manager: extension.NewManager(t.TempDir(), func(string) extension.HostProvider { return nil }, nopLogger()), stateRepo: repo, logger: nopLogger()}
	st = empty.CommandReviewState(ctx, "en", "")
	assert.Equal(t, "no_reviewer", st.Problem)
}

// 没有审核扩展（都停用、或一个都没装）时 reviewers 是空数组，不是 null：前端直接遍历它。
func TestCommandReviewStateWithoutReviewersEncodesAnEmptyList(t *testing.T) {
	repo := &memStateRepo{rows: map[string]*extension_state_entity.ExtensionState{}}
	svc := &Service{manager: extension.NewManager(t.TempDir(), func(string) extension.HostProvider { return nil }, nopLogger()), stateRepo: repo, logger: nopLogger()}

	data, err := json.Marshal(svc.CommandReviewState(context.Background(), "en", ""))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"reviewers":[]`)
}
