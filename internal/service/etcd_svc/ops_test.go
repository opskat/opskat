package etcd_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/opskat/opskat/internal/service/etcd_svc/mock_kv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/mock/gomock"
)

func TestDispatchGet_SingleKey(t *testing.T) {
	ctrl := gomock.NewController(t)
	kv := mock_kv.NewMockKV(ctrl)
	kv.EXPECT().
		Get(gomock.Any(), "/foo", gomock.Any()).
		Return(&clientv3.GetResponse{
			Header: &etcdserverpb.ResponseHeader{Revision: 7},
			Count:  1,
			Kvs: []*mvccpb.KeyValue{
				{Key: []byte("/foo"), Value: []byte("bar"), ModRevision: 5, CreateRevision: 5, Version: 1, Lease: 99},
			},
		}, nil)

	res, err := dispatchGet(context.Background(), kv, &ExecRequest{Op: "get", Key: "/foo"})
	require.NoError(t, err)
	require.Len(t, res.KVs, 1)
	assert.Equal(t, "/foo", res.KVs[0].Key)
	assert.Equal(t, "bar", res.KVs[0].Value)
	assert.Equal(t, int64(5), res.KVs[0].ModRevision)
	assert.Equal(t, int64(1), res.KVs[0].Version)
	assert.Equal(t, int64(99), res.KVs[0].Lease)
	assert.Equal(t, int64(7), res.Revision)
}

func TestDispatchGet_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	kv := mock_kv.NewMockKV(ctrl)
	kv.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, errors.New("rpc fail"))

	res, err := dispatchGet(context.Background(), kv, &ExecRequest{Op: "get", Key: "/x"})
	assert.Nil(t, res)
	assert.Error(t, err)
}

func TestDispatchGet_PrefixAndLimit(t *testing.T) {
	ctrl := gomock.NewController(t)
	kv := mock_kv.NewMockKV(ctrl)
	kv.EXPECT().
		Get(gomock.Any(), "/p/", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
			// 至少 prefix + limit 两个选项
			assert.Len(t, opts, 2)
			return &clientv3.GetResponse{Header: &etcdserverpb.ResponseHeader{}, Count: 0}, nil
		})

	_, err := dispatchGet(context.Background(), kv, &ExecRequest{Op: "get", Key: "/p/", Prefix: true, Limit: 50})
	require.NoError(t, err)
}

func TestDispatchPut(t *testing.T) {
	ctrl := gomock.NewController(t)
	kv := mock_kv.NewMockKV(ctrl)
	kv.EXPECT().
		Put(gomock.Any(), "/foo", "bar", gomock.Any()).
		Return(&clientv3.PutResponse{Header: &etcdserverpb.ResponseHeader{Revision: 10}}, nil)

	res, err := dispatchPut(context.Background(), kv, &ExecRequest{Op: "put", Key: "/foo", Value: "bar"})
	require.NoError(t, err)
	assert.Equal(t, int64(10), res.Revision)
	assert.Equal(t, int64(1), res.Count)
}

func TestDispatchPut_WithLease(t *testing.T) {
	ctrl := gomock.NewController(t)
	kv := mock_kv.NewMockKV(ctrl)
	kv.EXPECT().
		Put(gomock.Any(), "/k", "v", gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, opts ...clientv3.OpOption) (*clientv3.PutResponse, error) {
			assert.NotEmpty(t, opts, "lease option should be passed when LeaseID > 0")
			return &clientv3.PutResponse{Header: &etcdserverpb.ResponseHeader{}}, nil
		})

	_, err := dispatchPut(context.Background(), kv, &ExecRequest{Op: "put", Key: "/k", Value: "v", LeaseID: 0xabc})
	require.NoError(t, err)
}

func TestDispatchDel(t *testing.T) {
	ctrl := gomock.NewController(t)
	kv := mock_kv.NewMockKV(ctrl)
	kv.EXPECT().
		Delete(gomock.Any(), "/locks/a", gomock.Any()).
		Return(&clientv3.DeleteResponse{Header: &etcdserverpb.ResponseHeader{Revision: 12}, Deleted: 1}, nil)

	res, err := dispatchDel(context.Background(), kv, &ExecRequest{Op: "del", Key: "/locks/a"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.Count)
	assert.Equal(t, int64(12), res.Revision)
}

func TestDispatchDel_WithPrefix(t *testing.T) {
	ctrl := gomock.NewController(t)
	kv := mock_kv.NewMockKV(ctrl)
	kv.EXPECT().
		Delete(gomock.Any(), "/locks/", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, opts ...clientv3.OpOption) (*clientv3.DeleteResponse, error) {
			assert.NotEmpty(t, opts, "prefix option should be passed")
			return &clientv3.DeleteResponse{Header: &etcdserverpb.ResponseHeader{}, Deleted: 3}, nil
		})

	res, err := dispatchDel(context.Background(), kv, &ExecRequest{Op: "del", Key: "/locks/", Prefix: true})
	require.NoError(t, err)
	assert.Equal(t, int64(3), res.Count)
}

