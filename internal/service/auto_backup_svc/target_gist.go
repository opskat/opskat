package auto_backup_svc

import (
	"context"
	"errors"
	"fmt"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/service/backup_svc"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

// gistTarget 把自动备份写进一个专用私密 Gist，每次覆盖；旧版本靠 GitHub 修订历史。
type gistTarget struct {
	token  func() (string, error)
	upsert func(token, gistID string, content []byte) (*backup_svc.GistInfo, error)
}

func init() {
	RegisterTarget(gistTarget{
		token: func() (string, error) {
			token, _, err := backup_svc.StoredGitHubToken(credential_svc.Default())
			return token, err
		},
		upsert: backup_svc.CreateOrUpdateAutoGist,
	})
}

func (gistTarget) ID() string { return "gist" }

func (gistTarget) Validate(Settings) error { return nil }

func (gistTarget) Available(Settings) bool {
	cfg := bootstrap.GetConfig()
	return cfg != nil && cfg.GitHubToken != ""
}

// Destination 是登录的 GitHub 账号：换了账号，上一个账号的 Gist 不再沿用。
func (gistTarget) Destination(Settings) string { return bootstrap.GetConfig().GitHubUser }

func (g gistTarget) Write(_ context.Context, req WriteRequest) (string, error) {
	token, err := g.token()
	if err != nil {
		return req.Ref, err
	}
	if token == "" {
		return req.Ref, fmt.Errorf("尚未登录 GitHub")
	}
	info, err := g.upsert(token, req.Ref, req.Content)
	if errors.Is(err, backup_svc.ErrGistNotFound) {
		info, err = g.upsert(token, "", req.Content)
	}
	if err != nil {
		return req.Ref, err
	}
	return info.ID, nil
}
