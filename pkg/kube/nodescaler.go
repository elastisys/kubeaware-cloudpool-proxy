package kube

import (
	"fmt"
	"time"

	"github.com/golang/glog"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"

	apiv1 "k8s.io/client-go/pkg/api/v1"
	policy "k8s.io/client-go/pkg/apis/policy/v1beta1"
)

const (
	// KubeSystemNS is the kube-system namespace.
	KubeSystemNS = "kube-system"
	// ComponentLabelKey is the node label key used for component
	ComponentLabelKey = "component"
	// APIServerLabelValue is the node label value used for the apiserver component
	APIServerLabelValue = "kube-apiserver"
	// NoScaleDownNodeAnnotation is the node annotation used to protect a node from
	// being scaled down.
	NoScaleDownNodeAnnotation = "cluster-autoscaler.kubernetes.io/scale-down-disabled"

	// NodeAboutToBeDrainedTaint is a taint that is placed on nodes prior to draining them to
	// prevent new pods from being scheduled onto the node during the drain.
	NodeAboutToBeDrainedTaint = "node-about-to-be-drained"
)

// NodeScaler is an entity capable of scaling down Kubernetes worker nodes in a graceful manner.
type NodeScaler interface {
	// GetNode returns a particular node in the cluster or nil if no such node exists.
	GetNode(name string) (*NodeInfo, error)
	// ListNodes returns all nodes (masters and workers) currently in the Kubernetes cluster.
	ListNodes() ([]*NodeInfo, error)
	// WorkerNodes returns all worker nodes currently in the Kubernetes cluster.
	ListWorkerNodes() ([]*NodeInfo, error)
	// IsScaleDownCancidate determines if a particular node is (reasonably) safe to scale down.
	IsScaleDownCandidate(node *NodeInfo) (bool, error)
	// DeleteNode deletes a node from the Kubernetes cluster, akin to `kubectl delete node`.
	DeleteNode(node *NodeInfo) error
	// NodeLoad determines the load on a given node (according to some definition of
	// load that the NodeScaler has been configured to use). This value can be used to
	// determine which node is "cheapest" to scale down (requiring fewest migrations).
	// A higher value represents a higher load on the node.
	NodeLoad(node *NodeInfo) float64
	// DrainNode evacutates a node by moving all pods hosted on the node to other nodes.
	// During this operation, and after (on success), the node is marked as unschedulable
	// to prevent new pods from being scheduled onto it. At that point the machine that the
	// node is running on is safe to be terminated. Should the operation fail part-way through
	// the node will be marked schedulable again. On failure some pods may have been migrated
	// to other nodes, while some pods may remain on the node.
	DrainNode(victimNode *NodeInfo) error
}

// NodeScaleDownProtectedError is returned on IsScaleDownCandidate to indicate
// that the given node is protected via a scale-down protection annotation
type NodeScaleDownProtectedError struct {
	NodeName string
}

func (e *NodeScaleDownProtectedError) Error() string {
	return fmt.Sprintf("node %s is protected from scale-down via %s annotation", e.NodeName, NoScaleDownNodeAnnotation)
}

// PodWithoutControllerError is returned on IsScaleDownCandidate to indicate
// that the given node is prevented from being scaled down by having one or more
// pods without controller (deployment/replica set), which would not get recreated
// on node evacuation.
type PodWithoutControllerError struct {
	NodeName string
	PodName  string
}

func (e *PodWithoutControllerError) Error() string {
	return fmt.Sprintf("pod %s on node %s does not have a (replication) controller and cannot be evacuated (it won't get recreated)", e.PodName, e.NodeName)
}

// PodLocalStorageError is returned by IsScaleDownCandidate to indicate
// that the given node is prevented from being scaled down by having one or more
// pods with a host-local storage volume.
type PodLocalStorageError struct {
	NodeName string
	PodName  string
}

func (e *PodLocalStorageError) Error() string {
	return fmt.Sprintf("pod %s on node %s has a node-local storage volume and cannot be evacuated", e.PodName, e.NodeName)
}

// NodeEvacuationInfeasibleError is returned by IsScaleDownCandidate to indicate
// that the given node cannot be safely evacuated.
type NodeEvacuationInfeasibleError struct {
	NodeName string
	Reason   error
}

func (e *NodeEvacuationInfeasibleError) Error() string {
	return fmt.Sprintf("not all pods on node %s can be safely evacuated: %s", e.NodeName, e.Reason)
}

