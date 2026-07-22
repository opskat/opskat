package tool

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"context"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/pkg/dbutil"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/audit_repo"
	"github.com/opskat/opskat/internal/repository/group_repo"
)

// fakeAssetRepo is a minimal in-memory AssetRepo for the CRUD tests below.
//
// The create-then-update flow under test needs Create to hand back a real, freshly
// assigned ID and FindByName/Find/Update to observe it afterwards — a gomock
// expectation list would have to hardcode the ID a fresh Create happens to assign,
// which defeats the point of testing create-then-update as one flow.
type fakeAssetRepo struct {
	mu     sync.Mutex
	nextID int64
	byID   map[int64]*asset_entity.Asset

	// policyLookups, when set by setupCRUD, is incremented on every by-id Find — the
	// exact call permission.CheckPermission's internal asset_svc.Asset().Get makes to
	// load an asset's CommandPolicy. See crudTestEnv.policyChecks's doc comment for why
	// that specific call is a faithful "did anything consult policy/grant" signal.
	policyLookups *int
}

func newFakeAssetRepo() *fakeAssetRepo {
	return &fakeAssetRepo{byID: map[int64]*asset_entity.Asset{}}
}

func (r *fakeAssetRepo) Find(_ context.Context, id int64) (*asset_entity.Asset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.policyLookups != nil {
		*r.policyLookups++
	}
	a, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("asset %d not found", id)
	}
	return a, nil
}

func (r *fakeAssetRepo) FindByName(_ context.Context, name string) ([]*asset_entity.Asset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*asset_entity.Asset
	for _, a := range r.byID {
		if a.Name == name {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r *fakeAssetRepo) List(_ context.Context, opts asset_repo.ListOptions) ([]*asset_entity.Asset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*asset_entity.Asset
	for _, a := range r.byID {
		if opts.Type != "" && a.Type != opts.Type {
			continue
		}
		// Mirrors the real asset_repo.List's group filtering (see asset_repo/asset.go):
		// ExactGroupID matches the group id verbatim (including 0, for "ungrouped");
		// otherwise a positive GroupID still filters, 0 means "don't filter by group".
		if opts.ExactGroupID && a.GroupID != opts.GroupID {
			continue
		}
		if !opts.ExactGroupID && opts.GroupID > 0 && a.GroupID != opts.GroupID {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

func (r *fakeAssetRepo) Create(_ context.Context, a *asset_entity.Asset) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	a.ID = r.nextID
	r.byID[a.ID] = a
	return nil
}

func (r *fakeAssetRepo) Update(_ context.Context, a *asset_entity.Asset) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[a.ID]; !ok {
		return fmt.Errorf("asset %d not found", a.ID)
	}
	r.byID[a.ID] = a
	return nil
}

func (r *fakeAssetRepo) Delete(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, id)
	return nil
}

// MoveToGroup mirrors asset_repo.assetRepo.MoveToGroup (internal/repository/asset_repo/asset.go):
// every asset currently in fromGroupID moves to toGroupID. A no-op stub let
// TestHandleDeleteGroup_DefaultsToMovingAssetsOut pass while only ever checking the
// asset still existed, never that it actually left the deleted group — a regression
// that forgot to move assets out would have gone undetected.
func (r *fakeAssetRepo) MoveToGroup(_ context.Context, fromGroupID, toGroupID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.byID {
		if a.GroupID == fromGroupID {
			a.GroupID = toGroupID
		}
	}
	return nil
}

func (r *fakeAssetRepo) DeleteByGroupID(_ context.Context, groupID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, a := range r.byID {
		if a.GroupID == groupID {
			delete(r.byID, id)
		}
	}
	return nil
}
func (r *fakeAssetRepo) FindByCredentialID(_ context.Context, _ int64) ([]*asset_entity.Asset, error) {
	return nil, nil
}
func (r *fakeAssetRepo) UpdateSortOrder(_ context.Context, _ int64, _ int) error { return nil }
func (r *fakeAssetRepo) UpdateGroupID(_ context.Context, _, _ int64) error       { return nil }
func (r *fakeAssetRepo) CountByTypes(_ context.Context, _ []string) (int64, error) {
	return 0, nil
}

// fakeGroupRepo mirrors fakeAssetRepo for groups, for the same reason.
type fakeGroupRepo struct {
	mu     sync.Mutex
	nextID int64
	byID   map[int64]*group_entity.Group
}

func newFakeGroupRepo() *fakeGroupRepo {
	return &fakeGroupRepo{byID: map[int64]*group_entity.Group{}}
}

func (r *fakeGroupRepo) Find(_ context.Context, id int64) (*group_entity.Group, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("group %d not found", id)
	}
	return g, nil
}

func (r *fakeGroupRepo) List(_ context.Context) ([]*group_entity.Group, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*group_entity.Group, 0, len(r.byID))
	for _, g := range r.byID {
		out = append(out, g)
	}
	return out, nil
}

