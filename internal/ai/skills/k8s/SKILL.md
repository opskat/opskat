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

## Asset config (for put_asset)

| field | type | required | notes |
|---|---|---|---|
| `kubeconfig` | string | yes | **Write-only.** Full YAML encrypted directly in the asset config; it does not create a managed credential |
| `namespace` | string | no | Default namespace; must not contain whitespace or start with `-` |
| `context` | string | no | Kubeconfig context to use; same character restrictions as `namespace` |
| `ssh_asset_id` | number | no | SSH asset to tunnel through; 0 detaches |
| `password` | string | no | Reserved compatibility key; any non-empty value is rejected as inapplicable |
| `credential_id` | number | no | Reserved compatibility key; any non-zero value is rejected as inapplicable |

Never echo kubeconfig. It remains direct encrypted asset material, unlike automation
password/private-key fields. Use top-level `credential_name` for neither K8s path.