// PodDisruptionViolationError is returned by IsScaleDownCandidate to indicate
// that the given node cannot be safely evacuated due to violating a pod
// disruption budget.
type PodDisruptionViolationError struct {
	PodDisruptionBudget string
	RequiredDisruptions int32
	AllowedDisruptions  int32
}

func (e *PodDisruptionViolationError) Error() string {
	return fmt.Sprintf("pod disruption budget %s prevents node evacuation: "+
		"disruptions required: %d, disruptions allowed: %d",
		e.PodDisruptionBudget, e.RequiredDisruptions, e.AllowedDisruptions)
}

// NoEvacuationNodesAvailableError is returned by IsScaleDownCandidate to
// indicate a situation where a node cannot be scaled down since there are
// no available nodes to evacuate its pods to.
type NoEvacuationNodesAvailableError struct{}

func (e *NoEvacuationNodesAvailableError) Error() string {
	return fmt.Sprintf("no other schedulable nodes to evacuate to")
}

// DefaultNodeScaler is capable of gracefully scaling down nodes (by first evacuating the pods
// hosted on the node).
type DefaultNodeScaler struct {
	KubeClient KubeClient
}

// NewNodeScaler creates a new NodeScaler using a given kubernetes API server client.
func NewNodeScaler(kubeClient KubeClient) NodeScaler {
	return &DefaultNodeScaler{KubeClient: kubeClient}
}

// GetNode returns a particular node in the cluster or nil if no such node exists.
func (scaler *DefaultNodeScaler) GetNode(name string) (*NodeInfo, error) {
	return scaler.getNodeInfo(name)
}

// ListNodes returns all cluster nodes (including masters).
func (scaler *DefaultNodeScaler) ListNodes() ([]*NodeInfo, error) {
	nodes, err := scaler.getNodeInfos()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve nodes from kubernetes: %s", err)
	}
	return nodes, nil
}

// ListWorkerNodes returns all non-master nodes.
func (scaler *DefaultNodeScaler) ListWorkerNodes() ([]*NodeInfo, error) {
	nodes, err := scaler.ListNodes()
	if err != nil {
		return nil, err
	}

	nonMasterNodes := filterNodes(nodes, not(isMasterNode))
	return nonMasterNodes, nil
}

// IsScaleDownCandidate determines if a particular node is (reasonably) safe to scale down.
// If false, an error gives further explanation as to why the node was not deemed safe
// to scale down.
func (scaler *DefaultNodeScaler) IsScaleDownCandidate(node *NodeInfo) (bool, error) {
	// node cannot be marked with no scaledown
	if node.Annotations[NoScaleDownNodeAnnotation] == "true" {
		return false, &NodeScaleDownProtectedError{NodeName: node.Name}
	}

	// retrieve all nodes and their pods
	nodes, err := scaler.getNodeInfos()
	if err != nil {
		return false, err
	}

	// get the remaining nodes that are available as potential destination for evacuated pods. filter out
	// - master nodes
	// - unschedulable nodes
	// - victim node
	// - nodes that are not ready
	// - nodes that are unschedulable
	remainingWorkerNodes := filterNodes(nodes,
		not(isMasterNode), not(hasNodeName(node.Name)), isNodeReady, isNodeSchedulable)

	if len(remainingWorkerNodes) == 0 {
		return false, &NodeEvacuationInfeasibleError{
			NodeName: node.Name, Reason: &NoEvacuationNodesAvailableError{},
		}
	}
	glog.V(1).Infof("remaining worker nodes: %s", remainingWorkerNodes)

	// filter out kube-system pods from victim pods
	podsToEvacuate := filterPods(node.Pods, isNonSystemPod, isActivePod)
	glog.V(1).Infof("node %s hosts %d active (non-system) pods", node.Name, len(podsToEvacuate))
	for _, pod := range podsToEvacuate {
		glog.V(2).Infof("  pod: %s/%s", pod.Namespace, pod.Name)
	}

	// Check that pods *can* be moved to a different node and aren't required to run on this node
	for _, pod := range podsToEvacuate {
		if hasNodeLocalStorage(pod) {
			return false, &PodLocalStorageError{NodeName: node.Name, PodName: pod.Name}
		}
		if !hasController(pod) {
			return false, &PodWithoutControllerError{NodeName: node.Name, PodName: pod.Name}
		}
	}

	PDBs, err := scaler.KubeClient.PodDisruptionBudgets("")
	if err != nil {
		return false, fmt.Errorf("failed to list pod disruption budgets: %s", err)
	}

	if ok, err := scaler.podDisruptionBudgetsAllowEvacuation(PDBs.Items, podsToEvacuate); !ok {
		return false, &NodeEvacuationInfeasibleError{NodeName: node.Name, Reason: err}
	}

	// make a simplified pod placement, similar to the one done by the kubernetes scheduler,
	// to make an educated guess if evacuation and scale-down of this node is viable
	err = scaler.verifyEvacuationFeasibility(podsToEvacuate, remainingWorkerNodes)
	if err != nil {
		return false, &NodeEvacuationInfeasibleError{NodeName: node.Name, Reason: err}
	}

	return true, nil
}