func (r *fakeGroupRepo) Create(_ context.Context, g *group_entity.Group) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	g.ID = r.nextID
	r.byID[g.ID] = g
	return nil
}

func (r *fakeGroupRepo) Update(_ context.Context, g *group_entity.Group) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[g.ID]; !ok {
		return fmt.Errorf("group %d not found", g.ID)
	}
	r.byID[g.ID] = g
	return nil
}

func (r *fakeGroupRepo) Delete(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, id)
	return nil
}

func (r *fakeGroupRepo) UpdateName(_ context.Context, id int64, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.byID[id]; ok {
		g.Name = name
	}
	return nil
}

// ReparentChildren mirrors group_repo.groupRepo.ReparentChildren
// (internal/repository/group_repo/group.go): every child of oldParentID is reparented
// to newParentID. group_svc.Delete always calls this before removing a group so its
// children don't end up dangling — a no-op stub can't catch a regression there.
func (r *fakeGroupRepo) ReparentChildren(_ context.Context, oldParentID, newParentID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, g := range r.byID {
		if g.ParentID == oldParentID {
			g.ParentID = newParentID
		}
	}
	return nil
}

func (r *fakeGroupRepo) UpdateSortOrder(_ context.Context, id int64, sortOrder int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.byID[id]; ok {
		g.SortOrder = sortOrder
	}
	return nil
}

func (r *fakeGroupRepo) UpdateParentID(_ context.Context, id, parentID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.byID[id]; ok {
		g.ParentID = parentID
	}
	return nil
}

// crudTestEnv is the fixture shared by the TestHandlePut*/TestHandleDelete* tests
// below: an isolated, in-memory asset/group repo pair swapped in for the process-global
// singletons and restored on cleanup, plus a permission.CommandPolicyChecker wired into
// ctx so the delete tests can drive and observe the confirmation dialog.
type crudTestEnv struct {
	ctx    context.Context
	assets *fakeAssetRepo
	groups *fakeGroupRepo

	// confirmDecision drives the fake ConfirmFunc's response ("allow" / "deny"),
	// settable by the test *after* setupCRUD returns (the closure reads it live).
	// confirmCalls/lastConfirmKind/lastConfirmItems record what it was last called with.
	confirmDecision  string
	confirmCalls     int
	lastConfirmKind  string
	lastConfirmItems []permission.ApprovalItem

	// policyChecks mirrors fakeAssetRepo.policyLookups — see that field's doc comment.
	policyChecks int
}

func setupCRUD(t *testing.T) *crudTestEnv {
	t.Helper()

	assets := newFakeAssetRepo()
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(assets)
	t.Cleanup(func() { asset_repo.RegisterAsset(origAsset) })

	groups := newFakeGroupRepo()
	origGroup := group_repo.Group()
	group_repo.RegisterGroup(groups)
	t.Cleanup(func() { group_repo.RegisterGroup(origGroup) })

	env := &crudTestEnv{assets: assets, groups: groups}
	assets.policyLookups = &env.policyChecks

	// group_svc.Group().Delete wraps its writes in dbutil.WithTransaction; without a
	// runner injected that falls through to a real GORM DB, which these tests don't
	// have. A passthrough runner makes it behave like a no-op wrapper, same as
	// group_svc's own tests (see group_test.go's setupTest).
	ctx := dbutil.WithTransactionRunner(context.Background(),
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })

	checker := permission.NewCommandPolicyChecker(
		func(_ context.Context, kind string, items []permission.ApprovalItem) permission.ApprovalResponse {
			env.confirmCalls++
			env.lastConfirmKind = kind
			env.lastConfirmItems = items
			return permission.ApprovalResponse{Decision: env.confirmDecision}
		})
	env.ctx = permission.WithPolicyChecker(ctx, checker)

	return env
}

