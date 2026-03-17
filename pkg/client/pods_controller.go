/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package client

import (
	"context"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	metricsclientset "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/lavluda/k8s-pods-viewer/pkg/model"
)

type PodsController struct {
	kubeClient    *kubernetes.Clientset
	metricsClient *metricsclientset.Clientset
	uiModel       *model.PodsUIModel
	nodeSelector  labels.Selector
	podSelector   labels.Selector
	namespace     string
}

func NewPodsController(kubeClient *kubernetes.Clientset, metricsClient *metricsclientset.Clientset, uiModel *model.PodsUIModel, nodeSelector labels.Selector, podSelector labels.Selector, namespace string) *PodsController {
	return &PodsController{
		kubeClient:    kubeClient,
		metricsClient: metricsClient,
		uiModel:       uiModel,
		nodeSelector:  nodeSelector,
		podSelector:   podSelector,
		namespace:     namespace,
	}
}

func (m PodsController) Start(ctx context.Context) {
	cluster := m.uiModel.Cluster()
	m.startPodWatch(ctx, cluster)
	m.startNodeWatch(ctx, cluster)
	m.startPodMetricsPoller(ctx, cluster)
}

func (m PodsController) startNodeWatch(ctx context.Context, cluster *model.Cluster) {
	nodeWatchList := cache.NewFilteredListWatchFromClient(m.kubeClient.CoreV1().RESTClient(), "nodes",
		v1.NamespaceAll, func(options *metav1.ListOptions) {
			options.LabelSelector = m.nodeSelector.String()
		})
	_, nodeController := cache.NewInformer(
		nodeWatchList,
		&v1.Node{},
		time.Second*0,
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				node := model.NewNode(obj.(*v1.Node))
				n := cluster.AddNode(node)
				n.Show()
			},
			DeleteFunc: func(obj interface{}) {
				n := unwrapDeletedObject(obj).(*v1.Node)
				cluster.DeleteNode(nodeKey(n))
			},
			UpdateFunc: func(_, newObj interface{}) {
				n := newObj.(*v1.Node)
				if !n.DeletionTimestamp.IsZero() && len(n.Finalizers) == 0 {
					cluster.DeleteNode(nodeKey(n))
				} else {
					node, ok := cluster.GetNode(nodeKey(n))
					if ok {
						node.Update(n)
						node.Show()
					}
				}
			},
		},
	)
	go nodeController.Run(ctx.Done())
}

func (m PodsController) startPodWatch(ctx context.Context, cluster *model.Cluster) {
	namespace := m.namespace
	if namespace == "" {
		namespace = v1.NamespaceAll
	}

	podWatchList := cache.NewFilteredListWatchFromClient(m.kubeClient.CoreV1().RESTClient(), "pods",
		namespace, func(options *metav1.ListOptions) {
			options.LabelSelector = m.podSelector.String()
			options.FieldSelector = fields.Everything().String()
		})

	_, podController := cache.NewInformer(
		podWatchList,
		&v1.Pod{},
		time.Second*0,
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				p := obj.(*v1.Pod)
				if !isTerminalPod(p) {
					cluster.AddPod(model.NewPod(p))
				}
			},
			DeleteFunc: func(obj interface{}) {
				p := unwrapDeletedObject(obj).(*v1.Pod)
				cluster.DeletePod(p.Namespace, p.Name)
			},
			UpdateFunc: func(_, newObj interface{}) {
				p := newObj.(*v1.Pod)
				if isTerminalPod(p) {
					cluster.DeletePod(p.Namespace, p.Name)
				} else {
					pod, ok := cluster.GetPod(p.Namespace, p.Name)
					if !ok {
						cluster.AddPod(model.NewPod(p))
					} else {
						pod.Update(p)
						cluster.AddPod(pod)
					}
				}
			},
		},
	)
	go podController.Run(ctx.Done())
}

func (m PodsController) startPodMetricsPoller(ctx context.Context, cluster *model.Cluster) {
	if m.metricsClient == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		warned := false
		for {
			if err := m.refreshPodMetrics(ctx, cluster); err != nil {
				if !warned {
					m.uiModel.SetTransientStatus("Pod metrics unavailable; retrying without live usage data...", 10*time.Second)
					warned = true
				}
			} else if warned {
				m.uiModel.SetTransientStatus("Pod metrics reconnected.", 5*time.Second)
				warned = false
			}

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (m PodsController) refreshPodMetrics(ctx context.Context, cluster *model.Cluster) error {
	namespace := m.namespace
	if namespace == "" {
		namespace = v1.NamespaceAll
	}

	metricsList, err := m.metricsClient.MetricsV1beta1().PodMetricses(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: m.podSelector.String(),
	})
	if err != nil {
		return err
	}

	cluster.ForEachPod(func(p *model.Pod) {
		p.ClearUsage()
	})

	for _, pm := range metricsList.Items {
		pod, ok := cluster.GetPod(pm.Namespace, pm.Name)
		if !ok {
			continue
		}
		usage := v1.ResourceList{}
		for _, c := range pm.Containers {
			for rn, q := range c.Usage {
				existing := usage[rn]
				existing.Add(q)
				usage[rn] = existing
			}
		}
		// Ensure missing resources render as zero rather than stale values.
		if _, ok := usage[v1.ResourceCPU]; !ok {
			usage[v1.ResourceCPU] = resource.MustParse("0")
		}
		if _, ok := usage[v1.ResourceMemory]; !ok {
			usage[v1.ResourceMemory] = resource.MustParse("0")
		}
		pod.SetUsage(usage)
	}
	return nil
}

func nodeKey(node *v1.Node) string {
	if id := node.Spec.ProviderID; id != "" {
		return id
	}
	return "name://" + node.Name
}

func isTerminalPod(p *v1.Pod) bool {
	if !p.DeletionTimestamp.IsZero() {
		return true
	}
	switch p.Status.Phase {
	case v1.PodSucceeded, v1.PodFailed:
		return true
	}
	return false
}

func unwrapDeletedObject(obj interface{}) interface{} {
	if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		return d.Obj
	}
	return obj
}