// verifyEvacuationFeasibility makes an educated guess to indicate if
// a certain group pods can safely be scheduled onto a group of nodes. It does
// this by making a simplified pod scheduling, similar to the one done by the
// Kubernetes scheduler.
func (scaler *DefaultNodeScaler) verifyEvacuationFeasibility(
	podsToEvacuate []apiv1.Pod, schedulableNodes []*NodeInfo) error {

	scheduler := NewQuasiScheduler(schedulableNodes, nil)
	err := scheduler.VerifySchedulingViable(podsToEvacuate)
	if err != nil {
		return err
	}
	for _, podToEvacuate := range podsToEvacuate {
		placement, err := scheduler.Schedule(podToEvacuate)
		if err != nil {
			return err
		}
		glog.V(1).Infof("quasi-scheduling: %s", placement)
	}
	return nil
}

// DeleteNode deletes a node from the Kubernetes cluster, akin to `kubectl delete node`.
func (scaler *DefaultNodeScaler) DeleteNode(node *NodeInfo) error {
	err := scaler.KubeClient.DeleteNode(&node.Node)
	if err != nil {
		return fmt.Errorf("failed to delete node: %s", err)
	}
	return nil
}

// NodeLoad determines the load on a given node (according to some definition of
// load that the NodeScaler has been configured to use). This value can be used to
// determine which node is "cheapest" to scale down (requiring fewest migrations).
// A higher value represents a higher load on the node.
func (scaler *DefaultNodeScaler) NodeLoad(node *NodeInfo) float64 {
	// let the quasi scheduler determine the node load
	return NewQuasiScheduler([]*NodeInfo{}, nil).NodeLoad(node)
}

// DrainNode marks a node unschedulable (via a taint) and then drains the node by evicting all
// active, non-system pods scheduled onto the node.
// https://kubernetes.io/docs/tasks/administer-cluster/safely-drain-node/#the-eviction-api
func (scaler *DefaultNodeScaler) DrainNode(nodeToDrain *NodeInfo) error {
	err := scaler.markUnschedulable(nodeToDrain)
	if err != nil {
		return fmt.Errorf("DrainNode: failed to mark node unschedulable: %s", err)
	}

	// evict all active non-system pods on node
	podsToEvacuate := filterPods(nodeToDrain.Pods, isNonSystemPod, isActivePod)
	for _, podToEvacuate := range podsToEvacuate {
		glog.V(0).Infof("evicting %s/%s ...", podToEvacuate.Namespace, podToEvacuate.Name)
		gracePeriodSeconds := podToEvacuate.GetDeletionGracePeriodSeconds()
		eviction := &policy.Eviction{
			ObjectMeta: metav1.ObjectMeta{
				Name:      podToEvacuate.Name,
				Namespace: podToEvacuate.Namespace,
			},
			DeleteOptions: &metav1.DeleteOptions{
				GracePeriodSeconds: gracePeriodSeconds,
			},
		}

		evictionError := scaler.KubeClient.EvictPod(eviction)

		// if evacuation fails, we remove unschedulable mark
		defer func() {
			if evictionError != nil {
				glog.Infof("DrainNode: marking node schedulable again due to evacuation error: %s", evictionError)
				if err := scaler.markSchedulable(nodeToDrain); err != nil {
					glog.Errorf("DrainNode: failed to remove unschedulable taint after evacuation failure: %s", err)
				}
			}
		}()

		if evictionError != nil {
			return fmt.Errorf("DrainNode: failed to evacuate pod %s: %s", podToEvacuate.Name, evictionError)
		}

	}

	return nil
}

