# Least-Privilege RBAC

`k8s-pods-viewer` does not require EKS or Kubernetes cluster-admin access.

The minimum useful permission is `list` and `watch` on pods in one namespace.
Run the viewer with the same namespace:

```bash
k8s-pods-viewer --namespace production
```

An administrator for that namespace can grant read-only pod and metrics access:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: k8s-pods-viewer
  namespace: production
rules:
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["metrics.k8s.io"]
    resources: ["pods"]
    verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: k8s-pods-viewer
  namespace: production
subjects:
  - kind: User
    name: <kubernetes-username-or-iam-arn>
    apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: Role
  name: k8s-pods-viewer
  apiGroup: rbac.authorization.k8s.io
```

Node details are optional because nodes are cluster-scoped. To enable the node
pressure panel, grant `get`, `list`, and `watch` on nodes through a ClusterRole.

Pod actions need additional permissions:

- logs: `get` on `pods/log`
- exec: `create` on `pods/exec`
- kill: `delete` on `pods`
- scale: `get` and `update` on the workload's `scale` subresource

Check the current identity before launching:

```bash
kubectl auth can-i list pods --namespace production
kubectl auth can-i watch pods --namespace production
kubectl auth can-i list pods.metrics.k8s.io --namespace production
kubectl auth can-i list nodes
```
