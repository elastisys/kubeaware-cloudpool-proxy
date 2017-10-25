package kube

import (
	"bytes"
	"fmt"
	"math"

	"github.com/golang/glog"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	apiv1 "k8s.io/client-go/pkg/api/v1"
)

const (
	// ResourceTypeCPU denotes CPU capacity
	ResourceTypeCPU = "cpu"
	// ResourceTypeCPU denotes memory capacity
	ResourceTypeMem = "memory"
)

// NoSchedulableNodesError denotes a scheduling that failed due to
// no node destinations being available (the set of schedulable nodes
// is empty).
type NoSchedulableNodesError struct {
}

func (e *NoSchedulableNodesError) Error() string {
	return "there are no schedulable nodes available"
}

// ClusterCapacityError is used as a scheduling error that denotes a situation
// where a set of nodes cannot fit on available nodes due to their total request
// for a given resource exceeds free capacity.
type ClusterCapacityError struct {
	InsufficientResource InsufficientResourceError
}

func (e *ClusterCapacityError) Error() string {
	return fmt.Sprintf("insufficient room on cluster nodes: %s", e.InsufficientResource.Error())
}

// InsufficientResourceError denotes a shortage of a certain type of system resource.
type InsufficientResourceError struct {
	Type   string
	Wanted *resource.Quantity
	Free   *resource.Quantity
}

func (e *InsufficientResourceError) Error() string {
	return fmt.Sprintf("insufficient %s: wanted: %s, free: %s", e.Type, e.Wanted, e.Free)
}

// TaintTolerationError denotes a scheduling that failed due to a pod not
// tolerating a certain node taint.
type TaintTolerationError struct {
	PodName string
	Node    string
	Taint   apiv1.Taint
}

func (e *TaintTolerationError) Error() string {
	return fmt.Sprintf("pod %s does not tolerate node %s taint: %s=%s:%s",
		e.PodName, e.Node, e.Taint.Key, e.Taint.Value, e.Taint.Effect)
}

// NodeSelectorMismatchError denotes a scheduling that failed due to a pod
// having a nodeSelector that did not match a given node.
type NodeSelectorMismatchError struct {
	Pod  *apiv1.Pod
	Node *apiv1.Node
}

func (e *NodeSelectorMismatchError) Error() string {
	return fmt.Sprintf("pod %s nodeSelector does not match node %s labels: %s does not match %s",
		e.Pod.Name, e.Node.Name, e.Pod.Spec.NodeSelector, e.Node.ObjectMeta.Labels)
}

// NodeAffinityMismatchError denotes a scheduling that failed due to a pod
// having a node affinity constraints that did not match a given node.
type NodeAffinityMismatchError struct {
	Pod  *apiv1.Pod
	Node *apiv1.Node
}

func (e *NodeAffinityMismatchError) Error() string {
	podNodeAffinityRequirements := e.Pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	return fmt.Sprintf("pod %s node affinity does not match node %s labels: %v does not match %s",
		e.Pod.Name, e.Node.Name, podNodeAffinityRequirements, e.Node.ObjectMeta.Labels)
}

// NodeMismatchError describes why a certain pod cannot be scheduled onto a certain node.
type NodeMismatchError struct {
	PodName string
	Node    string
	Reason  error
}

func (e *NodeMismatchError) Error() string {
	return fmt.Sprintf("pod %s could not be scheduled onto node %s: %s", e.PodName, e.Node, e.Reason.Error())
}

// PodSchedulingError describes what prevented a given pod from being scheduled
// onto any of the schedulable nodes.
type PodSchedulingError struct {
	// PodName is the name of the pod that could not be scheduled.
	PodName string
	// NodeMismatches describes, for each schedulable node, why
	// the pod could not be scheduled onto that node.
	NodeMismatches map[string]NodeMismatchError
}

func (e *PodSchedulingError) Error() string {
	buf := bytes.NewBufferString("")
	fmt.Fprintf(buf, "failed to reschedule pod %s:\n", e.PodName)
	for node, mismatch := range e.NodeMismatches {
		fmt.Fprintf(buf, "  %s: %s\n", node, mismatch.Error())
	}
	return buf.String()
}

// PodPlacement represents a suggested pod-to-node mapping suggested by the Schedule() method-
type PodPlacement struct {
	Pod  string
	Node string
}

func (p PodPlacement) String() string {
	return fmt.Sprintf("%s -> %s", p.Pod, p.Node)
}

// NodeLoadFunction describes a function that can determine the
// load of a node as a float value where a high value represents a
// highly utilized node.
type NodeLoadFunction func(*NodeInfo) float64

