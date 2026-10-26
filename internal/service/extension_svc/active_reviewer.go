package extension_svc

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/opskat/opskat/internal/service/command_review_svc"
	"github.com/opskat/opskat/pkg/extension"
)

// ActiveReviewer 是当前选中、且通过来源与摘要检查的审核器。
type ActiveReviewer struct {
	Name          string
	Version       string
	SettingsHash  string
	MaxItems      int
	MaxCommandLen int
	questions     []extension.ReviewQuestion
	Plugin        *extension.Plugin
}

// ActiveReviewer 就是审核服务要的 command_review_svc.ExtensionReviewer。
var _ command_review_svc.ExtensionReviewer = (*ActiveReviewer)(nil)

// Identity 是扩展名、版本和设置哈希；设置哈希参与审核缓存键。
func (r *ActiveReviewer) Identity() (string, string, string) {
	return r.Name, r.Version, r.SettingsHash
}

// Limits 是 describe() 声明的单批条数和单条命令最大长度。
func (r *ActiveReviewer) Limits() (int, int) { return r.MaxItems, r.MaxCommandLen }

// Questions 是 describe() 声明的题目。
func (r *ActiveReviewer) Questions() []extension.ReviewQuestion { return r.questions }

// Review 调用扩展的 review_commands；超时由调用方的 ctx 控制。
func (r *ActiveReviewer) Review(ctx context.Context, items []extension.ReviewItem) ([]extension.ReviewCommandResult, error) {
	return r.Plugin.ReviewCommands(ctx, items, 0)
}

// ActiveCommandReviewer 返回名为 name 的已加载审核器。
// 不是审核器、未加载、或来源/摘要不合格时返回错误。name 为空表示没有选中。
func (s *Service) ActiveCommandReviewer(ctx context.Context, name string) (*ActiveReviewer, error) {
	if name == "" {
		return nil, fmt.Errorf("no command reviewer selected")
	}
	ext := s.manager.GetExtension(name)
	if ext == nil && s.reviewerLoader != nil {
		loaded, err := s.loadReviewer(ctx, name)
		if err != nil {
			return nil, err
		}
		ext = loaded
	}
	if ext == nil || ext.Plugin == nil || !ext.Manifest.IsCommandReviewer() {
		return nil, fmt.Errorf("extension %q is not a loaded command reviewer", name)
	}
	if err := s.CheckReviewerProvenance(ctx, name, ext.PackageSHA256); err != nil {
		return nil, err
	}
	hash, err := s.SettingsHash(ctx, name)
	if err != nil {
		return nil, err
	}
	rev := ext.Manifest.Reviewer
	return &ActiveReviewer{
		Name:          ext.Name,
		Version:       ext.Manifest.Version,
		SettingsHash:  hash,
		MaxItems:      rev.MaxItems,
		MaxCommandLen: rev.MaxCommandLen,
		questions:     rev.Questions,
		Plugin:        ext.Plugin,
	}, nil
}

// LoadReviewersOnDemand 让 ActiveCommandReviewer 在选中的审核器还没加载时自己加载它。
// 桌面端启动时已加载全部启用的扩展，不用这个；opsctl 平时只按缓存的 describe() 注册扩展，
// 只在真正要审核时才编译、实例化选中的那一个。
func (s *Service) LoadReviewersOnDemand() {
	s.reviewerLoader = func(ctx context.Context, dir string) error {
		_, err := s.manager.LoadExtension(ctx, dir)
		return err
	}
}

// loadReviewer 先确认扩展已启用、包摘要和来源合格，再运行它的代码。
func (s *Service) loadReviewer(ctx context.Context, name string) (*extension.Extension, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ext := s.manager.GetExtension(name); ext != nil {
		return ext, nil
	}
	state, err := s.stateRepo.Find(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("command reviewer %q has no install record: %w", name, err)
	}
	if !state.Enabled {
		return nil, fmt.Errorf("command reviewer %q is disabled", name)
	}
	dir := s.manager.ExtDir(name)
	info, err := extension.LoadManifestInfo(dir)
	if err != nil {
		return nil, fmt.Errorf("read command reviewer %q: %w", name, err)
	}
	sum, err := extension.PackageSHA256(dir, info.Manifest.Backend.Binary)
	if err != nil {
		return nil, fmt.Errorf("read command reviewer %q: %w", name, err)
	}
	if err := s.CheckReviewerProvenance(ctx, name, sum); err != nil {
		return nil, err
	}
	if err := s.reviewerLoader(ctx, dir); err != nil {
		return nil, fmt.Errorf("load command reviewer %q: %w", name, err)
	}
	return s.manager.GetExtension(name), nil
}