// seedAsset directly inserts an SSH asset into the fake repo, bypassing asset_svc's
// validation — the delete tests operate on a target that must already exist, not one
// created through the tool under test.
func (e *crudTestEnv) seedAsset(name string, groupID int64) *asset_entity.Asset {
	a := &asset_entity.Asset{Name: name, Type: asset_entity.AssetTypeSSH, GroupID: groupID}
	if err := e.assets.Create(e.ctx, a); err != nil {
		panic(err)
	}
	return a
}

// seedGroup directly inserts a group into the fake repo, for the same reason.
func (e *crudTestEnv) seedGroup(name string) *group_entity.Group {
	g := &group_entity.Group{Name: name}
	if err := e.groups.Create(e.ctx, g); err != nil {
		panic(err)
	}
	return g
}

// grantEverything seeds (or reuses) the asset named "web-9" that the delete tests
// target and stuffs an allow-* command policy onto it — "往策略里塞一条 allow *". Delete
// must ignore this entirely: it never consults CommandPolicy or grant, so even the most
// permissive policy surface must not change whether the user gets prompted.
func (e *crudTestEnv) grantEverything() *asset_entity.Asset {
	a := e.asset("web-9")
	if a == nil {
		a = e.seedAsset("web-9", 0)
	}
	if err := a.SetCommandPolicy(&asset_entity.CommandPolicy{AllowList: []string{"*"}}); err != nil {
		panic(err)
	}
	return a
}

func (e *crudTestEnv) assetCount() int {
	e.assets.mu.Lock()
	defer e.assets.mu.Unlock()
	return len(e.assets.byID)
}

func (e *crudTestEnv) asset(name string) *asset_entity.Asset {
	e.assets.mu.Lock()
	defer e.assets.mu.Unlock()
	for _, a := range e.assets.byID {
		if a.Name == name {
			return a
		}
	}
	return nil
}

func (e *crudTestEnv) groupCount() int {
	e.groups.mu.Lock()
	defer e.groups.mu.Unlock()
	return len(e.groups.byID)
}

func (e *crudTestEnv) group(name string) *group_entity.Group {
	e.groups.mu.Lock()
	defer e.groups.mu.Unlock()
	for _, g := range e.groups.byID {
		if g.Name == name {
			return g
		}
	}
	return nil
}

// validOSSConfig builds the minimal config internal/assettype/oss.go's ValidateCreateArgs
// requires: endpoint + access_key_id. secret_access_key is deliberately omitted — supplying
// one would exercise credential_svc.Default().Encrypt, which needs a master key wired up
// that a package-local handler test has no reason to set up.
func (e *crudTestEnv) validOSSConfig() map[string]any {
	return map[string]any{
		"provider":      "s3",
		"endpoint":      "s3.us-east-1.amazonaws.com",
		"region":        "us-east-1",
		"access_key_id": "AKIAEXAMPLE",
	}
}