// SumOfAllocatedMemAndCPUFractions calculates node load as the sum of allocated
// memory and CPU fractions.
func SumOfAllocatedMemAndCPUFractions(node *NodeInfo) float64 {
	allocatableCPU := node.Status.Allocatable.Cpu().MilliValue()
	freeCPU := node.FreeCPU()
	allocatedCPU := allocatableCPU - freeCPU.MilliValue()
	cpuFraction := float64(allocatedCPU) / float64(allocatableCPU)

	allocatableMem := node.Status.Allocatable.Memory().Value()
	freeMem := node.FreeMem()
	allocatedMem := allocatableMem - freeMem.Value()
	memFraction := float64(allocatedMem) / float64(allocatableMem)

	glog.V(8).Infof("%s cpu load: %f", node.Name, cpuFraction)
	glog.V(8).Infof("%s mem load: %f", node.Name, memFraction)
	return cpuFraction + memFraction
}

// QuasiPodScheduler is a simplified Kubernetes scheduler which can do
// simulated pod scheduling over a set of schedulable nodes.
type QuasiPodScheduler struct {
	// Nodes are the schedulable nodes that the QuasiPodScheduler considers for
	// placing pods when Schedule() is called.
	Nodes []*NodeInfo
	// NodeLoadFunc is used to calculate a load number for a node when
	// NodeLoad() is called.
	NodeLoadFunc NodeLoadFunction
}

// NewQuasiScheduler creates a new QuasiScheduler with the given set of nodes
// and load function. loadFunc can be nil, in which case a default load function
// will be used (calculating node load as the sum of allocated memory and CPU fractions).
func NewQuasiScheduler(nodes []*NodeInfo, loadFunc NodeLoadFunction) *QuasiPodScheduler {
	var schedulableNodes []*NodeInfo
	for _, node := range nodes {

		nodeCopy := NodeInfo{Node: node.Node}
		for _, pod := range node.Pods {
			nodeCopy.Pods = append(nodeCopy.Pods, pod)
		}
		schedulableNodes = append(schedulableNodes, &nodeCopy)
	}

	if loadFunc == nil {
		loadFunc = SumOfAllocatedMemAndCPUFractions
	}

	return &QuasiPodScheduler{Nodes: schedulableNodes, NodeLoadFunc: loadFunc}
}

// FreeCPU calculates the total amount of free CPU available over all schedulable
// nodes that the QuasiPodScheduler was configured with.
func (s *QuasiPodScheduler) FreeCPU() *resource.Quantity {
	var totalFreeCPU resource.Quantity
	for _, node := range s.Nodes {
		totalFreeCPU.Add(node.FreeCPU())
	}
	return &totalFreeCPU
}

// FreeMem calculates the total amount of free memory available over all schedulable
// nodes that the QuasiPodScheduler was configured with.
func (s *QuasiPodScheduler) FreeMem() *resource.Quantity {
	var totalFreeMem resource.Quantity
	for _, node := range s.Nodes {
		totalFreeMem.Add(node.FreeMem())
	}
	return &totalFreeMem
}

// NodeLoad determines the load on a single node, as determined by the
// load function that was passed to the QuasiPodScheduler on creation.
func (s *QuasiPodScheduler) NodeLoad(node *NodeInfo) float64 {
	glog.V(8).Infof("calculating load for node %s", node)
	return s.NodeLoadFunc(node)
}

// Schedule attempts to schedule a pod onto one of the schedulable nodes that
// the QuasiPodScheduler has been configured to use. It strives to be a simplified, yet
// realistic emulation of the actual Kubernetes scheduler [1].
// It honors node capacity and pod resource requests, and makes sure that pods are not
// scheduled onto nodes with taints for which the pod does not have a corresponding
// toleration. On success, a PodPlacement is returned, which indicates which node the
// pod was assigned to. On failure, a scheduling error is returned, indicating why
// the pod could not be scheduled onto a node.
// [1] https://github.com/kubernetes/community/blob/8decfe42b8cc1e027da290c4e98fa75b3e98e2cc/contributors/devel/scheduler.md
func (s *QuasiPodScheduler) Schedule(podToSchedule apiv1.Pod) (*PodPlacement, error) {

	if len(s.Nodes) == 0 {
		return nil, &NoSchedulableNodesError{}
	}

	nodePredicates := []acceptablePodDestination{
		podFitsOnNode, podToleratesNodeTaints, podNodeSelectorPermitsNode, podNodeAffinityPermitsNode,
	}
	candidateNodes, rejections := schedulingCandidates(&podToSchedule, s.Nodes, nodePredicates)
	if len(candidateNodes) == 0 {
		return nil, rejections
	}

	// select least loaded node for node to be placed on
	assignedNode := s.leastLoadedNode(candidateNodes)

	// assign pod to node and reserve capacity on node
	assignedNode.Pods = append(assignedNode.Pods, podToSchedule)
	return &PodPlacement{Node: assignedNode.Name, Pod: podToSchedule.Name}, nil
}

