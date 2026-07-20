---
name: k8s
description: "Run kubectl commands against a Kubernetes asset via exec. Covers command syntax and the stored kubeconfig / jump-host behavior."
---

# Kubernetes assets

## Command syntax

Pass the kubectl command as `command`, with or without the leading `kubectl`:

- `get pods -A`
- `describe pod api-0 -n prod`
- `logs deploy/api --tail 100`
- `kubectl get nodes -o wide`

## Notes

- The asset's stored kubeconfig is used automatically. **Do not pass
  `--kubeconfig`.**
- The asset's default context and namespace are applied when the command does
  not specify them.
- If the asset has an SSH jump host configured, kubectl runs on that host;
  otherwise it runs locally.
- The `scope` parameter is not used by Kubernetes assets.
