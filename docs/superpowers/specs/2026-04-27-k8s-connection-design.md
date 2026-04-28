# K8S Cluster Connection & Basic Info Panel

**Date:** 2026-04-27
**Status:** approved

## Overview

Add real K8S cluster connectivity via `k8s.io/client-go`, enabling users to connect to K8S clusters and view basic cluster information (version, nodes, namespaces) through the desktop app.

## Scope

- Add `k8s.io/client-go` and related K8S Go libraries
- Build a K8S client layer that creates a `kubernetes.Clientset` from asset config (kubeconfig YAML)
- Create a Wails binding `GetK8sClusterInfo(assetID int64) (string, error)` that returns cluster info JSON
- On the frontend, change K8S asset type to `canConnect: true` and show cluster info in a dialog on connect

**Out of scope:**
- Persistent K8S connections or real-time watch
- Pod listing, log viewing, or kubectl execution (future work)
- CA certificate validation (uses Insecure TLS for now)

## Architecture

```
Frontend (K8sDetailInfoCard + K8sClusterInfoDialog)
   └─ GetK8sClusterInfo(assetId) → Wails IPC
        └─ Backend app_k8s.go (thin binding)
             └─ k8s/client.go (K8sClient)
                  ├─ Build rest.Config from kubeconfig
                  ├─ Create kubernetes.Clientset
                  └─ GetClusterInfo():
                       ├─ ServerVersion()
                       ├─ ListNodes()  → filter to safe fields
                       └─ ListNamespaces()
```

## Files Changed / Created

### New Files (Backend)

| File | Purpose |
|------|---------|
| `internal/k8s/client.go` | K8S client builder + `GetClusterInfo()` |
| `internal/app/app_k8s.go` | Wails binding `GetK8sClusterInfo` |

### Modified Files (Backend)

| File | Change |
|------|--------|
| `go.mod` | Add `k8s.io/client-go`, `k8s.io/api`, `k8s.io/apimachinery` |
| `internal/assettype/k8s.go` | `DefaultPort()` returns 6443 |
| `internal/app/app.go` | Import k8s package if needed |

### Modified Files (Frontend)

| File | Change |
|------|--------|
| `src/lib/assetTypes/k8s.ts` | `canConnect: true`, add connect handler |
| `src/components/asset/detail/K8sDetailInfoCard.tsx` | Add connect button + cluster info dialog |
| `src/i18n/locales/en/common.json` | Add K8S connect/cluster info i18n keys |
| `src/i18n/locales/zh-CN/common.json` | Add Chinese translations |

## K8S Config Resolution Logic

```
internal/k8s/client.go:

func NewClient(cfg *K8sConfig) (*K8sClient, error)
  1. If cfg.Kubeconfig != "":
       restConfig = clientcmd.RESTConfigFromKubeConfig([]byte(cfg.Kubeconfig))
       If cfg.Context != "": override restConfig with specified context
  2. Create clientset = kubernetes.NewForConfig(restConfig)
  3. Ping: clientset.ServerVersion() to validate connectivity
```

## ClusterInfo Response Schema

```json
{
  "version": "v1.32.0",
  "platform": "linux/amd64",
  "nodes": [
    {
      "name": "node-1",
      "status": "Ready",
      "roles": ["control-plane", "worker"],
      "version": "v1.32.0",
      "cpu": "4",
      "memory": "16Gi",
      "os": "Ubuntu 22.04",
      "arch": "amd64"
    }
  ],
  "namespaces": [
    {"name": "default", "status": "Active"},
    {"name": "kube-system", "status": "Active"}
  ]
}
```

## Frontend UI Flow

1. User double-clicks K8S asset in sidebar or clicks "Connect" in AssetDetail
2. Frontend calls `GetK8sClusterInfo(assetId)` via Wails IPC
3. Shows loading spinner while waiting
4. On success: opens a Dialog displaying cluster version, node list, namespace list
5. On error: shows error toast with message

## Security

- Credentials should be embedded in kubeconfig; standalone API server + bearer token auth is not supported
- TLS verification skipped (`Insecure: true`) for internal clusters; CA cert support can be added later
- Connection timeout: 30 seconds
- Node/node resource data is read-only, no secrets/configmaps exposed

## Testing

- Go unit tests for `k8s/client.go` config building (mock clientset)
- Frontend: existing registry test updated for `canConnect: true`
- Manual E2E: add a K8S asset with kubeconfig, click connect, verify info panel

## Dependencies

```
k8s.io/client-go v0.34.1
k8s.io/api v0.34.1
k8s.io/apimachinery v0.34.1
```
