package proxy

import (
	"bytes"
	"fmt"
	"net/http"
	"sort"

	"github.com/golang/glog"

	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/cloudpool"
	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/kube"
)

// CloudPoolProxy is an interface that captures the main functionality
// of the kubernetes-aware cloudpool proxy: forwarding requests to the
// backend cloudpool and handling scale-downs.
type CloudPoolProxy interface {
	// Forward forwards an HTTP request to the backend cloudpool and
	// responds to the client with the backend cloudpool's response.
	Forward(request *http.Request) (*http.Response, error)

	// SetDesiredSize sets a new desired size on the cloudpool and, if
	// the new size, suggests a scale-down, it attempts to find a suitable
	// Kubernetes node to scale-down, evacuates that node, and deletes the
	// node from Kubernetes, before terminating the machine in the cloudpool.
	SetDesiredSize(desiredSize int) error

	// TerminateMachine forwards a termination request to the backend
	// cloudpool after ensuring that that the machine can be safely
	// scaled down, and after evacuating the node and removing it from the
	// Kubernetes cluster.
	TerminateMachine(machineID string, decrementDesiredSize bool) error
}

// KubernetesClientError is returned to indicate a problem to
// communicate with the Kubernetes API server.
type KubernetesClientError struct {
	Reason string
}

func (e *KubernetesClientError) Error() string {
	return e.Reason
}

// CloudPoolClientError is returned to indicate a problem to
// communicate with the backend cloudpool.
type CloudPoolClientError struct {
	Reason string
}

func (e *CloudPoolClientError) Error() string {
	return e.Reason
}

// NodeNotFoundError is returned to indicate that a certain requested
// Kubernetes node was not found.
type NodeNotFoundError struct {
	Reason string
}

func (e *NodeNotFoundError) Error() string {
	return e.Reason
}

// MachineNotFoundError is returned to indicate that a certain machine
// was not found in the cloudpool.
type MachineNotFoundError struct {
	Reason string
}

func (e *MachineNotFoundError) Error() string {
	return e.Reason
}

// ScaleDownError indicates an error to scale down a node.
type ScaleDownError struct {
	Reason string
}

func (e *ScaleDownError) Error() string {
	return e.Reason
}

// NoViableScaleDownCandidateError indicates a failure to identify
// a node that can be safely scaled down.
type NoViableScaleDownCandidateError struct {
	Rejections []*NoScaleDownCandidateError
}

func (e *NoViableScaleDownCandidateError) Error() string {
	buf := bytes.NewBufferString("found no viable candidates for scaling down: ")
	for i := 0; i < len(e.Rejections); i++ {
		rejection := e.Rejections[i]
		fmt.Fprintf(buf, "%s", rejection.Error())
		if i < len(e.Rejections)-1 {
			fmt.Fprintf(buf, "; ")
		}
	}
	return buf.String()
}

// NodeMachinePair pairs a kubernetes node with its cloudpool machine
// counterpart.
type NodeMachinePair struct {
	KubeNode *kube.NodeInfo
	Machine  *cloudpool.Machine
}

func (n *NodeMachinePair) String() string {
	return "{" + n.KubeNode.Name + ": " + n.Machine.ID + "}"
}

// NoScaleDownCandidateError gives a reason as to why a certain node is
// not seen as a viable scale-down candidate.
type NoScaleDownCandidateError struct {
	NodeName string
	Reason   string
}

func (e *NoScaleDownCandidateError) Error() string {
	return fmt.Sprintf("%s is not a viable scale-down candidate: %s", e.NodeName, e.Reason)
}

// Proxy implements the proxy logic of the kubeaware-cloudpool-proxy:
// forwarding requests to the backend cloudpool and handling scale-downs.
type Proxy struct {
	poolClient cloudpool.CloudPoolClient
	nodeScaler kube.NodeScaler
}

// New creates a new Proxy with a given configuration.
func New(poolClient cloudpool.CloudPoolClient, nodeScaler kube.NodeScaler) *Proxy {
	return &Proxy{poolClient: poolClient, nodeScaler: nodeScaler}
}

