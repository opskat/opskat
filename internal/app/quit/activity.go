package quit

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"go.uber.org/zap"
)

type Activity struct {
	Kind     string `json:"kind"`
	Category string `json:"category"`
	Title    string `json:"title"`
	Detail   string `json:"detail,omitempty"`
	RefID    int64  `json:"ref_id,omitempty"`
}

type Session struct {
	Kind      string
	SessionID string
	AssetID   int64
	StartedAt time.Time
}

type AssetFinder func(context.Context, int64) (*asset_entity.Asset, error)

func BuildSessionActivities(ctx context.Context, sessions []Session, find AssetFinder) []Activity {
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].StartedAt.Equal(sessions[j].StartedAt) {
			return sessions[i].SessionID < sessions[j].SessionID
		}
		return sessions[i].StartedAt.Before(sessions[j].StartedAt)
	})
	activities := make([]Activity, 0, len(sessions))
	for _, session := range sessions {
		activity := Activity{Kind: session.Kind, Category: "connection", Title: session.SessionID}
		asset, err := find(ctx, session.AssetID)
		if err != nil {
			logger.Ctx(ctx).Warn("load asset for quit activity", zap.Int64("assetID", session.AssetID), zap.Error(err))
		} else if asset != nil {
			if strings.TrimSpace(asset.Name) != "" {
				activity.Title = asset.Name
			}
			activity.Detail = assetDetail(asset)
		}
		activities = append(activities, activity)
	}
	return activities
}

func assetDetail(asset *asset_entity.Asset) string {
	var cfg struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
	}
	if json.Unmarshal([]byte(asset.Config), &cfg) != nil || cfg.Host == "" {
		return ""
	}
	host := cfg.Host
	if cfg.Port > 0 {
		host = fmt.Sprintf("%s:%d", host, cfg.Port)
	}
	if strings.TrimSpace(cfg.Username) != "" {
		return cfg.Username + "@" + host
	}
	return host
}
