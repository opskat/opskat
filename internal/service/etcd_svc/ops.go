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

func dispatchLeaseGrant(ctx context.Context, lease clientv3.Lease, req *ExecRequest) (*ExecResult, error) {
	ttl, _ := req.Args["ttl"].(int64)
	if ttl <= 0 {
		return nil, fmt.Errorf("lease_grant requires positive ttl")
	}
	resp, err := lease.Grant(ctx, ttl)
	if err != nil {
		return nil, fmt.Errorf("etcd lease grant failed: %w", err)
	}
	return &ExecResult{
		Op:    "lease_grant",
		Count: 1,
		KVs: []EtcdKV{{
			Lease: int64(resp.ID),
			Value: fmt.Sprintf("ttl=%d", resp.TTL),
		}},
	}, nil
}

func dispatchLeaseRevoke(ctx context.Context, lease clientv3.Lease, req *ExecRequest) (*ExecResult, error) {
	if req.LeaseID == 0 {
		return nil, fmt.Errorf("lease_revoke requires lease id")
	}
	if _, err := lease.Revoke(ctx, clientv3.LeaseID(req.LeaseID)); err != nil {
		return nil, fmt.Errorf("etcd lease revoke failed: %w", err)
	}
	return &ExecResult{Op: "lease_revoke", Count: 1}, nil
}

func dispatchLeaseList(ctx context.Context, lease clientv3.Lease) (*ExecResult, error) {
	resp, err := lease.Leases(ctx)
	if err != nil {
		return nil, fmt.Errorf("etcd lease list failed: %w", err)
	}
	res := &ExecResult{Op: "lease_list", Count: int64(len(resp.Leases))}
	for _, l := range resp.Leases {
		res.KVs = append(res.KVs, EtcdKV{Lease: int64(l.ID)})
	}
	return res, nil
}

func dispatchMemberList(ctx context.Context, cluster clientv3.Cluster, _ *ExecRequest) (*ExecResult, error) {
	resp, err := cluster.MemberList(ctx)
	if err != nil {
		return nil, fmt.Errorf("etcd member list failed: %w", err)
	}
	res := &ExecResult{Op: "member_list", Count: int64(len(resp.Members))}
	for _, m := range resp.Members {
		res.KVs = append(res.KVs, EtcdKV{
			Key:   fmt.Sprintf("%x", m.ID),
			Value: fmt.Sprintf("name=%s urls=%v", m.Name, m.ClientURLs),
		})
	}
	return res, nil
}

// dispatchEndpointStatus 处理 endpoint_status 与 endpoint_health。
// 注意:当前 ParseCommand 不会为 endpoint_* op 写入 req.Key,
// 需由调用方(Task 12 Service / Task 20 query UI)显式提供 endpoint 地址。
func dispatchEndpointStatus(ctx context.Context, ms clientv3.Maintenance, req *ExecRequest) (*ExecResult, error) {
	if req.Key == "" {
		return nil, fmt.Errorf("%s requires endpoint key", req.Op)
	}
	resp, err := ms.Status(ctx, req.Key)
	if err != nil {
		return nil, fmt.Errorf("etcd endpoint status failed: %w", err)
	}
	return &ExecResult{
		Op:    req.Op,
		Count: 1,
		KVs:   []EtcdKV{{Key: req.Key, Value: fmt.Sprintf("version=%s dbSize=%d leader=%x", resp.Version, resp.DbSize, resp.Leader)}},
	}, nil
}

// Dispatch 按 op 路由到细分 dispatch 函数。
// 接收 *clientv3.Client(嵌入 KV/Lease/Cluster/Maintenance 接口),
// per-op 函数仍取窄接口以便 mock 单测。
func Dispatch(ctx context.Context, client *clientv3.Client, req *ExecRequest) (*ExecResult, error) {
	switch req.Op {
	case "get":
		return dispatchGet(ctx, client, req)
	case "put":
		return dispatchPut(ctx, client, req)
	case "del":
		return dispatchDel(ctx, client, req)
	case "lease_grant":
		return dispatchLeaseGrant(ctx, client, req)
	case "lease_revoke":
		return dispatchLeaseRevoke(ctx, client, req)
	case "lease_list":
		return dispatchLeaseList(ctx, client)
	case "member_list":
		return dispatchMemberList(ctx, client, req)
	case "endpoint_status", "endpoint_health":
		return dispatchEndpointStatus(ctx, client, req)
	default:
		return nil, fmt.Errorf("unsupported op: %s", req.Op)
	}
}
