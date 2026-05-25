package etcd_svc

import (
	"context"
	"fmt"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// EtcdKV 是返回给 IPC 的 KV 投影,屏蔽 etcd 内部 protobuf 类型。
type EtcdKV struct {
	Key            string `json:"key"`
	Value          string `json:"value"`
	ModRevision    int64  `json:"modRevision"`
	CreateRevision int64  `json:"createRevision"`
	Version        int64  `json:"version"`
	Lease          int64  `json:"lease"`
}

// ExecResult 是 etcd 操作的统一返回。
type ExecResult struct {
	Op       string   `json:"op"`
	KVs      []EtcdKV `json:"kvs,omitempty"`
	Count    int64    `json:"count"`
	Revision int64    `json:"revision"`
}

func dispatchGet(ctx context.Context, kv clientv3.KV, req *ExecRequest) (*ExecResult, error) {
	opts := []clientv3.OpOption{}
	if req.Prefix {
		opts = append(opts, clientv3.WithPrefix())
	}
	if req.Limit > 0 {
		opts = append(opts, clientv3.WithLimit(req.Limit))
	}
	if req.Revision > 0 {
		opts = append(opts, clientv3.WithRev(req.Revision))
	}
	resp, err := kv.Get(ctx, req.Key, opts...)
	if err != nil {
		return nil, fmt.Errorf("etcd get failed: %w", err)
	}
	res := &ExecResult{Op: "get", Count: resp.Count, Revision: resp.Header.Revision}
	for _, k := range resp.Kvs {
		res.KVs = append(res.KVs, EtcdKV{
			Key:            string(k.Key),
			Value:          string(k.Value),
			ModRevision:    k.ModRevision,
			CreateRevision: k.CreateRevision,
			Version:        k.Version,
			Lease:          k.Lease,
		})
	}
	return res, nil
}

func dispatchPut(ctx context.Context, kv clientv3.KV, req *ExecRequest) (*ExecResult, error) {
	opts := []clientv3.OpOption{}
	if req.LeaseID > 0 {
		opts = append(opts, clientv3.WithLease(clientv3.LeaseID(req.LeaseID)))
	}
	resp, err := kv.Put(ctx, req.Key, req.Value, opts...)
	if err != nil {
		return nil, fmt.Errorf("etcd put failed: %w", err)
	}
	return &ExecResult{Op: "put", Count: 1, Revision: resp.Header.Revision}, nil
}

func dispatchDel(ctx context.Context, kv clientv3.KV, req *ExecRequest) (*ExecResult, error) {
	opts := []clientv3.OpOption{}
	if req.Prefix {
		opts = append(opts, clientv3.WithPrefix())
	}
	resp, err := kv.Delete(ctx, req.Key, opts...)
	if err != nil {
		return nil, fmt.Errorf("etcd del failed: %w", err)
	}
	return &ExecResult{Op: "del", Count: resp.Deleted, Revision: resp.Header.Revision}, nil
}

// Dispatch 按 op 路由到细分 dispatch 函数。Task 10 只覆盖 get/put/del,
// 其它 op(lease/member/endpoint/...)在 Task 11 追加。
func Dispatch(ctx context.Context, kv clientv3.KV, req *ExecRequest) (*ExecResult, error) {
	switch req.Op {
	case "get":
		return dispatchGet(ctx, kv, req)
	case "put":
		return dispatchPut(ctx, kv, req)
	case "del":
		return dispatchDel(ctx, kv, req)
	default:
		return nil, fmt.Errorf("unsupported op: %s", req.Op)
	}
}
