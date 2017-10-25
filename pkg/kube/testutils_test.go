package kube

import (
	"fmt"
	"log"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiv1 "k8s.io/client-go/pkg/api/v1"
	policy "k8s.io/client-go/pkg/apis/policy/v1beta1"
)

//
// NOTE: this file does not contain tests themselves, but rather common
//       utility functions used throughout the other package tests.
//

type TestPod apiv1.Pod

// NewTestPod creates a new TestPod with given name and namespace.
func NewTestPod(namespace string, name string) *TestPod {
	pod := apiv1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			SelfLink:  fmt.Sprintf("/api/v1/namespaces/default/pods/%s", name),
			Labels:    make(map[string]string, 0),
		},
		Spec: apiv1.PodSpec{
			Containers: []apiv1.Container{
				{
					Resources: apiv1.ResourceRequirements{
						Requests: apiv1.ResourceList{},
					},
				},
			},
			NodeSelector: map[string]string{},
		},
	}

	p := TestPod(pod)
	return &p
}

// CPU sets a CPU resource request on the pod.
func (p *TestPod) CPU(requestedCPU string) *TestPod {
	if requestedCPU != "" {
		p.Spec.Containers[0].Resources.Requests[apiv1.ResourceCPU] = *parseQuantity(requestedCPU)
	}
	return p
}

// Mem sets a memory resource request on the pod.
func (p *TestPod) Mem(requestedMem string) *TestPod {
	if requestedMem != "" {
		p.Spec.Containers[0].Resources.Requests[apiv1.ResourceMemory] = *parseQuantity(requestedMem)
	}
	return p
}

// Status sets the pod status of the pod.
func (p *TestPod) SetStatus(phase apiv1.PodPhase) *TestPod {
	p.Status = apiv1.PodStatus{Phase: phase}
	return p
}

// Sets a ReplicaSet controller (with a given name) for the pod.
func (p *TestPod) Controller(controller string) *TestPod {
	podHasController := true
	p.ObjectMeta.OwnerReferences = append(p.ObjectMeta.OwnerReferences,
		metav1.OwnerReference{Controller: &podHasController, Kind: "ReplicaSet", Name: controller},
	)
	return p
}

// HostPathVolume adds a hostPath volume to the pod
func (p *TestPod) HostPathVolume(hostPath string) *TestPod {
	p.Spec.Volumes = append(
		p.Spec.Volumes,
		apiv1.Volume{
			Name: "vol",
			VolumeSource: apiv1.VolumeSource{
				HostPath: &apiv1.HostPathVolumeSource{Path: hostPath}}},
	)

	return p
}

// AddToleration adds a node taint toleration to the pod.
func (p *TestPod) AddToleration(toleration apiv1.Toleration) *TestPod {
	p.Spec.Tolerations = append(p.Spec.Tolerations, toleration)
	return p
}

// AddNodeSelector adds a nodeSelector to the pod.
func (p *TestPod) AddNodeSelector(labelKey, labelValue string) *TestPod {
	p.Spec.NodeSelector[labelKey] = labelValue
	return p
}

// Label adds a label to the pod.
func (p *TestPod) Label(labelKey, labelValue string) *TestPod {
	p.ObjectMeta.Labels[labelKey] = labelValue
	return p
}

// NodeAffinityRequirement sets a hard node affinity policy to the pod.
func (p *TestPod) NodeAffinityRequirement(req apiv1.NodeSelectorRequirement) *TestPod {

	p.Spec.Affinity = &apiv1.Affinity{
		NodeAffinity: &apiv1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &apiv1.NodeSelector{
				NodeSelectorTerms: []apiv1.NodeSelectorTerm{
					apiv1.NodeSelectorTerm{
						MatchExpressions: []apiv1.NodeSelectorRequirement{req},
					},
				},
			},
		},
	}

	return p
}

// ToPod converts a TestPod to an apiv1.Pod.
func (p *TestPod) ToPod() *apiv1.Pod {
	pod := apiv1.Pod(*p)
	return &pod
}

// Parses a quantity such as '100m', '10Gi', '1Ki'
func parseQuantity(quantityExpression string) *resource.Quantity {
	quant, err := resource.ParseQuantity(quantityExpression)
	if err != nil {
		log.Fatalf("aborting test: could not parse quantity '%s': %s\n", quantityExpression, err)
	}
	return &quant
}

type TestNode apiv1.Node

func NewTestNode(name string) *TestNode {
	node := apiv1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			SelfLink:    fmt.Sprintf("/api/v1/nodes/%s", name),
			Labels:      map[string]string{},
			Annotations: map[string]string{},
		},
		Spec: apiv1.NodeSpec{
			ProviderID: name,
			ExternalID: name,
		},
		Status: apiv1.NodeStatus{
			Capacity: apiv1.ResourceList{},
		},
	}
	node.Status.Allocatable = apiv1.ResourceList{}

	t := TestNode(node)
	t.CPU("0m").Mem("0M")
	return &t
}