// VerifySchedulingViable runs a checks if the total free memory and CPU on the
// schedulable cluster nodes is sufficient to fit a collection of pods. Note that
// even if this method suggests that scheduling is viable, there is no guarantee
// that the group of pods actually do fit the nodes. This cannot be determined
// until Schedule() is used to try and find a spot for each pod. This method
// can be used as a quick check to quickly determine if scheduling of a group
// of pods is at all feasible to attempt.
func (s *QuasiPodScheduler) VerifySchedulingViable(podsToEvacuate []apiv1.Pod) error {
	neededCPU := totalCPURequests(podsToEvacuate)
	glog.V(1).Infof("needed CPU to evacuate: %s", neededCPU.String())
	freeCPU := s.FreeCPU()
	glog.V(1).Infof("total free CPU on remaining nodes: %s", freeCPU.String())
	if neededCPU.Cmp(*freeCPU) > 0 {
		return &ClusterCapacityError{InsufficientResourceError{Type: ResourceTypeCPU, Wanted: neededCPU, Free: freeCPU}}
	}

	neededMem := totalMemRequests(podsToEvacuate)
	glog.V(1).Infof("needed memory to evacuate: %s", neededMem.String())
	freeMem := s.FreeMem()
	glog.V(1).Infof("total free memory on remaining nodes: %s", freeMem.String())
	if neededMem.Cmp(*freeMem) > 0 {
		return &ClusterCapacityError{InsufficientResourceError{Type: ResourceTypeMem, Wanted: neededMem, Free: freeMem}}
	}

	return nil
}

// acceptablePodDestination is a predicate that returns true if a given pod can be
// assigned to a given node.
type acceptablePodDestination func(pod *apiv1.Pod, node *NodeInfo) (bool, error)

func schedulingCandidates(pod *apiv1.Pod, nodes []*NodeInfo, predicates []acceptablePodDestination) (candidates []*NodeInfo, rejections *PodSchedulingError) {
	rejections = &PodSchedulingError{
		PodName:        pod.Name,
		NodeMismatches: make(map[string]NodeMismatchError, 0),
	}

	for _, node := range nodes {
		satisfiesAllPredicates := true
		for _, predicate := range predicates {
			if ok, err := predicate(pod, node); !ok {
				mismatch := NodeMismatchError{PodName: pod.Name, Node: node.Name, Reason: err}
				rejections.NodeMismatches[node.Name] = mismatch
				satisfiesAllPredicates = false
				break
			}
		}

		if satisfiesAllPredicates {
			candidates = append(candidates, node)
		}
	}
	return candidates, rejections
}

// podFitsOnNode returns true if a given pod fits on a given node, based on the
// pods resource requirements and the free resources of the node.
func podFitsOnNode(pod *apiv1.Pod, node *NodeInfo) (bool, error) {
	requestedCPU, requestedMem := podSize(pod)
	freeCPU := node.FreeCPU()
	if requestedCPU.Cmp(freeCPU) > 0 {
		return false, &InsufficientResourceError{Type: ResourceTypeCPU, Wanted: requestedCPU, Free: &freeCPU}
	}
	freeMem := node.FreeMem()
	if requestedMem.Cmp(freeMem) > 0 {
		return false, &InsufficientResourceError{Type: ResourceTypeMem, Wanted: requestedMem, Free: &freeMem}
	}
	return true, nil
}

// podNodeSelectorPermitsNode returns true if a given pod's nodeSelector terms
// (if any) match the labels of the given node.
func podNodeSelectorPermitsNode(pod *apiv1.Pod, node *NodeInfo) (bool, error) {
	selector := labels.SelectorFromSet(pod.Spec.NodeSelector)
	if !selector.Matches(labels.Set(node.Labels)) {
		return false, &NodeSelectorMismatchError{Pod: pod, Node: &node.Node}
	}
	return true, nil
}

// podToleratesNodeTaints returns true if a given pod tolerates all of a node's taints
// (and therefore isn't prevented from being scheduled onto that node).
func podToleratesNodeTaints(pod *apiv1.Pod, node *NodeInfo) (bool, error) {
	for _, taint := range node.Spec.Taints {
		if taint.Effect != apiv1.TaintEffectNoSchedule && taint.Effect != apiv1.TaintEffectNoExecute {
			continue
		}
		if !hasToleration(pod, taint) {
			return false, &TaintTolerationError{PodName: pod.Name, Node: node.Name, Taint: taint}
		}

	}
	return true, nil
}