// markUnschedulable taints a node prior to draining it in order to make
// sure new pods are not scheduled onto it while draining.
func (scaler *DefaultNodeScaler) markUnschedulable(node *NodeInfo) error {
	// need a fresh snapshot of node
	freshNode, err := scaler.KubeClient.GetNode(node.Name)
	if err != nil {
		return fmt.Errorf("failed to refresh node metadata: %s", err)
	}

	taints := freshNode.Spec.Taints
	for _, taint := range taints {
		if taint.Key == NodeAboutToBeDrainedTaint {
			glog.V(1).Infof("node %s already has taint %s", freshNode.Name, NodeAboutToBeDrainedTaint)
			return nil
		}
	}

	glog.V(0).Infof("marking node %s unschedulable by setting taint %s", freshNode.Name, NodeAboutToBeDrainedTaint)
	unschedulableTaint := apiv1.Taint{
		Key:       NodeAboutToBeDrainedTaint,
		Value:     "true",
		Effect:    apiv1.TaintEffectNoSchedule,
		TimeAdded: metav1.Time{Time: time.Now()},
	}
	freshNode.Spec.Taints = append(freshNode.Spec.Taints, unschedulableTaint)

	err = scaler.KubeClient.UpdateNode(freshNode)
	if err != nil {
		return err
	}
	return nil
}

func (scaler *DefaultNodeScaler) markSchedulable(node *NodeInfo) error {
	// Refresh node metadata
	freshNode, err := scaler.KubeClient.GetNode(node.Name)
	if err != nil {
		return fmt.Errorf("markSchedulable: failed to get node: %s", err)
	}

	taints := freshNode.Spec.Taints
	taintsToKeep := make([]apiv1.Taint, 0)
	for _, taint := range taints {
		if taint.Key != NodeAboutToBeDrainedTaint {
			// keep taint
			taintsToKeep = append(taintsToKeep, taint)
		}
	}

	freshNode.Spec.Taints = taintsToKeep
	err = scaler.KubeClient.UpdateNode(freshNode)
	if err != nil {
		return fmt.Errorf("markSchedulable: failed to remove %s taint: %s", NodeAboutToBeDrainedTaint, err)
	}
	return nil
}

// podDisruptionBudgetsAllowEvacuation returns true if a given set of pods can be
// moved without violating a given set of disruption budgets
func (scaler *DefaultNodeScaler) podDisruptionBudgetsAllowEvacuation(PDBs []policy.PodDisruptionBudget, nodePods []apiv1.Pod) (bool, error) {
	for _, PDB := range PDBs {
		pdbLabelSelector, err := metav1.LabelSelectorAsSelector(PDB.Spec.Selector)
		if err != nil {
			return false, fmt.Errorf("cannot parse label selector on PDB %s: %s", PDB.Name, err)
		}

		var numPDBMatchingPods int32
		for _, pod := range nodePods {
			if pdbLabelSelector.Matches(labels.Set(pod.ObjectMeta.Labels)) {
				numPDBMatchingPods++
			}
		}
		if numPDBMatchingPods > PDB.Status.PodDisruptionsAllowed {
			return false, &PodDisruptionViolationError{
				PodDisruptionBudget: PDB.Name,
				RequiredDisruptions: numPDBMatchingPods,
				AllowedDisruptions:  PDB.Status.PodDisruptionsAllowed}
		}
	}
	return true, nil
}

func (scaler *DefaultNodeScaler) getNodeInfos() ([]*NodeInfo, error) {
	nodeList, err := scaler.KubeClient.ListNodes()
	if err != nil {
		return nil, err
	}

	var nodeInfos []*NodeInfo
	for _, node := range nodeList.Items {
		nodeInfo, err := scaler.getNodeInfo(node.Name)
		if err != nil {
			return nil, err
		}
		nodeInfos = append(nodeInfos, nodeInfo)
	}
	return nodeInfos, nil
}

func (scaler *DefaultNodeScaler) getNodeInfo(nodeName string) (*NodeInfo, error) {
	node, err := scaler.KubeClient.GetNode(nodeName)
	if err != nil {
		return nil, err
	}

	pods, err := scaler.getPodsOnNode(node)
	if err != nil {
		return nil, err
	}
	return &NodeInfo{*node, pods}, nil
}

