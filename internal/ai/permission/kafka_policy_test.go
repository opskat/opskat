package permission

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
	"github.com/opskat/opskat/internal/repository/grant_repo"
)

func TestMatchKafkaRule(t *testing.T) {
	tests := []struct {
		name    string
		rule    string
		command string
		want    bool
	}{
		{name: "exact action and resource", rule: "topic.read orders", command: "topic.read orders", want: true},
		{name: "action wildcard matches three level action", rule: "topic.* *", command: "topic.config.write orders", want: true},
		{name: "resource wildcard", rule: "message.read orders-*", command: "message.read orders-2026", want: true},
		{name: "single wildcard matches any canonical operation", rule: "*", command: "topic.read orders", want: true},
		{name: "single wildcard matches message write", rule: "*", command: "message.write orders", want: true},
		{name: "action is case insensitive", rule: "TOPIC.READ orders", command: "topic.read orders", want: true},
		{name: "resource is case sensitive", rule: "topic.read Orders", command: "topic.read orders", want: false},
		{name: "invalid rule shape", rule: "topic.read", command: "topic.read orders", want: false},
		{name: "invalid command shape", rule: "topic.read *", command: "topic.read orders extra", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, policy.MatchKafkaRule(tt.rule, tt.command))
		})
	}
}

func TestCheckKafkaPolicy(t *testing.T) {
	ctx := context.Background()
	p := &asset_entity.KafkaPolicy{
		AllowList: []string{"topic.* *", "message.read orders-*", "message.write *"},
		DenyList:  []string{"topic.delete *", "message.write prod-*"},
	}

	denied := policy.CheckKafkaPolicy(ctx, p, "topic.delete orders")
	assert.Equal(t, aictx.Deny, denied.Decision)
	assert.Equal(t, aictx.SourcePolicyDeny, denied.DecisionSource)
	assert.Equal(t, "topic.delete *", denied.MatchedPattern)

	allowed := policy.CheckKafkaPolicy(ctx, p, "topic.config.write orders")
	assert.Equal(t, aictx.Allow, allowed.Decision)
	assert.Equal(t, aictx.SourcePolicyAllow, allowed.DecisionSource)
	assert.Equal(t, "topic.* *", allowed.MatchedPattern)

	allowed = policy.CheckKafkaPolicy(ctx, p, "message.write orders")
	assert.Equal(t, aictx.Allow, allowed.Decision)
	assert.Equal(t, aictx.SourcePolicyAllow, allowed.DecisionSource)
	assert.Equal(t, "message.write *", allowed.MatchedPattern)

	denied = policy.CheckKafkaPolicy(ctx, p, "message.write prod-orders")
	assert.Equal(t, aictx.Deny, denied.Decision)
	assert.Equal(t, aictx.SourcePolicyDeny, denied.DecisionSource)
	assert.Equal(t, "message.write prod-*", denied.MatchedPattern)

	needConfirm := policy.CheckKafkaPolicy(ctx, p, "message.read invoices")
	assert.Equal(t, aictx.NeedConfirm, needConfirm.Decision)
}

func TestCheckKafkaPolicy_DefaultsAndWildcard(t *testing.T) {
	ctx := context.Background()

	metadataRead := policy.CheckKafkaPolicy(ctx, nil, "topic.read orders")
	assert.Equal(t, aictx.Allow, metadataRead.Decision)
	assert.Equal(t, aictx.SourcePolicyAllow, metadataRead.DecisionSource)

	messageWrite := policy.CheckKafkaPolicy(ctx, nil, "message.write orders")
	assert.Equal(t, aictx.NeedConfirm, messageWrite.Decision)

	defaultDangerous := policy.CheckKafkaPolicy(ctx, nil, "topic.delete orders")
	assert.Equal(t, aictx.Deny, defaultDangerous.Decision)
	assert.Equal(t, aictx.SourcePolicyDeny, defaultDangerous.DecisionSource)

	allowAll := policy.CheckKafkaPolicy(ctx, &asset_entity.KafkaPolicy{AllowList: []string{"*"}}, "message.write orders")
	assert.Equal(t, aictx.Allow, allowAll.Decision)
	assert.Equal(t, aictx.SourcePolicyAllow, allowAll.DecisionSource)
	assert.Equal(t, "*", allowAll.MatchedPattern)

	allowAllDangerous := policy.CheckKafkaPolicy(ctx, &asset_entity.KafkaPolicy{AllowList: []string{"*"}}, "topic.delete orders")
	assert.Equal(t, aictx.Deny, allowAllDangerous.Decision)
	assert.Equal(t, aictx.SourcePolicyDeny, allowAllDangerous.DecisionSource)

	denyAll := policy.CheckKafkaPolicy(ctx, &asset_entity.KafkaPolicy{DenyList: []string{"*"}}, "topic.read orders")
	assert.Equal(t, aictx.Deny, denyAll.Decision)
	assert.Equal(t, aictx.SourcePolicyDeny, denyAll.DecisionSource)
	assert.Equal(t, "*", denyAll.MatchedPattern)
}