// hasToleration returns true if the given pod has a toleration for the given node taint.
func hasToleration(pod *apiv1.Pod, taint apiv1.Taint) bool {
	for _, toleration := range pod.Spec.Tolerations {
		if toleration.ToleratesTaint(&taint) {
			return true
		}
	}
	return false
}

// podNodeAffinityPermitsNode returns true if the pod's node affinity constraints (if any)
// permits the given node. Only hard node affinity requirements are considered
// (requiredDuringSchedulingIgnoredDuringExecution)
func podNodeAffinityPermitsNode(pod *apiv1.Pod, node *NodeInfo) (bool, error) {
	affinity := pod.Spec.Affinity

	if affinity != nil && affinity.NodeAffinity != nil &&
		affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
		hardAffinityConstraints := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
		for _, nodeSelectorTerm := range hardAffinityConstraints.NodeSelectorTerms {
			nodeSelector, err := nodeSelectorRequirementsAsSelector(nodeSelectorTerm)
			if err != nil {
				return false, fmt.Errorf("failed to parse MatchExpressions: %+v: %s",
					nodeSelectorTerm.MatchExpressions, err)
			}

			if !nodeSelector.Matches(labels.Set(node.Labels)) {
				return false, &NodeAffinityMismatchError{Pod: pod, Node: &node.Node}
			}
		}
	}

	return true, nil
}

// nodeSelectorRequirementsAsSelector converts a NodeSelectorTerm into a labels.Selector.
// Stolen with pride from https://github.com/kubernetes/kubernetes/blob/master/pkg/api/v1/helper/helpers.go#L227-L259
func nodeSelectorRequirementsAsSelector(nodeSelectorTerm apiv1.NodeSelectorTerm) (labels.Selector, error) {
	if len(nodeSelectorTerm.MatchExpressions) == 0 {
		return labels.Nothing(), nil
	}
	selector := labels.NewSelector()
	for _, expr := range nodeSelectorTerm.MatchExpressions {
		var op selection.Operator
		switch expr.Operator {
		case apiv1.NodeSelectorOpIn:
			op = selection.In
		case apiv1.NodeSelectorOpNotIn:
			op = selection.NotIn
		case apiv1.NodeSelectorOpExists:
			op = selection.Exists
		case apiv1.NodeSelectorOpDoesNotExist:
			op = selection.DoesNotExist
		case apiv1.NodeSelectorOpGt:
			op = selection.GreaterThan
		case apiv1.NodeSelectorOpLt:
			op = selection.LessThan
		default:
			return nil, fmt.Errorf("%q is not a valid node selector operator", expr.Operator)
		}
		r, err := labels.NewRequirement(expr.Key, op, expr.Values)
		if err != nil {
			return nil, err
		}
		selector = selector.Add(*r)
	}
	return selector, nil
}

// leastLoadedNode returns, from a list of nodes, the node that is least
// loaded, where the load of a node is calculated by the configured load function.
func (s *QuasiPodScheduler) leastLoadedNode(nodes []*NodeInfo) (leastLoaded *NodeInfo) {
	minLoad := math.MaxFloat64
	for _, node := range nodes {
		load := s.NodeLoadFunc(node)
		if load < minLoad {
			leastLoaded = node
			minLoad = load
		}
	}
	return leastLoaded
}

// podSize calculates the sum of all CPU and memory requested by containers of a pod
func podSize(pod *apiv1.Pod) (requestedCPU, requestedMem *resource.Quantity) {
	requestedCPU = &resource.Quantity{}
	requestedMem = &resource.Quantity{}

	for _, containers := range pod.Spec.Containers {
		requestedCPU.Add(containers.Resources.Requests["cpu"])
		requestedMem.Add(containers.Resources.Requests["memory"])
	}
	return requestedCPU, requestedMem
}

func totalCPURequests(pods []apiv1.Pod) *resource.Quantity {
	totalCPU := &resource.Quantity{}
	for _, pod := range pods {
		for _, container := range pod.Spec.Containers {
			requestedCPU := container.Resources.Requests["cpu"]
			totalCPU.Add(requestedCPU)
		}
	}
	return totalCPU
}

func totalMemRequests(pods []apiv1.Pod) *resource.Quantity {
	totalMem := &resource.Quantity{}
	for _, pod := range pods {
		for _, container := range pod.Spec.Containers {
			requestedMem := container.Resources.Requests["memory"]
			totalMem.Add(requestedMem)
		}
	}
	return totalMem
}