// CommandReviewerInfo 是设置页列出的一个审核扩展。
type CommandReviewerInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Version     string `json:"version"`
	// Usable 表示现在能当审核器；不能时 Reason 说明原因（已停用、包摘要不符、正式版只认商店安装等）。
	Usable bool   `json:"usable"`
	Reason string `json:"reason,omitempty"`
	// Signed 表示从官方商店安装、签名已校验；非正式版上本地安装的审核器可用但不是签名的。
	Signed      bool `json:"signed"` // HasSettings 表示扩展声明了设置项，设置页据此显示它的设置表单。
	HasSettings bool `json:"hasSettings"`
}

// ListCommandReviewers 列出已加载的审核扩展及其能否使用，供设置页选择。
func (s *Service) ListCommandReviewers(ctx context.Context, lang string) []CommandReviewerInfo {
	// 没有审核扩展时也是空数组：它会经 IPC 交给前端，nil 切片编码成 null，前端遍历时就报错。
	out := []CommandReviewerInfo{}
	for _, ext := range s.manager.ListExtensions() {
		if !ext.Manifest.IsCommandReviewer() {
			continue
		}
		localized := ext.Manifest.Localized(func(key string) string { return ext.Translate(lang, key) })
		info := CommandReviewerInfo{
			Name: ext.Name, DisplayName: localized.I18n.DisplayName, Version: ext.Manifest.Version,
			HasSettings: ext.Manifest.SettingsSchema != nil,
		}
		signed, err := s.reviewerProvenance(ctx, ext.Name, ext.PackageSHA256)
		if err != nil {
			info.Reason = err.Error()
		} else {
			info.Usable, info.Signed = true, signed
		}
		out = append(out, info)
	}
	return out
}

var (
	// ErrNoCommandReviewer 是没有启用中的审核扩展。
	ErrNoCommandReviewer = errors.New("no command review extension is installed and enabled")
	// ErrSeveralCommandReviewers 是启用了不止一个审核扩展，却没选用哪个。
	ErrSeveralCommandReviewers = errors.New("several command review extensions are enabled; select one in settings")
)

// CommandReviewerName 是这次要用的审核扩展的名字。selected 是设置里选的，它是启用中的
// 审核扩展就用它（能不能用由 ActiveCommandReviewer 检查）；没选、或选的已停用或卸载时，
// 启用中的审核扩展只有一个就用它。停用、卸载都不会清掉设置里的选择，所以不能照它用。
// 判断"是不是审核扩展"只读 manifest.json 的 capabilities.commandReview，不用加载扩展，
// 所以 opsctl 也能这样选。
func (s *Service) CommandReviewerName(ctx context.Context, selected string) (string, error) {
	infos, err := s.manager.ScanManifests()
	if err != nil {
		return "", err
	}
	states, err := s.stateRepo.FindAll(ctx)
	if err != nil {
		return "", fmt.Errorf("read extension states: %w", err)
	}
	disabled := make(map[string]bool, len(states))
	for _, st := range states {
		disabled[st.Name] = !st.Enabled
	}
	var names []string
	for _, info := range infos {
		if info.Manifest.Capabilities.CommandReview && !disabled[info.Name] {
			if info.Name == selected {
				return selected, nil
			}
			names = append(names, info.Name)
		}
	}
	switch len(names) {
	case 0:
		return "", ErrNoCommandReviewer
	case 1:
		return names[0], nil
	default:
		sort.Strings(names)
		return "", fmt.Errorf("%w (%s)", ErrSeveralCommandReviewers, strings.Join(names, ", "))
	}
}

// CommandReviewState 是设置页和权限模式卡片看到的审核状态。
type CommandReviewState struct {
	Reviewers []CommandReviewerInfo `json:"reviewers"`
	// Selected 是设置里选的、仍启用中的审核扩展；空表示没选或选的已停用、卸载
	// （启用中的只有一个时自动用它）。
	Selected string `json:"selected"`
	// Active 是实际在用的审核扩展；没有可用的时为空，Problem 说明原因
	// （与审核失败原因同名：no_reviewer / reviewer_unavailable）。
	Active  string `json:"active"`
	Problem string `json:"problem,omitempty"`
}

// CommandReviewState 按审核时同一套规则算出要用的审核扩展，并检查它现在能不能用。
func (s *Service) CommandReviewState(ctx context.Context, lang, selected string) CommandReviewState {
	st := CommandReviewState{Reviewers: s.ListCommandReviewers(ctx, lang)}
	name, err := s.CommandReviewerName(ctx, selected)
	if err == nil && name == selected {
		st.Selected = selected
	}
	switch {
	case errors.Is(err, ErrNoCommandReviewer) || errors.Is(err, ErrSeveralCommandReviewers):
		st.Problem = command_review_svc.ReasonNoReviewer
		return st
	case err != nil:
		st.Problem = command_review_svc.ReasonReviewerUnavailable
		return st
	}
	for _, r := range st.Reviewers {
		if r.Name == name && r.Usable {
			st.Active = name
			return st
		}
	}
	st.Problem = command_review_svc.ReasonReviewerUnavailable
	return st
}