// 有 asset → 更新；无 asset → 创建。同一个工具，分支只由标识的有无决定。
func TestHandlePutAsset_CreateThenUpdate(t *testing.T) {
	env := setupCRUD(t)

	out, err := handlePutAsset(env.ctx, map[string]any{
		"name": "web-9", "type": "ssh",
		"config": map[string]any{"host": "10.0.0.9", "port": float64(22), "username": "root"},
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if env.assetCount() != 1 {
		t.Fatalf("expected exactly 1 asset after create, got %d", env.assetCount())
	}

	if _, err := handlePutAsset(env.ctx, map[string]any{
		"asset":  "web-9",
		"config": map[string]any{"username": "deploy"},
	}); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if env.assetCount() != 1 {
		t.Errorf("put with an identifier must update in place, not create a second row (got %d)", env.assetCount())
	}
	sshCfg, err := env.asset("web-9").GetSSHConfig()
	if err != nil {
		t.Fatalf("GetSSHConfig: %v", err)
	}
	if got := sshCfg.Username; got != "deploy" {
		t.Errorf("username = %q, want %q", got, "deploy")
	}
	_ = out
}

// config 是自由对象，校验回到 assettype.ValidateCreateArgs——不是回到工具 schema。
func TestHandlePutAsset_ValidationComesFromAssetType(t *testing.T) {
	env := setupCRUD(t)

	_, err := handlePutAsset(env.ctx, map[string]any{
		"name": "broken", "type": "database",
		"config": map[string]any{"host": "10.0.0.1"}, // 缺 port/username/driver
	})
	if err == nil {
		t.Fatal("missing required config fields must fail")
	}
	if !strings.Contains(err.Error(), "driver") && !strings.Contains(err.Error(), "username") {
		t.Errorf("error %q should come from the asset type's own validation", err.Error())
	}
	if env.assetCount() != 0 {
		t.Errorf("a failed validation must not create anything, got %d assets", env.assetCount())
	}
}

// oss 类型此前被巨型 schema 完全遗漏（40 个属性里一个 oss 字段都没有），
// 自由 config 之后它必须可创建。
func TestHandlePutAsset_SupportsTypesTheOldSchemaOmitted(t *testing.T) {
	env := setupCRUD(t)

	if _, err := handlePutAsset(env.ctx, map[string]any{
		"name": "backup-bucket", "type": "oss",
		"config": env.validOSSConfig(), // 按 internal/assettype/oss.go 的必填字段构造
	}); err != nil {
		t.Fatalf("oss asset must be creatable via put_asset, got %v", err)
	}
}

// 未知类型必须报错并列出可用类型，而不是静默创建一个跑不起来的资产。
func TestHandlePutAsset_UnknownTypeIsNamed(t *testing.T) {
	env := setupCRUD(t)

	_, err := handlePutAsset(env.ctx, map[string]any{"name": "x", "type": "sqlite"})
	if err == nil || !strings.Contains(err.Error(), "sqlite") {
		t.Fatalf("unknown type must be named in the error, got %v", err)
	}
}

func TestHandlePutGroup_CreateThenUpdate(t *testing.T) {
	env := setupCRUD(t)

	if _, err := handlePutGroup(env.ctx, map[string]any{"name": "prod"}); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if _, err := handlePutGroup(env.ctx, map[string]any{
		"id": float64(env.group("prod").ID), "description": "production fleet",
	}); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if env.groupCount() != 1 {
		t.Errorf("put with an id must update in place, got %d groups", env.groupCount())
	}
	if got := env.group("prod").Description; got != "production fleet" {
		t.Errorf("description = %q, want %q", got, "production fleet")
	}
}

// 删除恒需确认：即使策略里有 allow * 的 grant，也必须弹确认。
// 实现方式决定了这一点——delete 根本不查策略/grant，直接调 ConfirmFunc。
func TestHandleDeleteAsset_AlwaysConfirmsAndIsNotGrantable(t *testing.T) {
	env := setupCRUD(t)
	env.grantEverything() // 往策略里塞一条 allow * ——对 delete 必须无效

	env.confirmDecision = "allow"
	slot := &aictx.CheckResult{}
	ctx := aictx.WithCheckResultSlot(env.ctx, slot)
	if _, err := handleDeleteAsset(ctx, map[string]any{"asset": "web-9"}); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if env.confirmCalls != 1 {
		t.Fatalf("delete must always prompt exactly once, got %d prompts", env.confirmCalls)
	}
	if env.policyChecks != 0 {
		t.Errorf("delete must not consult policy/grant at all, got %d checks", env.policyChecks)
	}
	if env.assetCount() != 0 {
		t.Errorf("asset should be gone, %d left", env.assetCount())
	}
	// decisionFromApproval 把用户点头映射进审计的 decision 列；写反了会让一次真实
	// 允许的删除在 audit_logs 里记成 decision=deny，且没有任何测试会报错。
	if slot.Decision != aictx.Allow || slot.DecisionSource != aictx.SourceUserAllow {
		t.Errorf("recorded decision = %v/%q, want Allow/%q", slot.Decision, slot.DecisionSource, aictx.SourceUserAllow)
	}
}

// 用户拒绝 → 不删，且不报 Go error（模型据此自纠，而不是整轮中断）。
func TestHandleDeleteAsset_DenyKeepsTheAsset(t *testing.T) {
	env := setupCRUD(t)
	env.seedAsset("web-9", 0)
	env.confirmDecision = "deny"

	slot := &aictx.CheckResult{}
	ctx := aictx.WithCheckResultSlot(env.ctx, slot)
	out, err := handleDeleteAsset(ctx, map[string]any{"asset": "web-9"})
	if err != nil {
		t.Fatalf("a user denial is not a tool error: %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "denied") {
		t.Errorf("result must tell the model it was denied: %q", out)
	}
	// 仓内拒绝文案的惯例是明确制止后续动作（checker.go:270、SubmitGrant、
	// local_tool_gate.go:93 都是这个措辞），不是软弱的小写 "user denied ..."——模型
	// 读到软弱的措辞可能换个引用重试。
	if !strings.Contains(out, "USER DENIED") || !strings.Contains(strings.ToLower(out), "stop the current task") {
		t.Errorf("result must use the repo's stop-now denial phrasing, got %q", out)
	}
	if env.assetCount() != 1 {
		t.Error("denied delete must keep the asset")
	}
	// 一次被拒绝的删除若在 decisionFromApproval 里被记成 Allow，audit_logs 会把它写成
	// decision=allow/user_allow——审计上看起来像被批准了，且没有任何东西会因此报错。
	if slot.Decision != aictx.Deny || slot.DecisionSource != aictx.SourceUserDeny {
		t.Errorf("recorded decision = %v/%q, want Deny/%q", slot.Decision, slot.DecisionSource, aictx.SourceUserDeny)
	}
}

// 审批项必须带上资产名与删除语义，审批弹窗上"删哪台"要一眼可见。
func TestHandleDeleteAsset_ApprovalItemNamesTheAsset(t *testing.T) {
	env := setupCRUD(t)
	env.seedAsset("web-9", 0)
	env.confirmDecision = "allow"

	_, _ = handleDeleteAsset(env.ctx, map[string]any{"asset": "web-9"})

	if env.lastConfirmKind != "delete" {
		t.Errorf("approval kind = %q, want %q (the frontend renders this one without an allow-all button)",
			env.lastConfirmKind, "delete")
	}
	item := env.lastConfirmItems[0]
	if item.AssetName != "web-9" {
		t.Errorf("approval item asset name = %q, want web-9", item.AssetName)
	}
	if !strings.Contains(item.Command, "delete") {
		t.Errorf("approval item must read as a delete, got %q", item.Command)
	}
}

// delete_group 默认非破坏性分支：资产移入未分组，不删。
func TestHandleDeleteGroup_DefaultsToMovingAssetsOut(t *testing.T) {
	env := setupCRUD(t)
	g := env.seedGroup("prod")
	env.seedAsset("worker-1", g.ID)
	env.confirmDecision = "allow"

	if _, err := handleDeleteGroup(env.ctx, map[string]any{"id": float64(env.group("prod").ID)}); err != nil {
		t.Fatalf("delete_group failed: %v", err)
	}
	if env.assetCount() == 0 {
		t.Error("delete_assets defaults to false — the group's assets must survive")
	}
	if env.groupCount() != 0 {
		t.Error("the group itself should be gone")
	}
	// 光"还在"不够——它必须真的移出了被删的分组，否则就是一条指向已经不存在的
	// group_id 的悬空引用。fakeAssetRepo.MoveToGroup 从前是空实现，这条断言测不出来。
	if got := env.asset("worker-1").GroupID; got != 0 {
		t.Errorf("survivor asset must move to ungrouped (group_id=0), got group_id=%d", got)
	}
}

// delete_group 删除一个有子分组的分组时，子分组要挂到被删分组的父级，而不是变成
// 悬空引用——group_svc.Delete 在事务里调 group_repo.ReparentChildren 做这件事。
// fakeGroupRepo.ReparentChildren 从前是空实现，这个行为在单元测试里从未被真正验证过。
func TestHandleDeleteGroup_ReparentsChildGroups(t *testing.T) {
	env := setupCRUD(t)
	root := env.seedGroup("root")
	mid := env.seedGroup("prod")
	mid.ParentID = root.ID
	child := env.seedGroup("prod-child")
	child.ParentID = mid.ID
	env.confirmDecision = "allow"

	if _, err := handleDeleteGroup(env.ctx, map[string]any{"id": float64(mid.ID)}); err != nil {
		t.Fatalf("delete_group failed: %v", err)
	}
	if got := env.group("prod-child").ParentID; got != root.ID {
		t.Errorf("child group must be reparented to the deleted group's parent, got parent_id=%d want %d", got, root.ID)
	}
}

// delete_group 拒绝 → 组和组内资产都不动。
func TestHandleDeleteGroup_DenyKeepsGroupAndAssets(t *testing.T) {
	env := setupCRUD(t)
	g := env.seedGroup("prod")
	env.seedAsset("worker-1", g.ID)
	env.confirmDecision = "deny"

	slot := &aictx.CheckResult{}
	ctx := aictx.WithCheckResultSlot(env.ctx, slot)
	out, err := handleDeleteGroup(ctx, map[string]any{"id": float64(g.ID)})
	if err != nil {
		t.Fatalf("a user denial is not a tool error: %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "denied") {
		t.Errorf("result must tell the model it was denied: %q", out)
	}
	// 与 TestHandleDeleteAsset_DenyKeepsTheAsset 同一条惯例断言。
	if !strings.Contains(out, "USER DENIED") || !strings.Contains(strings.ToLower(out), "stop the current task") {
		t.Errorf("result must use the repo's stop-now denial phrasing, got %q", out)
	}
	if env.groupCount() != 1 {
		t.Error("denied delete must keep the group")
	}
	if env.assetCount() != 1 {
		t.Error("denied delete must keep the group's assets")
	}
	if slot.Decision != aictx.Deny || slot.DecisionSource != aictx.SourceUserDeny {
		t.Errorf("recorded decision = %v/%q, want Deny/%q", slot.Decision, slot.DecisionSource, aictx.SourceUserDeny)
	}
}

// fakeAuditRepo captures WriteToolCall's rows in memory, mirroring the audit package's
// own memAuditRepo test helper (internal/ai/audit/audit_asset_ref_test.go) — that one
// isn't exported, and package tool can't import a _test.go file from another package.
type fakeAuditRepo struct {
	mu   sync.Mutex
	logs []*audit_entity.AuditLog
}

func (r *fakeAuditRepo) Create(_ context.Context, log *audit_entity.AuditLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, log)
	return nil
}

func (r *fakeAuditRepo) List(_ context.Context, _ audit_repo.ListOptions) ([]*audit_entity.AuditLog, int64, error) {
	return nil, 0, nil
}

func (r *fakeAuditRepo) ListSessions(_ context.Context, _ int64) ([]audit_repo.SessionInfo, error) {
	return nil, nil
}

func setupFakeAuditRepo(t *testing.T) *fakeAuditRepo {
	t.Helper()
	repo := &fakeAuditRepo{}
	orig := audit_repo.Audit()
	audit_repo.RegisterAudit(repo)
	t.Cleanup(func() { audit_repo.RegisterAudit(orig) })
	return repo
}

// deleteAssets=true 连带删除组内资产，且逐条补 delete_asset 审计——删一个含多台机器的
// 分组不能只留一行审计（internal/app/system/asset.go 的 System.DeleteGroup 同一份写法，
// 这里是它在 AI 路径上的对照）。
func TestHandleDeleteGroup_CascadeDeletesAssetsAndAuditsEachOne(t *testing.T) {
	env := setupCRUD(t)
	repo := setupFakeAuditRepo(t)
	g := env.seedGroup("prod")
	env.seedAsset("worker-1", g.ID)
	env.seedAsset("worker-2", g.ID)
	env.confirmDecision = "allow"

	out, err := handleDeleteGroup(env.ctx, map[string]any{"id": float64(g.ID), "delete_assets": true})
	if err != nil {
		t.Fatalf("delete_group failed: %v", err)
	}
	if !strings.Contains(out, `"deleted_assets":2`) {
		t.Errorf("result must report how many assets were cascaded, got %q", out)
	}
	if env.assetCount() != 0 {
		t.Errorf("deleteAssets=true must remove the group's assets too, %d left", env.assetCount())
	}
	if env.groupCount() != 0 {
		t.Error("the group itself should be gone")
	}

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.logs) != 2 {
		t.Fatalf("expected 2 delete_asset audit rows (one per cascaded asset), got %d", len(repo.logs))
	}
	for _, l := range repo.logs {
		if l.ToolName != "delete_asset" {
			t.Errorf("cascade audit row tool_name = %q, want delete_asset", l.ToolName)
		}
	}
}
