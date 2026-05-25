//go:generate mockgen -destination=./mock_kv.go -package=mock_kv go.etcd.io/etcd/client/v3 KV,Lease,Cluster,Maintenance
package mock_kv