// SetDesiredSize sets a new desired size on the cloudpool and, if
// the new size, suggests a scale-down, it attempts to find a suitable
// Kubernetes node to scale-down, evacuates that node, and deletes the
// node from Kubernetes, before terminating the machine in the cloudpool.
func (p *Proxy) SetDesiredSize(desiredSize int) error {
	glog.V(0).Infof("received desiredSize request: %d", desiredSize)

	nodes, err := p.nodeScaler.ListWorkerNodes()
	if err != nil {
		return &KubernetesClientError{fmt.Sprintf("failed to retrieve kubernetes worker nodes: %s", err)}
	}
	glog.V(4).Infof("worker nodes: %s", nodes)

	pool, err := p.poolClient.GetMachinePool()
	if err != nil {
		return &CloudPoolClientError{fmt.Sprintf("failed to retrieve cloudpool: %s", err)}
	}
	glog.V(4).Infof("cloudpool machines: %s", pool.Machines)
	activeMachines := cloudpool.FilterMachines(pool.Machines, cloudpool.IsMachineActive)
	glog.V(4).Infof("active cloudpool machines: %s", activeMachines)

	// match worker nodes with their pool machine counterpart.
	// this effectively filters out nodes for which there is no machine and vice versa.
	nodeMachinePairs := pairNodesWithMachines(nodes, activeMachines)
	glog.V(4).Infof("node-machine pairs: %s", nodeMachinePairs)

	numActiveNodes := len(nodeMachinePairs)
	glog.V(0).Infof("number of nodes with active pool counterparts: %d", numActiveNodes)

	if desiredSize >= numActiveNodes {
		glog.V(0).Infof("desiredSize (%d) >= numActiveNodes (%d): passing on request to backend cloudpool", desiredSize, numActiveNodes)
		err = p.poolClient.SetDesiredSize(desiredSize)
		if err != nil {
			return &CloudPoolClientError{
				fmt.Sprintf("failed to request desiredSize against backend cloudpool: %s", err),
			}
		}
		return nil
	}

	glog.V(0).Infof("desiredSize (%d) < numActiveNodes (%d): scale-down ordered", desiredSize, numActiveNodes)
	numExcessNodes := numActiveNodes - desiredSize

	victimCandidates, rejections := p.partitionScaleDownCandidates(nodeMachinePairs)
	logRejectedNodes(rejections)
	if len(victimCandidates) == 0 {
		return &NoViableScaleDownCandidateError{rejections}
	}
	// sort candidates in order of increasing load (first should be easiest to evacuate)
	p.sortByLoad(victimCandidates)
	glog.V(2).Infof("scale-down candidates that are 'safe' to drain: %d", len(victimCandidates))
	nodesToRemove := numExcessNodes
	if numExcessNodes > len(victimCandidates) {
		nodesToRemove = len(victimCandidates)
	}
	glog.V(2).Infof("will attempt to scale down %d nodes", nodesToRemove)
	// scale down nodes until done or until we fail (for whatever reason)
	for i := 0; i < nodesToRemove; i++ {
		glog.V(0).Infof("attempting to scale down node %d of %d ...", (i + 1), nodesToRemove)
		victim := victimCandidates[i]
		err := p.evictAndTerminate(victim, true)
		if err != nil {
			return &ScaleDownError{fmt.Sprintf("scale-down failed: %s", err)}
		}
		glog.V(0).Infof("machine terminated: %s", victim.Machine.ID)
	}
	return nil
}

// TerminateMachine terminates the given machine (gracefully) IF the current
// cluster situation allows it to be evacuated.
func (p *Proxy) TerminateMachine(machineID string, decrementDesiredSize bool) error {
	node, err := p.getNodeWithExternalID(machineID)
	if err != nil {
		return err
	}
	machine, err := p.poolClient.GetMachine(machineID)
	if err != nil {
		return &CloudPoolClientError{
			fmt.Sprintf("failed to get cloudpool machine with ID %s: %s", machineID, err),
		}
	}

	victim := &NodeMachinePair{node, machine}

	ok, reason := p.isScaleDownCandidate(victim)
	if !ok {
		return &ScaleDownError{fmt.Sprintf(
			"TerminateMachine: machine is not deemed safe to scale down: %s", reason)}
	}
	err = p.evictAndTerminate(victim, decrementDesiredSize)
	if err != nil {
		return err
	}
	glog.V(0).Infof("machine terminated: %s", victim.Machine.ID)
	return nil
}

// Forward forwards an HTTP request to the backend cloudpool and
// responds to the client with the backend cloudpool's response.
func (p *Proxy) Forward(request *http.Request) (*http.Response, error) {
	return p.poolClient.Forward(request)
}

