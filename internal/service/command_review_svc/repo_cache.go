package command_review_svc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/opskat/opskat/internal/model/entity/command_review_entity"
	"github.com/opskat/opskat/internal/repository/command_review_repo"
)

// repoCache 把审核结果存进数据库。opsctl 每次运行都是新进程，内存缓存留不住；
// 桌面端和 opsctl 共用同一个数据库，审核过的命令可以互相命中。
type repoCache struct {
	repo command_review_repo.CommandReviewRepo
	now  func() time.Time
}

// NewRepoCache 创建基于数据库的缓存。
func NewRepoCache(repo command_review_repo.CommandReviewRepo) Cache {
	return &repoCache{repo: repo, now: time.Now}
}

func (c *repoCache) Get(ctx context.Context, key string) (*Result, error) {
	e, err := c.repo.Get(ctx, key, c.now().Unix())
	if err != nil || e == nil {
		return nil, err
	}
	var r Result
	if err := json.Unmarshal([]byte(e.Result), &r); err != nil {
		return nil, fmt.Errorf("decode cached review: %w", err)
	}
	return &r, nil
}

func (c *repoCache) Put(ctx context.Context, key string, r Result, ttl time.Duration) error {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode review: %w", err)
	}
	now := c.now()
	return c.repo.Put(ctx, &command_review_entity.CommandReview{
		CacheKey:   key,
		Result:     string(data),
		Createtime: now.Unix(),
		Expiretime: now.Add(ttl).Unix(),
	})
}