func TestCheckKafkaPolicy_TopicAdminDenyWins(t *testing.T) {
	ctx := context.Background()
	p := &asset_entity.KafkaPolicy{
		AllowList: []string{
			"topic.create *",
			"topic.config.write *",
			"topic.partitions.write *",
			"topic.records.delete *",
			"consumer_group.offset.write *",
			"consumer_group.delete *",
		},
		DenyList: []string{"topic.delete *", "topic.records.delete *", "consumer_group.delete *"},
	}

	created := policy.CheckKafkaPolicy(ctx, p, "topic.create orders")
	assert.Equal(t, aictx.Allow, created.Decision)

	config := policy.CheckKafkaPolicy(ctx, p, "topic.config.write orders")
	assert.Equal(t, aictx.Allow, config.Decision)

	partitions := policy.CheckKafkaPolicy(ctx, p, "topic.partitions.write orders")
	assert.Equal(t, aictx.Allow, partitions.Decision)

	deleted := policy.CheckKafkaPolicy(ctx, p, "topic.delete orders")
	assert.Equal(t, aictx.Deny, deleted.Decision)

	records := policy.CheckKafkaPolicy(ctx, p, "topic.records.delete orders")
	assert.Equal(t, aictx.Deny, records.Decision)
	assert.Equal(t, "topic.records.delete *", records.MatchedPattern)

	reset := policy.CheckKafkaPolicy(ctx, p, "consumer_group.offset.write billing")
	assert.Equal(t, aictx.Deny, reset.Decision)
	assert.Equal(t, "consumer_group.offset.write *", reset.MatchedPattern)

	deleteGroup := policy.CheckKafkaPolicy(ctx, p, "consumer_group.delete billing")
	assert.Equal(t, aictx.Deny, deleteGroup.Decision)
	assert.Equal(t, "consumer_group.delete *", deleteGroup.MatchedPattern)
}

func TestCheckPermission_Kafka(t *testing.T) {
	ctx, mockAsset, _ := setupPolicyTest(t)
	asset := &asset_entity.Asset{
		ID:   1,
		Type: asset_entity.AssetTypeKafka,
		CmdPolicy: mustJSON(asset_entity.KafkaPolicy{
			AllowList: []string{"topic.read orders-*"},
			DenyList:  []string{"topic.delete *"},
		}),
	}
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

	allowed := CheckPermission(ctx, asset_entity.AssetTypeKafka, 1, "topic.read orders-2026")
	assert.Equal(t, aictx.Allow, allowed.Decision)
	assert.Equal(t, aictx.SourcePolicyAllow, allowed.DecisionSource)

	denied := CheckPermission(ctx, asset_entity.AssetTypeKafka, 1, "topic.delete orders")
	assert.Equal(t, aictx.Deny, denied.Decision)
	assert.Equal(t, aictx.SourcePolicyDeny, denied.DecisionSource)

	needConfirm := CheckPermission(ctx, asset_entity.AssetTypeKafka, 1, "message.read orders")
	assert.Equal(t, aictx.NeedConfirm, needConfirm.Decision)
	assert.Contains(t, needConfirm.HintRules, "topic.read orders-*")
}