func (p *Proxy) evictAndTerminate(victim *NodeMachinePair, decrementDesiredSize bool) error {
	// drain victim
	glog.V(0).Infof("draining node %s ...", victim.KubeNode.Name)
	err := p.nodeScaler.DrainNode(victim.KubeNode)
	if err != nil {
		return &KubernetesClientError{fmt.Sprintf("failed to drain victim node: %s", err)}
	}
	// delete node from kubernetes cluster
	glog.V(0).Infof("deleting node %s from kubernetes cluster ...", victim.KubeNode.Name)
	err = p.nodeScaler.DeleteNode(victim.KubeNode)
	if err != nil {
		return &KubernetesClientError{
			fmt.Sprintf("failed to delete kubernetes node %s: %s", victim.KubeNode.Name, err),
		}
	}
	// terminate victim machine in cloudpool
	glog.V(0).Infof("terminating machine %s ...", victim.Machine.ID)
	err = p.poolClient.TerminateMachine(victim.Machine.ID, decrementDesiredSize)
	if err != nil {
		return &KubernetesClientError{
			fmt.Sprintf("failed to terminate machine %s: %s", victim.Machine.ID, err),
		}
	}
	return nil
}

func (p *Proxy) getNodeWithExternalID(machineID string) (*kube.NodeInfo, error) {
	nodes, err := p.nodeScaler.ListWorkerNodes()
	if err != nil {
		return nil, &KubernetesClientError{fmt.Sprintf("failed to get nodes: %s", err)}
	}
	for _, node := range nodes {
		if node.Spec.ExternalID == machineID {
			return node, nil
		}
	}

	return nil, &NodeNotFoundError{fmt.Sprintf("no kubernetes node with external ID %s exists", machineID)}
}

// partitionScaleDownCandidates takes a list of nodes and splits it in
// two slices: one with nodes that are candidates for scale-down and one
// slice which carries the reasons, one for each rejected node, that explains
// why a particular node was not designated as a scale-down candidate.
func (p *Proxy) partitionScaleDownCandidates(nodes []*NodeMachinePair) (candidates []*NodeMachinePair, rejections []*NoScaleDownCandidateError) {
	for _, node := range nodes {
		ok, reason := p.isScaleDownCandidate(node)
		if ok {
			candidates = append(candidates, node)
		} else {
			rejections = append(rejections, reason)
		}
	}

	return candidates, rejections
}

// isScaleDownCandidate returns true if a given node is a scale-down
// candidate and false otherwise. In case false is returned, the
// reason for the node not being a candidate can be found in the
// error return value.
func (p *Proxy) isScaleDownCandidate(node *NodeMachinePair) (bool, *NoScaleDownCandidateError) {
	ok, reason := isMachineEvictable(node)
	if !ok {
		return false, reason
	}

	ok, reason = p.isNodeSafeToDrain(node)
	if !ok {
		return false, reason
	}
	return true, nil
}

func (p *Proxy) sortByLoad(nodes []*NodeMachinePair) {
	byLoad := func(i int, j int) bool {
		return p.nodeScaler.NodeLoad(nodes[i].KubeNode) < p.nodeScaler.NodeLoad(nodes[j].KubeNode)
	}
	sort.Slice(nodes, byLoad)

	for _, node := range nodes {
		glog.V(4).Infof("load on %s: %f", node.KubeNode.Name, p.nodeScaler.NodeLoad(node.KubeNode))
	}
}

func pairNodesWithMachines(nodes []*kube.NodeInfo, machines []cloudpool.Machine) []*NodeMachinePair {
	var nodeMachinePairs []*NodeMachinePair
	for _, node := range nodes {
		for _, machine := range machines {
			if node.Spec.ExternalID == machine.ID {
				nodeMachinePairs = append(nodeMachinePairs, &NodeMachinePair{node, &machine})
				break
			}
		}
	}

	return nodeMachinePairs
}

// NodeMachinePairPredicate determines if a given NodeMachinePair
// satisfies a given condition and if it does not, a reason error is
// returned to describe why the condition is not satisfied.
// type nodeMachinePairPredicate func(*NodeMachinePair) (bool, error)

func isMachineEvictable(n *NodeMachinePair) (bool, *NoScaleDownCandidateError) {
	if n.Machine.MembershipStatus.Evictable {
		return true, nil
	}
	return false, &NoScaleDownCandidateError{
		NodeName: n.KubeNode.Name, Reason: "machine's membershipStatus.evictable is true"}
}

func (p *Proxy) isNodeSafeToDrain(node *NodeMachinePair) (bool, *NoScaleDownCandidateError) {
	ok, err := p.nodeScaler.IsScaleDownCandidate(node.KubeNode)
	if err != nil {
		glog.Infof("node %s is not a viable scale-down candidate: %s", node.KubeNode.Name, err)
		return false, &NoScaleDownCandidateError{NodeName: node.KubeNode.Name, Reason: err.Error()}
	}

	return ok, nil
}

func logRejectedNodes(rejections []*NoScaleDownCandidateError) {
	for _, rejection := range rejections {
		glog.V(2).Infof("node rejection: %s", rejection)
	}
}
