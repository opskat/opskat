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

func TestDispatch_RoutingAndUnsupported(t *testing.T) {
	ctrl := gomock.NewController(t)
	kv := mock_kv.NewMockKV(ctrl)
	kv.EXPECT().Get(gomock.Any(), "/r", gomock.Any()).Return(&clientv3.GetResponse{Header: &etcdserverpb.ResponseHeader{}}, nil)

	// Dispatch 接受 KV 接口(不是 *clientv3.Client) — 让 Dispatch 签名按测试推断
	res, err := Dispatch(context.Background(), kv, &ExecRequest{Op: "get", Key: "/r"})
	require.NoError(t, err)
	assert.Equal(t, "get", res.Op)

	_, err = Dispatch(context.Background(), kv, &ExecRequest{Op: "lease_grant", Args: map[string]any{"ttl": int64(60)}})
	assert.Error(t, err, "lease_grant should be unsupported in Task 10 (handled in Task 11)")
}