func TestCheckPermission_KafkaDefaultPolicy(t *testing.T) {
	ctx, mockAsset, _ := setupPolicyTest(t)
	asset := &asset_entity.Asset{
		ID:   1,
		Type: asset_entity.AssetTypeKafka,
	}
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

	allowed := CheckPermission(ctx, asset_entity.AssetTypeKafka, 1, "topic.read orders")
	assert.Equal(t, aictx.Allow, allowed.Decision)
	assert.Equal(t, aictx.SourcePolicyAllow, allowed.DecisionSource)

	needConfirm := CheckPermission(ctx, asset_entity.AssetTypeKafka, 1, "message.write orders")
	assert.Equal(t, aictx.NeedConfirm, needConfirm.Decision)

	denied := CheckPermission(ctx, asset_entity.AssetTypeKafka, 1, "topic.delete orders")
	assert.Equal(t, aictx.Deny, denied.Decision)
	assert.Equal(t, aictx.SourcePolicyDeny, denied.DecisionSource)
}

func TestCheckPermission_KafkaGrantUsesKafkaMatcher(t *testing.T) {
	ctx, mockAsset, _ := setupPolicyTest(t)
	asset := &asset_entity.Asset{
		ID:   1,
		Type: asset_entity.AssetTypeKafka,
		CmdPolicy: mustJSON(asset_entity.KafkaPolicy{
			AllowList: []string{"topic.read *"},
		}),
	}
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

	stubGrant := newStubGrantRepo()
	origGrant := grant_repo.Grant()
	grant_repo.RegisterGrant(stubGrant)
	t.Cleanup(func() {
		if origGrant != nil {
			grant_repo.RegisterGrant(origGrant)
		}
	})

	stubGrant.sessions["sess-1"] = &grant_entity.GrantSession{
		ID:     "sess-1",
		Status: grant_entity.GrantStatusApproved,
	}
	stubGrant.items["sess-1"] = []*grant_entity.GrantItem{
		{GrantSessionID: "sess-1", AssetID: 1, Command: "message.* orders"},
	}

	result := CheckPermission(aictx.WithSessionID(ctx, "sess-1"), asset_entity.AssetTypeKafka, 1, "message.read orders")
	assert.Equal(t, aictx.Allow, result.Decision)
	assert.Equal(t, aictx.SourceGrantAllow, result.DecisionSource)
	assert.Equal(t, "message.* orders", result.MatchedPattern)
}

func TestCheckPermission_KafkaGrantDoesNotOverrideDefaultDeny(t *testing.T) {
	ctx, mockAsset, _ := setupPolicyTest(t)
	asset := &asset_entity.Asset{
		ID:   1,
		Type: asset_entity.AssetTypeKafka,
	}
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

	stubGrant := newStubGrantRepo()
	origGrant := grant_repo.Grant()
	grant_repo.RegisterGrant(stubGrant)
	t.Cleanup(func() {
		if origGrant != nil {
			grant_repo.RegisterGrant(origGrant)
		}
	})

	stubGrant.sessions["sess-kafka-default-deny"] = &grant_entity.GrantSession{
		ID:     "sess-kafka-default-deny",
		Status: grant_entity.GrantStatusApproved,
	}
	stubGrant.items["sess-kafka-default-deny"] = []*grant_entity.GrantItem{
		{GrantSessionID: "sess-kafka-default-deny", AssetID: 1, Command: "topic.delete *"},
	}

	result := CheckPermission(aictx.WithSessionID(ctx, "sess-kafka-default-deny"), asset_entity.AssetTypeKafka, 1, "topic.delete orders")
	assert.Equal(t, aictx.Deny, result.Decision)
	assert.Equal(t, aictx.SourcePolicyDeny, result.DecisionSource)
}

func TestCommandPolicyChecker_KafkaApprovalType(t *testing.T) {
	ctx, mockAsset, _ := setupPolicyTest(t)
	asset := &asset_entity.Asset{
		ID:   1,
		Name: "local-kafka",
		Type: asset_entity.AssetTypeKafka,
		CmdPolicy: mustJSON(asset_entity.KafkaPolicy{
			AllowList: []string{"topic.read *"},
		}),
	}
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

	var approvalType string
	checker := NewCommandPolicyChecker(func(_ context.Context, _ string, items []ApprovalItem) ApprovalResponse {
		if len(items) > 0 {
			approvalType = items[0].Type
		}
		return ApprovalResponse{Decision: "allow"}
	})

	result := checker.CheckForAsset(ctx, 1, asset_entity.AssetTypeKafka, "message.read orders")
	assert.Equal(t, aictx.Allow, result.Decision)
	assert.Equal(t, aictx.SourceUserAllow, result.DecisionSource)
	assert.Equal(t, "kafka", approvalType)
}
