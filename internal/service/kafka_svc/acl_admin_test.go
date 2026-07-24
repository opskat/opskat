package kafka_svc

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kmsg"
)

func TestNormalizeListACLsRequest(t *testing.T) {
	filter, err := normalizeListACLsRequest(ListACLsRequest{Page: -1, PageSize: 0})
	require.NoError(t, err)
	assert.Equal(t, kmsg.ACLResourceTypeAny, filter.resourceType)
	assert.Equal(t, kmsg.ACLResourcePatternTypeAny, filter.patternType)
	assert.Equal(t, kmsg.ACLOperationAny, filter.operation)
	assert.Equal(t, kmsg.ACLPermissionTypeAny, filter.permission)
	assert.Equal(t, 1, filter.page)
	assert.Equal(t, 50, filter.pageSize)

	filter, err = normalizeListACLsRequest(ListACLsRequest{
		ResourceType: "topic",
		ResourceName: " orders ",
		Principal:    " User:alice ",
		Operation:    "read",
		Permission:   "allow",
	})
	require.NoError(t, err)
	assert.Equal(t, kmsg.ACLResourceTypeTopic, filter.resourceType)
	assert.Equal(t, "orders", filter.resourceName)
	assert.Equal(t, kmsg.ACLResourcePatternTypeMatch, filter.patternType)
	assert.Equal(t, "User:alice", filter.principal)
	assert.Equal(t, kmsg.ACLOperationRead, filter.operation)
	assert.Equal(t, kmsg.ACLPermissionTypeAllow, filter.permission)
}

func TestNormalizeCreateACLRequest(t *testing.T) {
	filter, err := normalizeCreateACLRequest(CreateACLRequest{
		ResourceType: "topic",
		ResourceName: "orders",
		Principal:    "User:alice",
		Operation:    "read",
		Permission:   "allow",
	})
	require.NoError(t, err)
	assert.Equal(t, kmsg.ACLResourceTypeTopic, filter.resourceType)
	assert.Equal(t, kmsg.ACLResourcePatternTypeLiteral, filter.patternType)
	assert.Equal(t, kmsg.ACLOperationRead, filter.operation)
	assert.Equal(t, kmsg.ACLPermissionTypeAllow, filter.permission)

	filter, err = normalizeCreateACLRequest(CreateACLRequest{
		ResourceType: "cluster",
		Principal:    "User:admin",
		Operation:    "describe",
		Permission:   "allow",
	})
	require.NoError(t, err)
	assert.Equal(t, "kafka-cluster", filter.resourceName)

	_, err = normalizeCreateACLRequest(CreateACLRequest{
		ResourceType: "topic",
		Principal:    "User:alice",
		Operation:    "read",
		Permission:   "allow",
	})
	assert.Error(t, err)

	_, err = normalizeCreateACLRequest(CreateACLRequest{
		ResourceType: "topic",
		ResourceName: "orders",
		Principal:    "User:alice",
		Operation:    "any",
		Permission:   "allow",
	})
	assert.Error(t, err)

	_, err = normalizeCreateACLRequest(CreateACLRequest{
		ResourceType: "topic",
		ResourceName: "orders",
		Principal:    "User:alice",
		Operation:    "read",
		Permission:   "any",
	})
	assert.Error(t, err)
}

func TestNormalizeDeleteACLRequestRequiresExactFilter(t *testing.T) {
	filter, err := normalizeDeleteACLRequest(DeleteACLRequest{
		ResourceType: "group",
		ResourceName: "billing",
		Principal:    "User:alice",
		Host:         "*",
		Operation:    "read",
		Permission:   "deny",
	})
	require.NoError(t, err)
	assert.Equal(t, kmsg.ACLResourceTypeGroup, filter.resourceType)
	assert.Equal(t, kmsg.ACLPermissionTypeDeny, filter.permission)

	_, err = normalizeDeleteACLRequest(DeleteACLRequest{
		ResourceType: "group",
		ResourceName: "billing",
		Principal:    "User:alice",
		Operation:    "read",
		Permission:   "deny",
	})
	assert.Error(t, err)

	_, err = normalizeDeleteACLRequest(DeleteACLRequest{
		ResourceType: "any",
		ResourceName: "billing",
		Principal:    "User:alice",
		Host:         "*",
		Operation:    "read",
		Permission:   "deny",
	})
	assert.Error(t, err)
}

func TestListACLsResponseSortsAndPaginates(t *testing.T) {
	response := listACLsResponse([]KafkaACL{
		{ResourceType: "TOPIC", ResourceName: "b", Principal: "User:b"},
		{ResourceType: "TOPIC", ResourceName: "a", Principal: "User:a"},
	}, 1, 1)
	require.Len(t, response.ACLs, 1)
	assert.Equal(t, 2, response.Total)
	assert.Equal(t, "a", response.ACLs[0].ResourceName)
}

// TestNormalizePageClampsPageToAvoidSliceOverflow 锁住分页下标不会溢出成负数。
//
// 两个调用点都用 start := (page-1)*pageSize 算切片下标，page 大到让乘法溢出 int 时
// start 是负数，`if start > total` 拦不住，切片表达式直接 panic。修复前实测：
// normalizePage(200000000000000000, 50) 原样返回，listACLsResponse 随即 panic
// "slice bounds out of range [:-8446744073709551616]"。internal/ai 里没有 recover()，
// 这个 panic 会带走整个桌面进程；而 page/pageSize 是 AI 工具参数，模型可写。
func TestNormalizePageClampsPageToAvoidSliceOverflow(t *testing.T) {
	acls := []KafkaACL{{ResourceType: "TOPIC", ResourceName: "a", Principal: "User:a"}}
	for _, tc := range []struct{ page, pageSize int }{
		{200000000000000000, 50},
		{math.MaxInt, 1},
		{math.MaxInt, 500},
		{math.MaxInt/50 + 2, 50},
	} {
		page, pageSize := normalizePage(tc.page, tc.pageSize)
		require.Positive(t, (page-1)*pageSize, "start index overflowed for page=%d pageSize=%d", tc.page, tc.pageSize)

		// 真正的回归断言：这一行在修复前 panic。
		response := listACLsResponse(acls, page, pageSize)
		assert.Empty(t, response.ACLs, "a page past the end must come back empty, not panic")
		assert.Equal(t, 1, response.Total)
	}
}

// TestNormalizePageKeepsOrdinaryValues 是上面那条钳位的反面守卫：钳位不能顺手改掉
// 正常页码，否则第 2 页会变成别的页。
func TestNormalizePageKeepsOrdinaryValues(t *testing.T) {
	for _, tc := range []struct{ inPage, inSize, wantPage, wantSize int }{
		{0, 0, 1, 50},
		{-5, -1, 1, 50},
		{2, 20, 2, 20},
		{1, 1000, 1, 500},
		{1000000, 50, 1000000, 50},
	} {
		page, pageSize := normalizePage(tc.inPage, tc.inSize)
		assert.Equal(t, tc.wantPage, page)
		assert.Equal(t, tc.wantSize, pageSize)
	}
}