// CPU sets the allocatable CPU of the node (e.g. "1000m").
func (n *TestNode) CPU(capacityCPU string) *TestNode {
	cpu := *parseQuantity(capacityCPU)
	n.Status.Capacity[apiv1.ResourceCPU] = cpu
	n.Status.Allocatable[apiv1.ResourceCPU] = cpu
	return n
}

// Mem sets the allocatable memory of the node (e.g. "1000M").
func (n *TestNode) Mem(capacityMem string) *TestNode {
	mem := *parseQuantity(capacityMem)
	n.Status.Capacity[apiv1.ResourceMemory] = mem
	n.Status.Allocatable[apiv1.ResourceMemory] = mem
	return n
}

// Schedulable updates the Unschedulable field of the node.
func (n *TestNode) Schedulable(schedulable bool) *TestNode {
	n.Spec.Unschedulable = !schedulable
	return n
}

// Ready updates the nodes ready status.
func (n *TestNode) Ready(ready bool) *TestNode {
	conditionStatus := apiv1.ConditionFalse
	if ready {
		conditionStatus = apiv1.ConditionTrue
	}

	for _, condition := range n.Status.Conditions {
		if condition.Type == apiv1.NodeReady {
			condition.Status = conditionStatus
			return n
		}
	}

	// no ready condition existed, add one
	n.Status.Conditions = append(n.Status.Conditions,
		apiv1.NodeCondition{Type: apiv1.NodeReady, Status: conditionStatus})

	return n
}

// Annotation sets a node annotation
func (n *TestNode) Annotation(key, value string) *TestNode {
	n.ObjectMeta.Annotations[key] = value
	return n
}

// Taint sets a "NoSchedule" taint with given key and value on the node.
func (n *TestNode) Taint(key, value string) *TestNode {
	n.Spec.Taints = append(
		n.Spec.Taints,
		apiv1.Taint{Key: key, Value: value, Effect: apiv1.TaintEffectNoSchedule},
	)
	return n
}

// Label sets a given label on the node.
func (n *TestNode) Label(key, value string) *TestNode {
	n.ObjectMeta.Labels[key] = value
	return n
}

// ToNode converts a TestNode to an api Node.
func (n *TestNode) ToNode() *apiv1.Node {
	node := apiv1.Node(*n)
	return &node
}

// FakeCluster represents a fake Kubernetes cluster
type FakeCluster struct {
	// node name -> Node
	Nodes map[string]*apiv1.Node
	// node name -> Pods
	Pods map[string][]apiv1.Pod

	PodDisruptionBudgetList *policy.PodDisruptionBudgetList
}

func NewFakeCluster() *FakeCluster {
	return &FakeCluster{
		Nodes: map[string]*apiv1.Node{},
		Pods:  map[string][]apiv1.Pod{},
		PodDisruptionBudgetList: &policy.PodDisruptionBudgetList{}}
}

// AddNodes adds a collection of nodes to the cluster.
func (c *FakeCluster) AddNodes(nodes ...*apiv1.Node) *FakeCluster {
	for _, node := range nodes {
		c.Nodes[node.Name] = node
	}
	return c
}

// AddPod adds a pod to the given cluster node.
func (c *FakeCluster) AddPod(nodeName string, pod *apiv1.Pod) *FakeCluster {
	pod.Spec.NodeName = nodeName

	c.Pods[nodeName] = append(c.Pods[nodeName], *pod)
	return c
}

// AddPodDisruptionBudget adds a pod disruption budget to the cluster.
func (c *FakeCluster) AddPodDisruptionBudget(disruptionBudget policy.PodDisruptionBudget) *FakeCluster {
	c.PodDisruptionBudgetList.Items = append(c.PodDisruptionBudgetList.Items, disruptionBudget)
	return c
}

// buildNodeInfo creates a NodeInfo from a given node, with a given
// set of pods scheduled.
func buildNodeInfo(node *apiv1.Node, pods ...*apiv1.Pod) *NodeInfo {
	var nodePods []apiv1.Pod
	for _, pod := range pods {
		nodePods = append(nodePods, *pod)
	}

	return &NodeInfo{Node: *node, Pods: nodePods}
}

// buildPod creates a "Running" pod with specified resource requests.
func buildPod(name string, requestedCPU string, requestedMem string) *apiv1.Pod {
	return buildPodNS("default", name, requestedCPU, requestedMem)
}

// buildPodNS creates a "Running" pod in a given namespace with specified resource requests.
func buildPodNS(namespace string, name string, requestedCPU string, requestedMem string) *apiv1.Pod {
	return NewTestPod(namespace, name).CPU(requestedCPU).Mem(requestedMem).SetStatus(apiv1.PodRunning).ToPod()
}
