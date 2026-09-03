package extension_svc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/extreg"
	"github.com/opskat/opskat/internal/model/entity/extension_state_entity"
	"github.com/opskat/opskat/internal/repository/extension_data_repo/mock_extension_data_repo"
	"github.com/opskat/opskat/internal/repository/extension_state_repo/mock_extension_state_repo"
	"github.com/opskat/opskat/internal/service/snippet_svc"
	"github.com/opskat/opskat/pkg/extension"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

// testConfigSchema is the smallest configSchema a manifest can declare: asset types must
// describe at least one config property (Manifest.validateAssetScope), because the asset
// handler derived from it owns the generic create contract.
func testConfigSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"endpoint": map[string]any{"type": "string"}},
	}
}

// registeredAssetTypes reports which of the service's loaded extensions actually reached
// the shared asset-type registry. The assertion deliberately lands on internal/assettype
// rather than on the bridge: extension asset types now go into the same table built-in
// types use, so "registered" means visible there — the bridge only tracks what is loaded.
func registeredAssetTypes(svc *Service) []string {
	out := make([]string, 0)
	for _, name := range svc.Bridge().ListNames() {
		ext := svc.Manager().GetExtension(name)
		if ext == nil {
			continue
		}
		for _, at := range ext.Manifest.AssetTypes {
			if _, ok := assettype.Get(at.Type); ok {
				out = append(out, at.Type)
			}
		}
	}
	sort.Strings(out)
	return out
}

// closeService 关掉服务并把它登记进进程级注册表的条目撤掉——注册表跨测试函数存活，
// 留着会让下一个用例撞上"同名资产类型已注册"。
func closeService(ctx context.Context, svc *Service) {
	for _, name := range svc.Bridge().ListNames() {
		extreg.Unregister(name)
	}
	svc.Close(ctx)
}

// minimalWASM is the smallest valid WASM module (magic + version header).
var minimalWASM = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

func writeTestExtension(dir, name string) {
	extDir := filepath.Join(dir, name)
	_ = os.MkdirAll(extDir, 0755)
	manifest := map[string]any{
		"name":    name,
		"version": "1.0.0",
		"hostABI": "1.0",
		"backend": map[string]any{"runtime": "wasm", "binary": "main.wasm"},
		"assetTypes": []map[string]any{
			{"type": name, "i18n": map[string]any{"name": name + ".name"},
				"configSchema": testConfigSchema()},
		},
		"policies": map[string]any{"type": "ext:" + name},
		"tools": []map[string]any{
			{
				"name":       "test_tool",
				"i18n":       map[string]any{"description": "a tool"},
				"parameters": map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
	}
	data, _ := json.Marshal(manifest)
	_ = os.WriteFile(filepath.Join(extDir, "manifest.json"), data, 0644)
	_ = os.WriteFile(filepath.Join(extDir, "main.wasm"), minimalWASM, 0644)
}

// writeTestExtensionWithSnippets writes an extension manifest declaring a single
// snippet category + seed. assetType is matched so manifest validation passes.
func writeTestExtensionWithSnippets(dir, name, assetType, seedKey string) {
	const catID = "kafka"

	extDir := filepath.Join(dir, name)
	_ = os.MkdirAll(extDir, 0755)
	manifest := map[string]any{
		"name":    name,
		"version": "1.0.0",
		"hostABI": "1.0",
		"backend": map[string]any{"runtime": "wasm", "binary": "main.wasm"},
		"assetTypes": []map[string]any{
			{"type": assetType, "i18n": map[string]any{"name": assetType}, "configSchema": testConfigSchema()},
		},
		"policies": map[string]any{"type": "ext:" + name},
		"snippets": map[string]any{
			"categories": []map[string]any{
				{"id": catID, "assetType": assetType, "i18n": map[string]any{"name": catID}},
			},
			"seed": []map[string]any{
				{"key": seedKey, "name": seedKey, "category": catID, "content": "echo " + seedKey},
			},
		},
	}
	data, _ := json.Marshal(manifest)
	_ = os.WriteFile(filepath.Join(extDir, "manifest.json"), data, 0644)
	_ = os.WriteFile(filepath.Join(extDir, "main.wasm"), minimalWASM, 0644)
}

// fakeSnippetHook captures invocations for snippet-hook-related tests.
type fakeSnippetHook struct {
	mu                sync.Mutex
	syncCalls         []syncCall
	removeCalls       []string
	refreshCount      int
	knownIDs          []string
	syncErr           error
	existingExtCatIDs []string // 供 KnownCategoryIDs 返回模拟已安装扩展分类
}

type syncCall struct {
	ExtName string
	Seeds   []snippet_svc.SeedDef
}

func (f *fakeSnippetHook) SyncExtensionSeeds(_ context.Context, extName string, seeds []snippet_svc.SeedDef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncCalls = append(f.syncCalls, syncCall{ExtName: extName, Seeds: seeds})
	return f.syncErr
}
func (f *fakeSnippetHook) RemoveExtensionSeeds(_ context.Context, extName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removeCalls = append(f.removeCalls, extName)
	return nil
}
func (f *fakeSnippetHook) RefreshCategories() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshCount++
}
func (f *fakeSnippetHook) KnownCategoryIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]string{"shell", "sql", "redis", "mongo", "prompt"}, f.existingExtCatIDs...)
	f.knownIDs = out
	return out
}