func (scaler *DefaultNodeScaler) getPodsOnNode(node *apiv1.Node) ([]apiv1.Pod, error) {
	podList, err := scaler.KubeClient.ListPods(
		metav1.NamespaceAll,
		metav1.ListOptions{FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": node.Name}).String()})
	if err != nil {
		return nil, err
	}
	return podList.Items, nil
}

type nodePredicate func(*NodeInfo) bool

func not(predicate nodePredicate) nodePredicate {
	return func(node *NodeInfo) bool {
		return !predicate(node)
	}
}

// isMasterNode is a node predicate that returns true for master Nodes.
// There is no foolproof way of determining this but we use the following heuristics:
// - a master has a pod with label "component": "kube-apiserver" (in kubernetes 1.7+)
// - a master has a pod with name "kube-apiserver-<nodename>"
func isMasterNode(node *NodeInfo) bool {
	for _, pod := range node.Pods {
		if pod.Namespace != KubeSystemNS {
			continue
		}

		if val, ok := pod.Labels[ComponentLabelKey]; ok && (val == APIServerLabelValue) {
			glog.V(2).Infof("node %s considered a master due to %s=%s label",
				node.Name, ComponentLabelKey, APIServerLabelValue)
			return true
		}

		commonAPIServerPodName := "kube-apiserver-" + node.Name
		if pod.Name == commonAPIServerPodName {
			glog.V(2).Infof("node %s considered a master due to pod %s",
				node.Name, pod.Name)
			return true
		}
	}
	return false
}

// hasNodeName returns a node predicate that matches for any node that has
//  a particular name
func hasNodeName(name string) nodePredicate {
	return func(node *NodeInfo) bool {
		return node.Name == name
	}
}

// isNodeReady returns true if a given node is "Ready"
func isNodeReady(node *NodeInfo) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == apiv1.NodeReady && condition.Status == apiv1.ConditionTrue {
			return true
		}
	}
	glog.V(2).Infof("node %s is not ready", node.Name)
	return false
}

// isNodeSchedulable returns true for any node without the unschedulable field set
func isNodeSchedulable(node *NodeInfo) bool {
	schedulable := !node.Spec.Unschedulable
	if !schedulable {
		glog.V(2).Infof("node %s is unschedulable", node.Name)
	}
	return schedulable
}

// filterNodes takes a list of nodes and returns a filtered list containing only
// the nodes that satisfy all predicates.
func filterNodes(nodes []*NodeInfo, predicates ...nodePredicate) []*NodeInfo {
	var filtered []*NodeInfo
	for _, node := range nodes {
		satisfiesAllPredicates := true
		for _, predicate := range predicates {
			if !predicate(node) {
				satisfiesAllPredicates = false
				break
			}
		}

		if satisfiesAllPredicates {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

type podPredicate func(apiv1.Pod) bool

// isNonSystemPod returns true for any pod that does not belong to the 'kube-system' namespace
func isNonSystemPod(pod apiv1.Pod) bool {
	return pod.Namespace != KubeSystemNS
}

// isActivePod is a predicate that returns true for any pod that is in state 'Pending' or 'Running'
func isActivePod(pod apiv1.Pod) bool {
	return pod.Status.Phase == apiv1.PodPending || pod.Status.Phase == apiv1.PodRunning
}

// hasNodeLocalStorage returns true for any pod with a volume that is local to its node
func hasNodeLocalStorage(pod apiv1.Pod) bool {
	for _, vol := range pod.Spec.Volumes {
		if vol.HostPath != nil || vol.EmptyDir != nil {
			return true
		}
	}
	return false
}

func hasController(pod apiv1.Pod) bool {
	for _, ownerRef := range pod.ObjectMeta.OwnerReferences {
		if *ownerRef.Controller {
			return true
		}
	}
	return false
}

// filterPods takes a list of pods and returns a filtered list containing
// only Pods that satisifies all predicates
func filterPods(pods []apiv1.Pod, predicates ...podPredicate) []apiv1.Pod {
	filtered := make([]apiv1.Pod, 0)
	for _, pod := range pods {

		satisfiesAllPredicates := true
		for _, predicate := range predicates {
			if !predicate(pod) {
				satisfiesAllPredicates = false
				break
			}
		}

		if satisfiesAllPredicates {
			filtered = append(filtered, pod)
		}
	}
	return filtered
}
