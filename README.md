# k8s-pods-viewer

`k8s-pods-viewer` is a keyboard-first terminal UI for watching live pod CPU and memory usage in Kubernetes.

It is designed for the common questions behind noisy clusters: which pods are using CPU and memory right now, how does that compare to requests and limits, which nodes are carrying the pressure, and what can I do about a selected pod without leaving the viewer.

> Inspired by [awslabs/eks-node-viewer](https://github.com/awslabs/eks-node-viewer).

## Quick Install

> Recommended install:

```bash
brew install lavluda/tap/k8s-pods-viewer
```

Latest release notes: [v1.6.0](docs/releases/v1.6.0.md).

## Preview

![k8s-pods-viewer dashboard](docs/assets/k8s-pods-viewer-v1.4.0.png)

## What It Shows

- A live pod list with separate `cpu` and `memory` bars
- Keyboard pod selection that follows the exact rendered on-screen order
- Auto-sorted pod order that stays steady briefly after keyboard input, so rows do not jump while navigating
- A contextual right-side popup menu for the selected pod
- Pod actions for `exec`, `logs`, `describe`, `kill pod`, `scale -1`, and `scale +1`
- Multi-container handling for `exec` and `logs`, with a container picker when needed
- Inline confirmation for dangerous actions with keyboard-selectable `Confirm` / `Cancel`
- In-app scrollable viewers for `logs` and `describe`
- Namespace, workload, or flat grouping for pod rows
- Adaptive multi-column pod layout on wide terminals, with `←`/`→` to move
  between columns and `pgup`/`pgdn` (or `[`/`]`) to change pages
- Semantic pod status indicators that prefer current readiness over stale restart history
- Keyboard sorting for `cpu`, `memory`, and `status`
- Live `/` filtering across pod, namespace, workload, and node names
- Compact node aliases with a detailed info toggle for optional instance type,
  region/zone, node pool, capacity type, OS, architecture, kubelet, and runtime metadata
- A right-side node panel with per-node CPU and memory usage
- A platform-aware node metadata panel beside the pod list, titled for the detected
  platform (`EKS Nodes`, `GKE Nodes`, `AKS Nodes`, or `Node Fleet` on unmanaged
  clusters), with per-node instance type, capacity type, zone, and node group plus
  cluster-wide rollups
- A top dashboard header with context, cluster summary, and keyboard shortcuts
- A right-side highlights rail for hot pods, restart visibility, and cluster watch status
- Direct initial pod/node fetch before watches start, so the UI can render immediately
- Recovery-friendly watch status reporting instead of crashing the TUI on informer/poller failures
- Cluster-level CPU and memory summary at the top
- A cluster running-cost tile beside `Cluster Memory` with live AWS on-demand and
  spot prices for the visible nodes (off by default; `--cost` to enable)
- Filters for namespace, pod labels, and node labels

By default the screen refreshes every `2s`.

The viewer runs in the normal terminal screen by default for better compatibility with terminals that clip the first rows in alt-screen mode. If you prefer the alternate screen buffer, use `--alt-screen`.

## Bar Semantics

The UI intentionally shows two different kinds of numbers:

- Pod bars: `% of live usage / pod baseline` with the percentage shown inside the bar
- Pod value text: `live usage / baseline`
- Node panel percentage: `live node usage / node allocatable`
- Node panel `asg/lim`: `sum of pod requests on the node / node allocatable`

For pods, the baseline is usually the limit when present, and falls back to the request when there is no limit. That means a pod line can show `500m/1200m` while the bar itself is still based on actual live usage from the metrics API.

## Platform Node Metadata

The node metadata panel adapts to the cluster it is pointed at:

- The platform is detected from the nodes themselves (`eks.amazonaws.com/*`,
  `cloud.google.com/gke-*`, `kubernetes.azure.com/*`, `karpenter.sh/*`, and the
  node `providerID`), so the panel title and the node group wording match the
  provider. A Karpenter node on an AWS provider ID still reads as `EKS`.
- Columns are only drawn when at least one node has a value for them. On EKS that
  means instance type, capacity type, zone, and nodegroup. On a `kind` or
  bare-metal cluster the same panel falls back to os/arch and kubelet.
- The rollup section under the table shows the fleet distribution: nodegroups,
  on-demand vs spot, instance types, zones, and kubelet versions.
- `i` (detailed info) adds the EC2/VM machine ID column and region, arch, AMI/image,
  OS image, and container runtime rollups.
- Everything comes from the Kubernetes node objects. No cloud API calls are made,
  so no AWS, GCP, or Azure credentials are needed.
- When the sidebar runs short of vertical space the panel drops its rollup first,
  then trims rows and reports the remainder as `+N more nodes`.

## Cluster Cost

The `Cluster Cost` tile sits beside `Cluster Memory` and shows what the visible
nodes cost to keep running:

- The big number is the summed hourly price of the nodes, and the sub-line is the
  monthly projection (`hourly × 730`) plus how many nodes were priced.
- On-demand prices come from the AWS Pricing API (`GetProducts`), matched on
  instance type, region, OS, shared tenancy, and used capacity status.
- Spot nodes are priced from the most recent EC2 spot price for their exact
  availability zone (`DescribeSpotPriceHistory`). Until that sample arrives, the
  node is charged at its on-demand rate, which is the ceiling of what it can cost.
- Prices are fetched in the background with the local AWS credentials (honoring
  `--aws-profile`), cached in memory, and never block the UI. On-demand prices
  refresh every 12 hours, spot prices every 10 minutes.
- Only AWS-backed nodes are priced. Other platforms show `n/a · no priced platform`
  instead of sending unknown instance types to the AWS API.
- When credentials or permissions are missing, the tile shows `n/a` with a short
  reason such as `no AWS credentials` or `pricing access denied`.
- The tile covers node compute only. Control-plane fees, EBS volumes, load
  balancers, snapshots, and data transfer are not included.
- Off by default. Pass `--cost` (or set `cost=true` in the config file) to show
  the tile and fetch AWS prices. Omit the flag / set `cost=false` to keep it off.

## Health Semantics

- `Running` and ready pods are healthy
- `NotReady`, `Pending`, restart loops, and waiting states are surfaced as warning or critical
- `Failed` pods are critical
- Status sorting uses the computed health severity rather than raw phase text
- The top `Unhealthy` count reflects pods currently classified as warning or critical

## Requirements

- Access to a Kubernetes cluster via `kubeconfig`
- Permission to `list/watch` `pods` in the namespace(s) being viewed
- Optional permission to `list/watch` `nodes` for node pressure and node filtering
- Optional permission to read pod metrics from `metrics.k8s.io` for live usage
- A working `metrics.k8s.io` provider, typically Metrics Server
- Optional AWS credentials with `pricing:GetProducts` and
  `ec2:DescribeSpotPriceHistory` for the cluster cost tile (read-only; only used
  when cost is enabled with `--cost` / `cost=true`)

Cluster-admin access is not required. If node access is forbidden, the viewer continues
in pod-only mode. If pod metrics are unavailable, the viewer continues without live
usage data. See [Least-Privilege RBAC](docs/rbac.md) for namespace-scoped examples.

## Install

### Homebrew

```bash
brew install lavluda/tap/k8s-pods-viewer
```

### Go install

```bash
go install github.com/lavluda/k8s-pods-viewer/cmd/k8s-pods-viewer@latest
```

### Build from source

```bash
git clone https://github.com/lavluda/k8s-pods-viewer.git
cd k8s-pods-viewer
make build
```

The binary will be available as `./k8s-pods-viewer`.

## Quick Start

Use your current kubeconfig context:

```bash
k8s-pods-viewer
```

Run directly from source:

```bash
go run ./cmd/k8s-pods-viewer
```

Use all namespaces explicitly:

```bash
k8s-pods-viewer --namespace all
```

## Examples

Watch a single namespace:

```bash
k8s-pods-viewer --namespace production
```

Filter pods by label:

```bash
k8s-pods-viewer --pod-selector app=api
```

Filter nodes by label:

```bash
k8s-pods-viewer --node-selector karpenter.sh/nodepool=default
```

Sort by memory usage descending:

```bash
k8s-pods-viewer --pod-sort memory=dsc
```

Sort by creation time ascending:

```bash
k8s-pods-viewer --pod-sort creation=asc
```

Use a specific kubeconfig and context:

```bash
k8s-pods-viewer \
  --kubeconfig ~/.kube/config \
  --context my-cluster
```

Use an AWS profile for EKS authentication:

```bash
k8s-pods-viewer --aws-profile alex
```

This sets `AWS_PROFILE` for both the Kubernetes client and pod actions that invoke `kubectl`. You can also use the environment variable directly:

```bash
AWS_PROFILE=alex k8s-pods-viewer
```

## Configuration File

The tool reads optional defaults from:

```text
~/.k8s-pods-viewer
```

Format:

```ini
context=my-cluster
kubeconfig=/Users/you/.kube/config
aws-profile=alex
namespace=production
node-selector=karpenter.sh/nodepool=default
pod-selector=app=api
pod-sort=cpu=dsc
resources=cpu,memory
style=#04B575,#FFFF00,#FF0000
alt-screen=false
cost=false
```

CLI flags override config file values.

## Flags

```text
-attribution
    Show the Open Source Attribution
-aws-profile string
    AWS profile to use for EKS authentication
-context string
    Name of the kubernetes context to use
-kubeconfig string
    Absolute path to the kubeconfig file
-namespace string
    Namespace to watch; empty means all namespaces
-node-selector string
    Node label selector used to filter nodes
-pod-selector string
    Pod label selector used to filter pods
-pod-sort string
    Sort order for pods. Examples: cpu=dsc, memory=asc, creation=dsc, namespace=asc
-resources string
    List of comma separated resources to monitor (default: cpu,memory)
-style string
    Three colors for styling 'good', 'ok' and 'bad' values
-alt-screen
    Run in the terminal alternate screen buffer
-cost
    Show the cluster running cost tile (off by default). Uses read-only AWS Pricing
    and EC2 spot price APIs with the local credentials; pass --cost to enable
-v, -version
    Display k8s-pods-viewer version
```

## Controls

- `↑/↓`: move pod selection
- `←/→`: change page
- `enter`: open the selected pod action menu
- `c`: sort by CPU
- `m`: sort by memory
- `s`: sort by status
- `g`: cycle grouping (`namespace`, `workload`, `flat`)
- `i`: toggle compact vs detailed mode
- `/`: filter pods
- `esc`: clear filter, close a popup, or back out of confirmation
- `q`: quit

When the pod list is sorted by changing live data such as CPU or memory, keyboard input briefly pauses automatic reordering. New pods are still shown, but existing rows stay in their current order long enough to move, inspect, and open actions without the selection jumping underneath you.

## Pod Actions

Press `enter` on the selected pod to open the popup menu.

- `Exec`: opens `kubectl exec -it ... -- sh`
- `Logs`: opens recent pod logs in the built-in viewer
- `Describe`: opens `kubectl describe pod` in the built-in viewer
- `Kill Pod`: runs `kubectl delete pod --wait=false`
- `Scale -1`: decreases the owning scalable workload by one replica
- `Scale +1`: increases the owning scalable workload by one replica

Scale actions appear only when the selected pod belongs to a scalable workload such as a Deployment or StatefulSet.

If a pod has multiple regular containers, `exec` and `logs` first open a container picker.

Dangerous actions use a confirmation step. The confirm popup supports:

- `←/→`, `↑/↓`, `h/l`, `j/k`, or `tab`: switch between `Confirm` and `Cancel`
- `enter`: activate the focused button
- `esc`: cancel and return to the action menu

## Log And Describe Viewer

The built-in output viewer is used for pod logs and describe output.

- `↑/↓` or `j/k`: scroll
- `pgup` / `pgdn` or `b/f`: page scroll
- `g`: jump to top
- `G`: jump to bottom
- `esc` or `q`: close the viewer

## Development

Run tests:

```bash
GOCACHE=/tmp/eks-pods-view-go-cache go test ./pkg/... ./cmd/...
```

Generate attribution files:

```bash
make generate
```

Build locally:

```bash
make build
```

Run the release flag matrix:

```bash
make test-release
```

## Acknowledgments

This project was inspired by [awslabs/eks-node-viewer](https://github.com/awslabs/eks-node-viewer).
While eks-node-viewer focuses on node-level cost and capacity, k8s-pods-viewer
focuses on live pod-level resource usage across any Kubernetes cluster.

## License

This project is licensed under Apache-2.0. See [LICENSE](LICENSE).

See [NOTICE](NOTICE) and [ATTRIBUTION.md](ATTRIBUTION.md) for third-party acknowledgments.