func newTestManager(dir string) *extension.Manager {
	return extension.NewManager(dir, func(extName string) extension.HostProvider {
		return extension.NewDefaultHostProvider(extension.DefaultHostConfig{Logger: zap.NewNop()})
	}, zap.NewNop())
}

func TestService(t *testing.T) {
	Convey("Service", t, func() {
		ctrl := gomock.NewController(t)
		ctx := context.Background()
		dir := t.TempDir()

		stateRepo := mock_extension_state_repo.NewMockExtensionStateRepo(ctrl)
		dataRepo := mock_extension_data_repo.NewMockExtensionDataRepo(ctrl)
		logger := zap.NewNop()

		var reloadCalled int
		svc := New(
			newTestManager(dir),
			stateRepo, dataRepo, nil, logger,
			func() { reloadCalled++ },
			nil, // snippetHook=nil: existing tests don't exercise snippet integration
		)
		// 注册表是进程级的：每个 Convey 叶子跑完都得把这一轮登记的扩展撤掉，
		// 否则下一个叶子会撞上"同名资产类型已注册"。
		Reset(func() {
			for _, name := range svc.Bridge().ListNames() {
				extreg.Unregister(name)
			}
		})

		Convey("Init with no extensions", func() {
			stateRepo.EXPECT().FindAll(gomock.Any()).Return(nil, nil)

			err := svc.Init(ctx)
			So(err, ShouldBeNil)
			So(registeredAssetTypes(svc), ShouldBeEmpty)
		})

		Convey("Init loads extension and applies DB disabled state", func() {
			writeTestExtension(dir, "ext-a")

			stateRepo.EXPECT().FindAll(gomock.Any()).Return([]*extension_state_entity.ExtensionState{
				{Name: "ext-a", Enabled: false},
			}, nil)

			err := svc.Init(ctx)
			So(err, ShouldBeNil)

			// ext-a should be unloaded because DB says disabled
			So(registeredAssetTypes(svc), ShouldBeEmpty)
			So(svc.Manager().GetExtension("ext-a"), ShouldBeNil)
		})

		Convey("Init loads enabled extension", func() {
			writeTestExtension(dir, "ext-b")

			stateRepo.EXPECT().FindAll(gomock.Any()).Return(nil, nil)

			err := svc.Init(ctx)
			So(err, ShouldBeNil)

			So(registeredAssetTypes(svc), ShouldResemble, []string{"ext-b"})
		})

		Convey("Reload closes and reinitializes", func() {
			writeTestExtension(dir, "ext-c")
			stateRepo.EXPECT().FindAll(gomock.Any()).Return(nil, nil).Times(2)

			err := svc.Init(ctx)
			So(err, ShouldBeNil)
			So(len(registeredAssetTypes(svc)), ShouldEqual, 1)

			reloadCalled = 0

			err = svc.Reload(ctx)
			So(err, ShouldBeNil)
			So(len(registeredAssetTypes(svc)), ShouldEqual, 1)
			So(reloadCalled, ShouldEqual, 1)
		})

		Convey("Disable unregisters and unloads", func() {
			writeTestExtension(dir, "ext-d")
			stateRepo.EXPECT().FindAll(gomock.Any()).Return(nil, nil)

			err := svc.Init(ctx)
			So(err, ShouldBeNil)
			So(len(registeredAssetTypes(svc)), ShouldEqual, 1)

			stateRepo.EXPECT().Find(gomock.Any(), "ext-d").Return(nil, fmt.Errorf("not found"))
			stateRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

			err = svc.Disable(ctx, "ext-d")
			So(err, ShouldBeNil)
			So(registeredAssetTypes(svc), ShouldBeEmpty)
			So(svc.Manager().GetExtension("ext-d"), ShouldBeNil)
		})

		Convey("Enable loads and registers", func() {
			writeTestExtension(dir, "ext-e")
			stateRepo.EXPECT().FindAll(gomock.Any()).Return([]*extension_state_entity.ExtensionState{
				{Name: "ext-e", Enabled: false},
			}, nil)

			err := svc.Init(ctx)
			So(err, ShouldBeNil)
			So(registeredAssetTypes(svc), ShouldBeEmpty)

			stateRepo.EXPECT().Find(gomock.Any(), "ext-e").Return(nil, fmt.Errorf("not found"))
			stateRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

			err = svc.Enable(ctx, "ext-e")
			So(err, ShouldBeNil)
			So(len(registeredAssetTypes(svc)), ShouldEqual, 1)
		})

		Convey("Uninstall removes extension", func() {
			writeTestExtension(dir, "ext-f")
			stateRepo.EXPECT().FindAll(gomock.Any()).Return(nil, nil)

			err := svc.Init(ctx)
			So(err, ShouldBeNil)

			stateRepo.EXPECT().Delete(gomock.Any(), "ext-f").Return(nil)
			dataRepo.EXPECT().DeleteAll(gomock.Any(), "ext-f").Return(nil)

			err = svc.Uninstall(ctx, "ext-f", true, false)
			So(err, ShouldBeNil)
			So(registeredAssetTypes(svc), ShouldBeEmpty)
		})

		Convey("Uninstall without cleanData skips data deletion", func() {
			writeTestExtension(dir, "ext-g")
			stateRepo.EXPECT().FindAll(gomock.Any()).Return(nil, nil)

			err := svc.Init(ctx)
			So(err, ShouldBeNil)

			stateRepo.EXPECT().Delete(gomock.Any(), "ext-g").Return(nil)
			// dataRepo.DeleteAll should NOT be called

			err = svc.Uninstall(ctx, "ext-g", false, false)
			So(err, ShouldBeNil)
		})

		Convey("ListInstalled returns enabled and disabled", func() {
			writeTestExtension(dir, "ext-h")
			stateRepo.EXPECT().FindAll(gomock.Any()).Return([]*extension_state_entity.ExtensionState{
				{Name: "ext-h", Enabled: false},
			}, nil)

			err := svc.Init(ctx)
			So(err, ShouldBeNil)

			infos := svc.ListInstalled("en")
			So(len(infos), ShouldEqual, 1)
			So(infos[0].Name, ShouldEqual, "ext-h")
			So(infos[0].Enabled, ShouldBeFalse)
		})

		Reset(func() {
			svc.Close(ctx)
		})
	})
}

