package quit

import (
	"context"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func TestBuildSessionActivitiesEnrichesAndOrdersDetails(t *testing.T) {
	sessions := []Session{
		{Kind: "vnc", SessionID: "vnc-2", AssetID: 2, StartedAt: time.Unix(2, 0)},
		{Kind: "terminal", SessionID: "ssh-1", AssetID: 1, StartedAt: time.Unix(1, 0)},
	}
	assets := map[int64]*asset_entity.Asset{
		1: {ID: 1, Name: "mac mini", Type: "ssh", Config: `{"host":"10.0.0.1","port":22,"username":"root"}`},
		2: {ID: 2, Name: "jump host", Type: "vnc", Config: `{"host":"10.0.0.2","port":5900}`},
	}

	got := BuildSessionActivities(context.Background(), sessions, func(_ context.Context, id int64) (*asset_entity.Asset, error) {
		return assets[id], nil
	})

	if len(got) != 2 {
		t.Fatalf("got %d activities, want 2", len(got))
	}
	if got[0].Title != "mac mini" || got[0].Detail != "root@10.0.0.1:22" {
		t.Fatalf("unexpected SSH detail: %+v", got[0])
	}
	if got[1].Title != "jump host" || got[1].Detail != "10.0.0.2:5900" {
		t.Fatalf("unexpected VNC detail: %+v", got[1])
	}
}