func TestDispatchLeaseGrant(t *testing.T) {
	ctrl := gomock.NewController(t)
	lease := mock_kv.NewMockLease(ctrl)
	lease.EXPECT().
		Grant(gomock.Any(), int64(60)).
		Return(&clientv3.LeaseGrantResponse{ID: clientv3.LeaseID(0xabc), TTL: 60}, nil)

	res, err := dispatchLeaseGrant(context.Background(), lease, &ExecRequest{Op: "lease_grant", Args: map[string]any{"ttl": int64(60)}})
	require.NoError(t, err)
	assert.Equal(t, "lease_grant", res.Op)
	require.Len(t, res.KVs, 1)
	assert.Equal(t, int64(0xabc), res.KVs[0].Lease)
}

func TestDispatchLeaseGrant_MissingTTL(t *testing.T) {
	ctrl := gomock.NewController(t)
	lease := mock_kv.NewMockLease(ctrl)
	// Grant should NOT be called — ttl missing
	res, err := dispatchLeaseGrant(context.Background(), lease, &ExecRequest{Op: "lease_grant"})
	assert.Nil(t, res)
	assert.Error(t, err)
}

func TestDispatchLeaseRevoke(t *testing.T) {
	ctrl := gomock.NewController(t)
	lease := mock_kv.NewMockLease(ctrl)
	lease.EXPECT().
		Revoke(gomock.Any(), clientv3.LeaseID(0xabc)).
		Return(&clientv3.LeaseRevokeResponse{}, nil)

	res, err := dispatchLeaseRevoke(context.Background(), lease, &ExecRequest{Op: "lease_revoke", LeaseID: 0xabc})
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.Count)
}

func TestDispatchLeaseList(t *testing.T) {
	ctrl := gomock.NewController(t)
	lease := mock_kv.NewMockLease(ctrl)
	lease.EXPECT().
		Leases(gomock.Any()).
		Return(&clientv3.LeaseLeasesResponse{Leases: []clientv3.LeaseStatus{
			{ID: clientv3.LeaseID(1)}, {ID: clientv3.LeaseID(2)},
		}}, nil)

	res, err := dispatchLeaseList(context.Background(), lease)
	require.NoError(t, err)
	assert.Equal(t, int64(2), res.Count)
	require.Len(t, res.KVs, 2)
}

func TestDispatchMemberList(t *testing.T) {
	ctrl := gomock.NewController(t)
	cluster := mock_kv.NewMockCluster(ctrl)
	cluster.EXPECT().
		MemberList(gomock.Any()).
		Return(&clientv3.MemberListResponse{Members: []*etcdserverpb.Member{
			{ID: 1, Name: "n1", ClientURLs: []string{"http://10.0.0.1:2379"}},
			{ID: 2, Name: "n2", ClientURLs: []string{"http://10.0.0.2:2379"}},
		}}, nil)

	res, err := dispatchMemberList(context.Background(), cluster, &ExecRequest{Op: "member_list"})
	require.NoError(t, err)
	assert.Equal(t, int64(2), res.Count)
	require.Len(t, res.KVs, 2)
}

func TestDispatchEndpointStatus(t *testing.T) {
	ctrl := gomock.NewController(t)
	ms := mock_kv.NewMockMaintenance(ctrl)
	ms.EXPECT().
		Status(gomock.Any(), "127.0.0.1:2379").
		Return(&clientv3.StatusResponse{Version: "3.5.16", DbSize: 4096, Leader: 1}, nil)

	res, err := dispatchEndpointStatus(context.Background(), ms, &ExecRequest{Op: "endpoint_status", Key: "127.0.0.1:2379"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.Count)
	require.Len(t, res.KVs, 1)
	assert.Equal(t, "127.0.0.1:2379", res.KVs[0].Key)
	assert.Contains(t, res.KVs[0].Value, "3.5.16")
}

func TestDispatchEndpointStatus_MissingKey(t *testing.T) {
	ctrl := gomock.NewController(t)
	ms := mock_kv.NewMockMaintenance(ctrl)
	// Status should NOT be called — key missing
	res, err := dispatchEndpointStatus(context.Background(), ms, &ExecRequest{Op: "endpoint_status"})
	assert.Nil(t, res)
	assert.Error(t, err)
}

// TestSupportedOpsAreDispatchable 守护 ParseCommand 接受的 op 必须能被 Dispatch 路由。
// 若 supportedOps 新增了 op 但忘了实现 dispatch，这里会失败。
func TestSupportedOpsAreDispatchable(t *testing.T) {
	for op := range supportedOps {
		t.Run(op, func(t *testing.T) {
			// 用 nil client：路由到具体 dispatch 函数后会因 nil deref / 缺参 panic 或返错，
			// 唯一不应出现的是 "unsupported op: ..." —— 那意味着 Dispatch switch 缺分支。
			defer func() {
				if r := recover(); r != nil {
					// nil client 触发的 panic 不算路由缺失
					return
				}
			}()
			_, err := Dispatch(context.Background(), nil, &ExecRequest{Op: op})
			if err != nil {
				assert.NotContains(t, err.Error(), "unsupported op",
					"supportedOps contains %q but Dispatch has no branch for it", op)
			}
		})
	}
}