func TestService_SnippetIntegration(t *testing.T) {
	Convey("Service (snippet integration)", t, func() {
		ctrl := gomock.NewController(t)
		ctx := context.Background()

		stateRepo := mock_extension_state_repo.NewMockExtensionStateRepo(ctrl)
		dataRepo := mock_extension_data_repo.NewMockExtensionDataRepo(ctrl)
		logger := zap.NewNop()

		Convey("Install with valid snippets runs RefreshCategories + SyncExtensionSeeds", func() {
			hook := &fakeSnippetHook{}

			// Manager points to a persistent target dir; source is separate.
			targetDir := t.TempDir()
			sourceDir := t.TempDir()
			writeTestExtensionWithSnippets(sourceDir, "kafka-ext", "kafka-ext", "list-topics")

			mgr := newTestManager(targetDir)
			svc := New(mgr, stateRepo, dataRepo, nil, logger,
				func() {}, hook)
			defer closeService(ctx, svc)

			stateRepo.EXPECT().Find(gomock.Any(), "kafka-ext").Return(nil, fmt.Errorf("not found"))
			stateRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

			m, err := svc.Install(ctx, filepath.Join(sourceDir, "kafka-ext"))
			So(err, ShouldBeNil)
			So(m.Name, ShouldEqual, "kafka-ext")
			So(len(hook.syncCalls), ShouldEqual, 1)
			So(hook.syncCalls[0].ExtName, ShouldEqual, "kafka-ext")
			So(len(hook.syncCalls[0].Seeds), ShouldEqual, 1)
			So(hook.syncCalls[0].Seeds[0].Key, ShouldEqual, "list-topics")
			So(hook.refreshCount, ShouldBeGreaterThanOrEqualTo, 1)
			So(len(hook.removeCalls), ShouldEqual, 0)
		})

		Convey("Install with conflicting category id rolls back", func() {
			hook := &fakeSnippetHook{}

			targetDir := t.TempDir()
			// Pre-install an extension that already owns category "kafka".
			writeTestExtensionWithSnippets(targetDir, "kafka-a", "kafka-a", "k1")

			mgr := newTestManager(targetDir)
			svc := New(mgr, stateRepo, dataRepo, nil, logger,
				func() {}, hook)
			defer closeService(ctx, svc)

			stateRepo.EXPECT().FindAll(gomock.Any()).Return(nil, nil)
			So(svc.Init(ctx), ShouldBeNil)
			// kafka-a should be loaded.
			So(mgr.GetExtension("kafka-a"), ShouldNotBeNil)

			// Now try to install a second extension that re-declares the same id.
			sourceDir := t.TempDir()
			writeTestExtensionWithSnippets(sourceDir, "kafka-b", "kafka-b", "k2")

			_, err := svc.Install(ctx, filepath.Join(sourceDir, "kafka-b"))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "already registered")

			// kafka-b should NOT be loaded (rolled back by manager.Uninstall).
			So(mgr.GetExtension("kafka-b"), ShouldBeNil)
			// kafka-a is still there.
			So(mgr.GetExtension("kafka-a"), ShouldNotBeNil)
			// No seeds were synced for kafka-b.
			for _, c := range hook.syncCalls {
				So(c.ExtName, ShouldNotEqual, "kafka-b")
			}
		})

		Convey("Uninstall invokes RemoveExtensionSeeds + RefreshCategories", func() {
			hook := &fakeSnippetHook{}

			targetDir := t.TempDir()
			writeTestExtensionWithSnippets(targetDir, "kafka-x", "kafka-x", "k1")

			mgr := newTestManager(targetDir)
			svc := New(mgr, stateRepo, dataRepo, nil, logger,
				func() {}, hook)
			defer closeService(ctx, svc)

			stateRepo.EXPECT().FindAll(gomock.Any()).Return(nil, nil)
			So(svc.Init(ctx), ShouldBeNil)

			// Reset so we can assert uninstall-side calls cleanly.
			hook.mu.Lock()
			hook.refreshCount = 0
			hook.mu.Unlock()

			stateRepo.EXPECT().Delete(gomock.Any(), "kafka-x").Return(nil)

			err := svc.Uninstall(ctx, "kafka-x", false, true)
			So(err, ShouldBeNil)
			So(hook.removeCalls, ShouldResemble, []string{"kafka-x"})
			So(hook.refreshCount, ShouldBeGreaterThanOrEqualTo, 1)
		})

		Convey("Install with nil snippetHook is a no-op", func() {
			targetDir := t.TempDir()
			sourceDir := t.TempDir()
			writeTestExtension(sourceDir, "simple")

			mgr := newTestManager(targetDir)
			svc := New(mgr, stateRepo, dataRepo, nil, logger,
				func() {}, nil)
			defer closeService(ctx, svc)

			stateRepo.EXPECT().Find(gomock.Any(), "simple").Return(nil, fmt.Errorf("not found"))
			stateRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

			_, err := svc.Install(ctx, filepath.Join(sourceDir, "simple"))
			So(err, ShouldBeNil)
		})
	})
}
