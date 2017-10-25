package kube

import (
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/kube/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apiv1 "k8s.io/client-go/pkg/api/v1"
	policy "k8s.io/client-go/pkg/apis/policy/v1beta1"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
)

func newError(message string) error {
	return fmt.Errorf(message)
}

// Verify that GetNode(name) calls the expected methods on the KubeClient
// and puts together a proper NodeInfo.
func TestGetNode(t *testing.T) {
	worker1 := NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	pod11 := NewTestPod("default", "pod-1").CPU("100m").Mem("100M").SetStatus(apiv1.PodRunning).ToPod()

	cluster := NewFakeCluster()
	cluster.AddNodes(worker1)
	cluster.AddPod("worker-1", pod11)

	mockClient := new(mocks.KubeClient)
	nodeScaler := NewNodeScaler(mockClient)

	// set up expectations for the methods that are expected to be called on the KubeClient
	mockClient.On("GetNode", "worker-1").Return(cluster.Nodes["worker-1"], nil)
	mockClient.On("ListPods", metav1.NamespaceAll, nodeNameSelector("worker-1")).Return(
		&apiv1.PodList{Items: cluster.Pods["worker-1"]}, nil)

	// make call
	node, err := nodeScaler.GetNode("worker-1")
	assert.Nil(t, err, "unexpectedly failed")
	assert.Equal(t, cluster.Nodes["worker-1"].Name, node.Name, "got unexpected node")
	assert.Equal(t, 1, len(node.Pods), "unexpected number of pods on node")

	mockClient.AssertExpectations(t)
}

// Verify GetNode(name) behavior on KubeClient error.
func TestGetNodeWhenClientCallFails(t *testing.T) {
	mockClient := new(mocks.KubeClient)
	nodeScaler := NewNodeScaler(mockClient)

	// set up expectations for the methods that are expected to be called on the KubeClient
	mockClient.On("GetNode", "worker-1").Return(nil, newError("node does not exist"))

	// make call
	_, err := nodeScaler.GetNode("worker-1")
	assert.NotNil(t, err, "expected call to fail")
	assert.Equal(t, "node does not exist", err.Error())

	mockClient.AssertExpectations(t)
}

// Verify that ListNodes() calls the expected methods on the KubeClient
// and puts together a proper return value.
func TestListNodes(t *testing.T) {
	worker1 := NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker2 := NewTestNode("worker-2").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	pod1 := NewTestPod("default", "pod-1").CPU("100m").Mem("100M").SetStatus(apiv1.PodRunning).ToPod()
	pod2 := NewTestPod("default", "pod-2").CPU("100m").Mem("100M").SetStatus(apiv1.PodRunning).ToPod()

	cluster := NewFakeCluster()
	cluster.AddNodes(worker1, worker2)
	cluster.AddPod("worker-1", pod1)
	cluster.AddPod("worker-2", pod2)

	mockClient := new(mocks.KubeClient)
	nodeScaler := NewNodeScaler(mockClient)

	// set up expectations for the methods that are expected to be called on the KubeClient
	mockClient.On("ListNodes").Return(
		&apiv1.NodeList{Items: []apiv1.Node{*worker1, *worker2}}, nil)
	mockClient.On("GetNode", "worker-1").Return(worker1, nil)
	mockClient.On("GetNode", "worker-2").Return(worker2, nil)
	mockClient.On("ListPods", metav1.NamespaceAll, nodeNameSelector("worker-1")).Return(
		&apiv1.PodList{Items: cluster.Pods["worker-1"]}, nil)
	mockClient.On("ListPods", metav1.NamespaceAll, nodeNameSelector("worker-2")).Return(
		&apiv1.PodList{Items: cluster.Pods["worker-2"]}, nil)

	// make call and check return values
	nodes, err := nodeScaler.ListNodes()
	assert.Nil(t, err, "unexpectedly failed")
	assert.Equal(t, "worker-1", nodes[0].Name)
	assert.Equal(t, "worker-2", nodes[1].Name)
	assert.Equal(t, "pod-1", nodes[0].Pods[0].Name)
	assert.Equal(t, "pod-2", nodes[1].Pods[0].Name)

	mockClient.AssertExpectations(t)
}

// Verify that ListWorkerNodes() filters out any master nodes.
// A master node is characterized either by a "component: kube-apiserver"
// label, or a name of "kube-apiserver-<nodeName>".
func TestListWorkerNodes(t *testing.T) {
	master1 := NewTestNode("master-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	master2 := NewTestNode("master-2").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker1 := NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker2 := NewTestNode("worker-2").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	pod1 := NewTestPod("default", "pod-1").CPU("100m").Mem("100M").SetStatus(apiv1.PodRunning).ToPod()
	pod2 := NewTestPod("default", "pod-2").CPU("100m").Mem("100M").SetStatus(apiv1.PodRunning).ToPod()
	apiServerPod1 := NewTestPod("kube-system", "kube-apiserver-master-1").CPU("100m").Mem("100M").
		SetStatus(apiv1.PodRunning).ToPod()
	apiServerPod2 := NewTestPod("kube-system", "apiserver").CPU("100m").Mem("100M").
		SetStatus(apiv1.PodRunning).ToPod()
	apiServerPod2.Labels[ComponentLabelKey] = APIServerLabelValue

	cluster := &FakeCluster{
		Nodes: map[string]*apiv1.Node{
			"master-1": master1,
			"master-2": master2,
			"worker-1": worker1,
			"worker-2": worker2,
		},
		Pods: map[string][]apiv1.Pod{
			"master-1": []apiv1.Pod{*apiServerPod1},
			"master-2": []apiv1.Pod{*apiServerPod2},
			"worker-1": []apiv1.Pod{*pod1},
			"worker-2": []apiv1.Pod{*pod2},
		},
	}

	mockClient := new(mocks.KubeClient)
	nodeScaler := NewNodeScaler(mockClient)

	// set up expectations for the methods that are expected to be called on the KubeClient
	mockClient.On("ListNodes").Return(
		&apiv1.NodeList{Items: []apiv1.Node{*master1, *master2, *worker1, *worker2}}, nil)
	mockClient.On("GetNode", "master-1").Return(master1, nil)
	mockClient.On("GetNode", "master-2").Return(master2, nil)
	mockClient.On("GetNode", "worker-1").Return(worker1, nil)
	mockClient.On("GetNode", "worker-2").Return(worker2, nil)
	mockClient.On("ListPods", metav1.NamespaceAll, nodeNameSelector("master-1")).Return(
		&apiv1.PodList{Items: cluster.Pods["master-1"]}, nil)
	mockClient.On("ListPods", metav1.NamespaceAll, nodeNameSelector("master-2")).Return(
		&apiv1.PodList{Items: cluster.Pods["master-2"]}, nil)
	mockClient.On("ListPods", metav1.NamespaceAll, nodeNameSelector("worker-1")).Return(
		&apiv1.PodList{Items: cluster.Pods["worker-1"]}, nil)
	mockClient.On("ListPods", metav1.NamespaceAll, nodeNameSelector("worker-2")).Return(
		&apiv1.PodList{Items: cluster.Pods["worker-2"]}, nil)

	// make call and check return values
	nodes, err := nodeScaler.ListWorkerNodes()
	assert.Nil(t, err, "unexpectedly failed")

	// master nodes should have been filtered out
	assert.Equal(t, "worker-1", nodes[0].Name)
	assert.Equal(t, "worker-2", nodes[1].Name)

	mockClient.AssertExpectations(t)
}

// A node should not be a scale-down candidate if there doesn't exist any other
// ready nodes to evacuate pods to.
func TestIsScaleDownCandidateWhenNoOtherSchedulableNodesExist(t *testing.T) {
	tests := []struct {
		// a master node with an apiserver pod (should never be considered an
		// evacuation destination)
		master *apiv1.Node
		// the worker node that we wish to assess as a scale-down candidate
		victimNode *apiv1.Node
		// the other worker nodes that are potential evacuation targets
		potentialEvacTargets []*apiv1.Node
	}{
		// potential evacuation node is not ready
		{
			master:     NewTestNode("master").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
			victimNode: NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
			potentialEvacTargets: []*apiv1.Node{
				NewTestNode("worker-2").CPU("1000m").Mem("1000M").
					Ready(false).ToNode(),
			},
		},
		// potential evacuation node is unschedulable
		{
			master:     NewTestNode("master").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
			victimNode: NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
			potentialEvacTargets: []*apiv1.Node{
				NewTestNode("worker-2").CPU("1000m").Mem("1000M").Ready(true).
					Schedulable(false).ToNode(),
			},
		},
		// potential evacuation nodes are not ready and unschedulable
		{
			master:     NewTestNode("master").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
			victimNode: NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
			potentialEvacTargets: []*apiv1.Node{
				NewTestNode("worker-2").CPU("1000m").Mem("1000M").Ready(true).
					Schedulable(false).ToNode(),
				NewTestNode("worker-3").CPU("1000m").Mem("1000M").Ready(false).
					Schedulable(true).ToNode(),
			},
		},
	}

	for _, test := range tests {
		apiServerPod := NewTestPod("kube-system", "apiserver").Label(ComponentLabelKey, APIServerLabelValue).
			SetStatus(apiv1.PodRunning).ToPod()
		pod11 := NewTestPod("default", "nginx-1-1").CPU("100m").Mem("100M").
			Controller("nginx-deployment").SetStatus(apiv1.PodRunning).ToPod()

		cluster := NewFakeCluster()
		cluster.AddNodes(test.master, test.victimNode)
		cluster.AddNodes(test.potentialEvacTargets...)
		cluster.AddPod("master", apiServerPod)
		cluster.AddPod("worker-1", pod11)

		// set up expectations for the methods that are expected to be called on the KubeClient
		mockClient := new(mocks.KubeClient)
		prepareClientResponses(mockClient, cluster)

		nodeScaler := NewNodeScaler(mockClient)
		node, err := nodeScaler.GetNode("worker-1")
		isCandidate, err := nodeScaler.IsScaleDownCandidate(node)
		assert.False(t, isCandidate, "node not expected to be a scale-down candidate")
		require.NotNil(t, err, "expected scheduling to fail")
		require.IsType(t, &NodeEvacuationInfeasibleError{}, err, "unexpected error type")
		// make sure error string is built without errors
		require.NotNil(t, err.Error())
		evacErr := err.(*NodeEvacuationInfeasibleError)
		require.IsType(t, &NoEvacuationNodesAvailableError{}, evacErr.Reason, "unexpected error reason")
		// make sure error string is built without errors
		require.NotNil(t, evacErr.Reason.Error())
	}
}

// A node with a true value on its "cluster-autoscaler.kubernetes.io/scale-down-disabled"
// annotation must never be scaled down.
func TestIsScaleDownCandidateOnNodeWithScaleDownProtectionLabel(t *testing.T) {
	master := NewTestNode("master").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	// worker-1 is protected with a no-scale-down annotation
	worker1 := NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).
		Annotation(NoScaleDownNodeAnnotation, "true").ToNode()
	worker2 := NewTestNode("worker-2").CPU("1000m").Mem("1000M").Ready(true).ToNode()

	apiServerPod := NewTestPod("kube-system", "kube-apiserver-master").SetStatus(apiv1.PodRunning).ToPod()
	pod11 := NewTestPod("default", "nginx-1-1").CPU("100m").Mem("100M").
		Controller("nginx-deployment").SetStatus(apiv1.PodRunning).ToPod()
	pod21 := NewTestPod("default", "nginx-2-1").CPU("100m").Mem("100M").
		Controller("nginx-deployment").SetStatus(apiv1.PodRunning).ToPod()

	cluster := NewFakeCluster()
	cluster.AddNodes(master, worker1, worker2)
	cluster.AddPod("master", apiServerPod)
	cluster.AddPod("worker-1", pod11)
	cluster.AddPod("worker-2", pod21)

	// set up expectations for the methods that are expected to be called on the KubeClient
	mockClient := new(mocks.KubeClient)
	prepareClientResponses(mockClient, cluster)

	nodeScaler := NewNodeScaler(mockClient)
	node, err := nodeScaler.GetNode("worker-1")
	isCandidate, err := nodeScaler.IsScaleDownCandidate(node)
	assert.False(t, isCandidate, "node not expected to be a scale-down candidate")
	require.NotNil(t, err, "expected scheduling to fail")
	require.IsType(t, &NodeScaleDownProtectedError{}, err, "unexpected error type")
	// make sure error string is built without errors
	require.NotNil(t, err.Error())
}

// A pod without a controller (deployment/replica set/replication controller)
// cannot be evacuated as it would not be recreated, and should therefore
// prevent its node from being scaled down.
func TestIsScaleDownCandidateOnPodWithoutController(t *testing.T) {
	master := NewTestNode("master").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker1 := NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker2 := NewTestNode("worker-2").CPU("1000m").Mem("1000M").Ready(true).ToNode()

	apiServerPod := NewTestPod("kube-system", "kube-apiserver-master").SetStatus(apiv1.PodRunning).ToPod()
	// pod does not have a controller and can therefore not be safely evacuated
	pod11 := NewTestPod("default", "nginx-1-1").CPU("100m").Mem("100M").SetStatus(apiv1.PodRunning).ToPod()
	pod21 := NewTestPod("default", "nginx-2-1").CPU("100m").Mem("100M").
		Controller("nginx-deployment").SetStatus(apiv1.PodRunning).ToPod()

	cluster := NewFakeCluster()
	cluster.AddNodes(master, worker1, worker2)
	cluster.AddPod("master", apiServerPod)
	cluster.AddPod("worker-1", pod11)
	cluster.AddPod("worker-2", pod21)

	// set up expectations for the methods that are expected to be called on the KubeClient
	mockClient := new(mocks.KubeClient)
	prepareClientResponses(mockClient, cluster)

	nodeScaler := NewNodeScaler(mockClient)
	node, err := nodeScaler.GetNode("worker-1")
	isCandidate, err := nodeScaler.IsScaleDownCandidate(node)
	assert.False(t, isCandidate, "node not expected to be a scale-down candidate")
	require.NotNil(t, err, "expected scheduling to fail")
	require.Equal(t,
		&PodWithoutControllerError{NodeName: "worker-1", PodName: "nginx-1-1"},
		err,
		"unexpected error type")
	// make sure error string is built without errors
	require.NotNil(t, err.Error())
}

// A pod with node-local storage should prevent scale-down of its node.
func TestIsScaleDownCandidateOnPodWithLocalStorage(t *testing.T) {
	master := NewTestNode("master").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker1 := NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker2 := NewTestNode("worker-2").CPU("1000m").Mem("1000M").Ready(true).ToNode()

	apiServerPod := NewTestPod("kube-system", "kube-apiserver-master").SetStatus(apiv1.PodRunning).ToPod()
	// pod has local storage and can therefore not be safely evacuated
	pod11 := NewTestPod("default", "nginx-1-1").CPU("100m").Mem("100M").SetStatus(apiv1.PodRunning).
		Controller("nginx-deployment").HostPathVolume("/var/log").ToPod()
	pod21 := NewTestPod("default", "nginx-2-1").CPU("100m").Mem("100M").
		Controller("nginx-deployment").SetStatus(apiv1.PodRunning).ToPod()

	cluster := NewFakeCluster()
	cluster.AddNodes(master, worker1, worker2)
	cluster.AddPod("master", apiServerPod)
	cluster.AddPod("worker-1", pod11)
	cluster.AddPod("worker-2", pod21)

	// set up expectations for the methods that are expected to be called on the KubeClient
	mockClient := new(mocks.KubeClient)
	prepareClientResponses(mockClient, cluster)

	nodeScaler := NewNodeScaler(mockClient)
	node, err := nodeScaler.GetNode("worker-1")
	isCandidate, err := nodeScaler.IsScaleDownCandidate(node)
	assert.False(t, isCandidate, "node not expected to be a scale-down candidate")
	require.NotNil(t, err, "expected scheduling to fail")
	require.Equal(t,
		&PodLocalStorageError{NodeName: "worker-1", PodName: "nginx-1-1"},
		err,
		"unexpected error type")
	// make sure error string is built without errors
	require.NotNil(t, err.Error())

}

// A pod that cannot find any new destination node due to not tolerating the
// node taints of the candidate nodes should prevent scale-down.
func TestIsScaleDownCandidateOnPodWithPreventiveTaintTolerations(t *testing.T) {
	master := NewTestNode("master").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker1 := NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	// has database=true taint
	worker2 := NewTestNode("worker-2").CPU("1000m").Mem("1000M").
		Taint("database", "true").Ready(true).ToNode()

	apiServerPod := NewTestPod("kube-system", "kube-apiserver-master").SetStatus(apiv1.PodRunning).ToPod()
	// pod tolerates database taint
	pod11 := NewTestPod("default", "nginx-1-1").CPU("100m").Mem("100M").SetStatus(apiv1.PodRunning).
		AddToleration(apiv1.Toleration{Key: "database", Operator: apiv1.TolerationOpExists}).
		Controller("nginx-deployment").ToPod()
	// pod does not tolerate database taint
	pod12 := NewTestPod("default", "nginx-1-2").CPU("100m").Mem("100M").SetStatus(apiv1.PodRunning).
		Controller("nginx-deployment").ToPod()
	pod21 := NewTestPod("default", "nginx-2-1").CPU("100m").Mem("100M").
		Controller("nginx-deployment").SetStatus(apiv1.PodRunning).ToPod()

	cluster := NewFakeCluster()
	cluster.AddNodes(master, worker1, worker2)
	cluster.AddPod("master", apiServerPod)
	cluster.AddPod("worker-1", pod11)
	cluster.AddPod("worker-1", pod12)
	cluster.AddPod("worker-2", pod21)

	// set up expectations for the methods that are expected to be called on the KubeClient
	mockClient := new(mocks.KubeClient)
	prepareClientResponses(mockClient, cluster)

	nodeScaler := NewNodeScaler(mockClient)
	node, err := nodeScaler.GetNode("worker-1")
	isCandidate, err := nodeScaler.IsScaleDownCandidate(node)
	assert.False(t, isCandidate, "node not expected to be a scale-down candidate")
	require.NotNil(t, err, "expected scheduling to fail")
	require.IsType(t, &NodeEvacuationInfeasibleError{}, err, "unexpected error type")
	nodeEvacErr := err.(*NodeEvacuationInfeasibleError)
	assert.Equal(t, "worker-1", nodeEvacErr.NodeName)
	// verify that error reason is that nginx-1-2 cannot be evacuated to any other node
	assert.IsType(t, &PodSchedulingError{}, nodeEvacErr.Reason, "unexpected error reason")
	schedulingErr := nodeEvacErr.Reason.(*PodSchedulingError)
	assertTaintTolerationError(t,
		&TaintTolerationError{PodName: "nginx-1-2", Node: "worker-2",
			Taint: apiv1.Taint{Key: "database", Value: "true", Effect: apiv1.TaintEffectNoSchedule}},
		schedulingErr.NodeMismatches["worker-2"].Reason)
}

// A node with pod who cannot be evacuated to another node due to a
// preventive node selector should not be a scale-down candidate.
func TestIsScaleDownCandidateOnPodWithPreventiveNodeSelector(t *testing.T) {
	master := NewTestNode("master").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	// has disktype=ssd label
	worker1 := NewTestNode("worker-1").CPU("1000m").Mem("1000M").
		Label("disktype", "ssd").Ready(true).ToNode()
	// has disktype=magnetic label
	worker2 := NewTestNode("worker-2").CPU("1000m").Mem("1000M").
		Label("disktype", "magnetic").Ready(true).ToNode()

	apiServerPod := NewTestPod("kube-system", "kube-apiserver-master").SetStatus(apiv1.PodRunning).ToPod()
	pod11 := NewTestPod("default", "nginx-1-1").CPU("100m").Mem("100M").SetStatus(apiv1.PodRunning).
		Controller("nginx-deployment").ToPod()
	// pod requires a node with disktype=ssd label
	pod12 := NewTestPod("default", "nginx-1-2").CPU("100m").Mem("100M").SetStatus(apiv1.PodRunning).
		AddNodeSelector("disktype", "ssd").Controller("nginx-deployment").ToPod()
	pod21 := NewTestPod("default", "nginx-2-1").CPU("100m").Mem("100M").
		Controller("nginx-deployment").SetStatus(apiv1.PodRunning).ToPod()

	cluster := NewFakeCluster()
	cluster.AddNodes(master, worker1, worker2)
	cluster.AddPod("master", apiServerPod)
	cluster.AddPod("worker-1", pod11)
	cluster.AddPod("worker-1", pod12)
	cluster.AddPod("worker-2", pod21)

	// set up expectations for the methods that are expected to be called on the KubeClient
	mockClient := new(mocks.KubeClient)
	prepareClientResponses(mockClient, cluster)

	nodeScaler := NewNodeScaler(mockClient)
	node, err := nodeScaler.GetNode("worker-1")
	isCandidate, err := nodeScaler.IsScaleDownCandidate(node)
	assert.False(t, isCandidate, "node not expected to be a scale-down candidate")
	require.NotNil(t, err, "expected scheduling to fail")
	require.IsType(t, &NodeEvacuationInfeasibleError{}, err, "unexpected error type")
	nodeEvacErr := err.(*NodeEvacuationInfeasibleError)
	assert.Equal(t, "worker-1", nodeEvacErr.NodeName)
	// verify that error reason is that nginx-1-2 cannot be evacuated to any other node
	assert.IsType(t, &PodSchedulingError{}, nodeEvacErr.Reason, "unexpected error reason")
	schedulingErr := nodeEvacErr.Reason.(*PodSchedulingError)
	assertNodeSelectorMismatchError(t,
		&NodeSelectorMismatchError{Pod: pod12, Node: worker2},
		schedulingErr.NodeMismatches["worker-2"].Reason)
}

// A node with pod who cannot be evacuated to another node due to a
// preventive node affinity policy should not be a scale-down candidate.
func TestIsScaleDownCandidateOnPodWithPreventiveNodeAffinity(t *testing.T) {
	master := NewTestNode("master").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker1 := NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	// has disktype=magnetic label
	worker2 := NewTestNode("worker-2").CPU("1000m").Mem("1000M").
		Label("disktype", "magnetic").Ready(true).ToNode()

	apiServerPod := NewTestPod("kube-system", "kube-apiserver-master").SetStatus(apiv1.PodRunning).ToPod()
	pod11 := NewTestPod("default", "nginx-1-1").CPU("100m").Mem("100M").SetStatus(apiv1.PodRunning).
		Controller("nginx-deployment").ToPod()
	// pod has node affinity policy: wants disktype=ssd
	nodeAffinityRequirement := apiv1.NodeSelectorRequirement{
		Key: "disktype", Operator: apiv1.NodeSelectorOpIn, Values: []string{"ssd"},
	}
	pod12 := NewTestPod("default", "nginx-1-2").CPU("100m").Mem("100M").SetStatus(apiv1.PodRunning).
		NodeAffinityRequirement(nodeAffinityRequirement).Controller("nginx-deployment").ToPod()
	pod21 := NewTestPod("default", "nginx-2-1").CPU("100m").Mem("100M").
		Controller("nginx-deployment").SetStatus(apiv1.PodRunning).ToPod()

	cluster := NewFakeCluster()
	cluster.AddNodes(master, worker1, worker2)
	cluster.AddPod("master", apiServerPod)
	cluster.AddPod("worker-1", pod11)
	cluster.AddPod("worker-1", pod12)
	cluster.AddPod("worker-2", pod21)

	// set up expectations for the methods that are expected to be called on the KubeClient
	mockClient := new(mocks.KubeClient)
	prepareClientResponses(mockClient, cluster)

	nodeScaler := NewNodeScaler(mockClient)
	node, err := nodeScaler.GetNode("worker-1")
	isCandidate, err := nodeScaler.IsScaleDownCandidate(node)
	assert.False(t, isCandidate, "node not expected to be a scale-down candidate")
	require.NotNil(t, err, "expected scheduling to fail")
	require.IsType(t, &NodeEvacuationInfeasibleError{}, err, "unexpected error type")
	nodeEvacErr := err.(*NodeEvacuationInfeasibleError)
	assert.Equal(t, "worker-1", nodeEvacErr.NodeName)
	// verify that error reason is that nginx-1-2 cannot be evacuated to any other node
	assert.IsType(t, &PodSchedulingError{}, nodeEvacErr.Reason, "unexpected error reason")
	schedulingErr := nodeEvacErr.Reason.(*PodSchedulingError)
	assertNodeAffinityMismatchError(t,
		&NodeAffinityMismatchError{Pod: pod12, Node: worker2},
		schedulingErr.NodeMismatches["worker-2"].Reason)
}

// A node with pods whose evacuation would violate a pod disruption budget
// must not be scaled down.
func TestIsScaleDownCandidateOnPodWithPreventivePodDisruptionBudget(t *testing.T) {
	master := NewTestNode("master").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker1 := NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker2 := NewTestNode("worker-2").CPU("1000m").Mem("1000M").Ready(true).ToNode()

	// pod disruption budget that does requires two running nginx pods at all times
	disruptionBudget := policy.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: "nginx-pdb"},
		Spec: policy.PodDisruptionBudgetSpec{
			MinAvailable: &intstr.IntOrString{IntVal: 2},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "nginx"},
			},
		},
		Status: policy.PodDisruptionBudgetStatus{
			CurrentHealthy:        3,
			DesiredHealthy:        3,
			PodDisruptionsAllowed: 1,
			DisruptedPods:         nil,
			ExpectedPods:          3,
		},
	}

	apiServerPod := NewTestPod("kube-system", "kube-apiserver-master").SetStatus(apiv1.PodRunning).ToPod()
	pod11 := NewTestPod("default", "nginx-1-1").CPU("100m").Mem("100M").SetStatus(apiv1.PodRunning).
		Label("app", "nginx").Controller("nginx-deployment").ToPod()
	pod12 := NewTestPod("default", "nginx-1-2").CPU("100m").Mem("100M").SetStatus(apiv1.PodRunning).
		Label("app", "nginx").Controller("nginx-deployment").ToPod()
	pod21 := NewTestPod("default", "nginx-2-1").CPU("100m").Mem("100M").
		Label("app", "nginx").Controller("nginx-deployment").SetStatus(apiv1.PodRunning).ToPod()

	cluster := NewFakeCluster()
	cluster.AddNodes(master, worker1, worker2)
	cluster.AddPod("master", apiServerPod)
	cluster.AddPod("worker-1", pod11)
	cluster.AddPod("worker-1", pod12)
	cluster.AddPod("worker-2", pod21)
	cluster.AddPodDisruptionBudget(disruptionBudget)

	// set up expectations for the methods that are expected to be called on the KubeClient
	mockClient := new(mocks.KubeClient)
	prepareClientResponses(mockClient, cluster)

	nodeScaler := NewNodeScaler(mockClient)
	node, err := nodeScaler.GetNode("worker-1")
	isCandidate, err := nodeScaler.IsScaleDownCandidate(node)
	assert.False(t, isCandidate, "node not expected to be a scale-down candidate")
	require.NotNil(t, err, "expected scheduling to fail")
	require.IsType(t, &NodeEvacuationInfeasibleError{}, err, "unexpected error type")
	nodeEvacErr := err.(*NodeEvacuationInfeasibleError)
	assert.Equal(t, "worker-1", nodeEvacErr.NodeName)
	// verify that error reason is that pod disruption budget would be violated
	assert.IsType(t, &PodDisruptionViolationError{}, nodeEvacErr.Reason, "unexpected error reason")
	pdbViolateErr := nodeEvacErr.Reason.(*PodDisruptionViolationError)
	assert.Equal(t, "nginx-pdb", pdbViolateErr.PodDisruptionBudget)
	assert.Equal(t, int32(1), pdbViolateErr.AllowedDisruptions)
	assert.Equal(t, int32(2), pdbViolateErr.RequiredDisruptions)
	// make sure error string is built without errors
	require.NotNil(t, pdbViolateErr.Error())
}

// If the quasi-scheduler is unable to find room for all pods on a node,
// that node must not be scaled down.
func TestIsScaleDownCandidateWithoutSufficientCPURoom(t *testing.T) {
	master := NewTestNode("master").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker1 := NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker2 := NewTestNode("worker-2").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker3 := NewTestNode("worker-3").CPU("1000m").Mem("1000M").Ready(true).ToNode()

	apiServerPod := NewTestPod("kube-system", "kube-apiserver-master").SetStatus(apiv1.PodRunning).ToPod()
	// pod does not fit on any available node gap (although total free cluster capacity suggests so)
	pod11 := NewTestPod("default", "nginx-1-1").CPU("300m").Mem("100M").SetStatus(apiv1.PodRunning).
		Label("app", "nginx").Controller("nginx-deployment").ToPod()
	pod21 := NewTestPod("default", "nginx-2-1").CPU("800m").Mem("800M").
		Label("app", "nginx").Controller("nginx-deployment").SetStatus(apiv1.PodRunning).ToPod()
	pod31 := NewTestPod("default", "nginx-3-1").CPU("750m").Mem("800M").
		Label("app", "nginx").Controller("nginx-deployment").SetStatus(apiv1.PodRunning).ToPod()

	cluster := NewFakeCluster()
	cluster.AddNodes(master, worker1, worker2, worker3)
	cluster.AddPod("master", apiServerPod)
	cluster.AddPod("worker-1", pod11)
	cluster.AddPod("worker-2", pod21)
	cluster.AddPod("worker-3", pod31)

	// set up expectations for the methods that are expected to be called on the KubeClient
	mockClient := new(mocks.KubeClient)
	prepareClientResponses(mockClient, cluster)

	nodeScaler := NewNodeScaler(mockClient)
	node, err := nodeScaler.GetNode("worker-1")
	isCandidate, err := nodeScaler.IsScaleDownCandidate(node)
	assert.False(t, isCandidate, "node not expected to be a scale-down candidate")
	require.NotNil(t, err, "expected scheduling to fail")
	require.IsType(t, &NodeEvacuationInfeasibleError{}, err, "unexpected error type")
	nodeEvacErr := err.(*NodeEvacuationInfeasibleError)
	assert.Equal(t, "worker-1", nodeEvacErr.NodeName)
	// pod cannot be scheduled onto any other node due to insufficient spare cpu
	assert.IsType(t, &PodSchedulingError{}, nodeEvacErr.Reason, "unexpected error reason")
	schedulingErr := nodeEvacErr.Reason.(*PodSchedulingError)
	assertInsufficentResourceError(t,
		&InsufficientResourceError{
			Type: ResourceTypeCPU, Wanted: parseQuantity("300m"), Free: parseQuantity("200m")},
		schedulingErr.NodeMismatches["worker-2"].Reason)
	assertInsufficentResourceError(t,
		&InsufficientResourceError{
			Type: ResourceTypeCPU, Wanted: parseQuantity("300m"), Free: parseQuantity("250m")},
		schedulingErr.NodeMismatches["worker-3"].Reason)
}

// If the quasi-scheduler is unable to find room for all pods on a node,
// that node must not be scaled down.
func TestIsScaleDownCandidateWithoutSufficientMemRoom(t *testing.T) {
	master := NewTestNode("master").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker1 := NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker2 := NewTestNode("worker-2").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker3 := NewTestNode("worker-3").CPU("1000m").Mem("1000M").Ready(true).ToNode()

	apiServerPod := NewTestPod("kube-system", "kube-apiserver-master").SetStatus(apiv1.PodRunning).ToPod()
	// pod does not fit on any available node gap (although total free cluster capacity suggests so)
	pod11 := NewTestPod("default", "nginx-1-1").CPU("100m").Mem("300M").SetStatus(apiv1.PodRunning).
		Label("app", "nginx").Controller("nginx-deployment").ToPod()
	pod21 := NewTestPod("default", "nginx-2-1").CPU("10m").Mem("800M").
		Label("app", "nginx").Controller("nginx-deployment").SetStatus(apiv1.PodRunning).ToPod()
	pod31 := NewTestPod("default", "nginx-3-1").CPU("10m").Mem("750M").
		Label("app", "nginx").Controller("nginx-deployment").SetStatus(apiv1.PodRunning).ToPod()

	cluster := NewFakeCluster()
	cluster.AddNodes(master, worker1, worker2, worker3)
	cluster.AddPod("master", apiServerPod)
	cluster.AddPod("worker-1", pod11)
	cluster.AddPod("worker-2", pod21)
	cluster.AddPod("worker-3", pod31)

	// set up expectations for the methods that are expected to be called on the KubeClient
	mockClient := new(mocks.KubeClient)
	prepareClientResponses(mockClient, cluster)

	nodeScaler := NewNodeScaler(mockClient)
	node, err := nodeScaler.GetNode("worker-1")
	isCandidate, err := nodeScaler.IsScaleDownCandidate(node)
	assert.False(t, isCandidate, "node not expected to be a scale-down candidate")
	require.NotNil(t, err, "expected scheduling to fail")
	require.IsType(t, &NodeEvacuationInfeasibleError{}, err, "unexpected error type")
	nodeEvacErr := err.(*NodeEvacuationInfeasibleError)
	assert.Equal(t, "worker-1", nodeEvacErr.NodeName)
	// pod cannot be scheduled onto any other node due to insufficient spare cpu
	assert.IsType(t, &PodSchedulingError{}, nodeEvacErr.Reason, "unexpected error reason")
	schedulingErr := nodeEvacErr.Reason.(*PodSchedulingError)
	assertInsufficentResourceError(t,
		&InsufficientResourceError{
			Type: ResourceTypeMem, Wanted: parseQuantity("300M"), Free: parseQuantity("200M")},
		schedulingErr.NodeMismatches["worker-2"].Reason)
	assertInsufficentResourceError(t,
		&InsufficientResourceError{
			Type: ResourceTypeMem, Wanted: parseQuantity("300M"), Free: parseQuantity("250M")},
		schedulingErr.NodeMismatches["worker-3"].Reason)
}

// Verify that node is deemed a scale-down candidate in a case where evacuation is possible
// (despite several scheduling restrictions: resource requests, node selectors, node affinity,
// pod disruption budgets)
func TestIsScaleDownCandidateWhenEvacuationPossible(t *testing.T) {
	master := NewTestNode("master").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker1 := NewTestNode("worker-1").CPU("1000m").Mem("1000M").
		Label("disktype", "ssd").Label("disk-performance", "high").
		Label("net-performance", "high").Ready(true).ToNode()
	worker2 := NewTestNode("worker-2").CPU("1000m").Mem("1000M").
		Label("disktype", "magnetic").Label("disk-performance", "low").
		Label("net-performance", "high").Ready(true).ToNode()
	worker3 := NewTestNode("worker-3").CPU("1000m").Mem("1000M").
		Label("disktype", "ssd").Label("disk-performance", "high").
		Label("net-performance", "low").Ready(true).ToNode()

	apiServerPod := NewTestPod("kube-system", "kube-apiserver-master").SetStatus(apiv1.PodRunning).ToPod()
	//
	// pods already present on evacuation destinations
	//
	pod21 := NewTestPod("default", "nginx-2-1").CPU("100m").Mem("100M").
		Label("app", "nginx").Controller("nginx-deployment").SetStatus(apiv1.PodRunning).ToPod()
	pod31 := NewTestPod("default", "nginx-3-1").CPU("100m").Mem("100M").
		Label("app", "nginx").Controller("nginx-deployment").SetStatus(apiv1.PodRunning).ToPod()

	//
	// pods that are to be evacuated
	//
	// has a node selector forcing it to be evacuated to worker2
	pod11 := NewTestPod("default", "nginx-1-1").CPU("900m").Mem("900M").SetStatus(apiv1.PodRunning).
		AddNodeSelector("net-performance", "high").Label("app", "nginx").Controller("nginx-deployment").ToPod()
	//  has node affinity forcing it to be evacuated to worker3
	pod12 := NewTestPod("default", "mysql").CPU("900m").Mem("900M").SetStatus(apiv1.PodRunning).
		NodeAffinityRequirement(apiv1.NodeSelectorRequirement{
			Key: "disk-performance", Operator: apiv1.NodeSelectorOpIn, Values: []string{"high"},
		}).
		Label("app", "mysql").Controller("mysql-deployment").ToPod()

	// pod disruption budget that requires two running nginx pods at all times
	disruptionBudget := policy.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: "nginx-pdb"},
		Spec: policy.PodDisruptionBudgetSpec{
			MinAvailable: &intstr.IntOrString{IntVal: 2},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "nginx"},
			},
		},
		Status: policy.PodDisruptionBudgetStatus{
			CurrentHealthy:        3,
			DesiredHealthy:        3,
			PodDisruptionsAllowed: 1,
			DisruptedPods:         nil,
			ExpectedPods:          3,
		},
	}

	cluster := NewFakeCluster()
	cluster.AddNodes(master, worker1, worker2, worker3)
	cluster.AddPod("master", apiServerPod)
	cluster.AddPod("worker-1", pod11)
	cluster.AddPod("worker-1", pod12)
	cluster.AddPod("worker-2", pod21)
	cluster.AddPod("worker-3", pod31)
	cluster.AddPodDisruptionBudget(disruptionBudget)

	// set up expectations for the methods that are expected to be called on the KubeClient
	mockClient := new(mocks.KubeClient)
	prepareClientResponses(mockClient, cluster)

	nodeScaler := NewNodeScaler(mockClient)
	node, err := nodeScaler.GetNode("worker-1")
	isCandidate, err := nodeScaler.IsScaleDownCandidate(node)
	require.Nil(t, err, "unexpectedly failed: %s", err)
	assert.True(t, isCandidate, "expected node to be a scale-down candidate")

	mockClient.AssertExpectations(t)
}

// Verify the behavior of DeleteNode(node).
func TestDeleteNode(t *testing.T) {
	master1 := NewTestNode("master-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker1 := NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker2 := NewTestNode("worker-2").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	pod1 := buildPod("pod-1", "100m", "100M")
	pod2 := buildPod("pod-2", "100m", "100M")
	apiServerPod1 := buildPodNS("kube-system", "kube-apiserver-master-1", "100m", "100M")

	cluster := NewFakeCluster()
	cluster.AddNodes(master1, worker1, worker2)
	cluster.AddPod("master-1", apiServerPod1)
	cluster.AddPod("worker-1", pod1)
	cluster.AddPod("worker-2", pod2)

	// set up expectations
	mockClient := new(mocks.KubeClient)
	mockClient.On("GetNode", "worker-2").Return(worker2, nil)
	mockClient.On("ListPods", metav1.NamespaceAll, nodeNameSelector("worker-2")).Return(
		&apiv1.PodList{Items: cluster.Pods["worker-2"]}, nil)

	nodeScaler := NewNodeScaler(mockClient)
	victimNode, err := nodeScaler.GetNode("worker-2")
	assert.Nil(t, err, "unexpectedly failed to get node")

	// set up expectation
	mockClient.On("DeleteNode", &victimNode.Node).Return(nil)

	err = nodeScaler.DeleteNode(victimNode)
	assert.Nil(t, err, "unexpectedly failed to delete node")

	mock.AssertExpectationsForObjects(t)
}

// Verify the behavior of DeleteNode(node).
func TestDeleteNodeOnError(t *testing.T) {
	master1 := NewTestNode("master-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker1 := NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker2 := NewTestNode("worker-2").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	pod1 := buildPod("pod-1", "100m", "100M")
	pod2 := buildPod("pod-2", "100m", "100M")
	apiServerPod1 := buildPodNS("kube-system", "kube-apiserver-master-1", "100m", "100M")

	cluster := NewFakeCluster()
	cluster.AddNodes(master1, worker1, worker2)
	cluster.AddPod("master-1", apiServerPod1)
	cluster.AddPod("worker-1", pod1)
	cluster.AddPod("worker-2", pod2)

	// set up expectations
	mockClient := new(mocks.KubeClient)
	mockClient.On("GetNode", "worker-2").Return(worker2, nil)
	mockClient.On("ListPods", metav1.NamespaceAll, nodeNameSelector("worker-2")).Return(
		&apiv1.PodList{Items: cluster.Pods["worker-2"]}, nil)

	nodeScaler := NewNodeScaler(mockClient)
	victimNode, err := nodeScaler.GetNode("worker-2")
	assert.Nil(t, err, "unexpectedly failed to get node")

	// set up expectation
	mockClient.On("DeleteNode", &victimNode.Node).Return(newError("timeout"))

	err = nodeScaler.DeleteNode(victimNode)
	assert.NotNil(t, err, "expected delete node to fail")
	assert.Equal(t, "failed to delete node: timeout", err.Error())

	mock.AssertExpectationsForObjects(t)
}

// Verify the behavior of NodeLoad(node)
func TestNodeLoadCall(t *testing.T) {
	node := buildNodeInfo(
		NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
		buildPod("p1", "300m", "800M"),
		buildPod("p2", "700m", "200M"))

	mockClient := new(mocks.KubeClient)

	nodeScaler := NewNodeScaler(mockClient)

	expectedNodeLoad := (1000.0 / 1000.0) + (1000.0 / 1000.0)
	load := nodeScaler.NodeLoad(node)
	assert.Equal(t, expectedNodeLoad, load, "unexpected node load")
}

// Verify the correctness of DrainNode(node). It should
// - mark the node unschedulable (by tainting the node)
// - evict each pending/running, non-system pod on the node
func TestDrainNode(t *testing.T) {
	master1 := NewTestNode("master-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	apiServerPod1 := buildPodNS("kube-system", "kube-apiserver-master-1", "100m", "100M")

	worker1 := NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	worker2 := NewTestNode("worker-2").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	runningPod := buildPod("pod-running", "100m", "100M")
	// only pending/active pods are not evacuated
	terminatingPod := buildPod("pod-failed", "100m", "100M")
	terminatingPod.Status = apiv1.PodStatus{Phase: apiv1.PodFailed}
	// system pods are not evacuated
	systemPod := buildPodNS("kube-system", "kube-proxy-xyz", "100m", "100M")

	cluster := NewFakeCluster()
	cluster.AddNodes(master1, worker1, worker2)
	cluster.AddPod("master-1", apiServerPod1)
	cluster.AddPod("worker-2", runningPod).AddPod("worker-2", terminatingPod).AddPod("worker-2", systemPod)

	// set up expectations
	mockClient := new(mocks.KubeClient)
	mockClient.On("GetNode", "worker-2").Return(worker2, nil)
	mockClient.On("ListPods", metav1.NamespaceAll, nodeNameSelector("worker-2")).Return(
		&apiv1.PodList{Items: cluster.Pods["worker-2"]}, nil)

	nodeScaler := NewNodeScaler(mockClient)
	victimNode, err := nodeScaler.GetNode("worker-2")
	assert.Nil(t, err, "unexpectedly failed to get victim node")

	// drain expectations
	// mark unschedulable: UpdateNode(node *apiv1.Node)
	mockClient.On("UpdateNode",
		mock.MatchedBy(nodeWithTaintArg("worker-2", NodeAboutToBeDrainedTaint))).Return(nil)
	// evict pod-running
	mockClient.On("EvictPod",
		mock.MatchedBy(podEvictionArg(runningPod))).Return(nil)

	err = nodeScaler.DrainNode(victimNode)
	assert.Nil(t, err, "unexpectedly failed to drain node")

	mock.AssertExpectationsForObjects(t)
}

// If a drain fails part-way through (for example, due to a pod eviction failing
// such as by violating a pod disruption budget) the node should be marked
// schedulable again.
func TestDrainNodeOnEvictionError(t *testing.T) {
	worker2 := NewTestNode("worker-2").CPU("1000m").Mem("1000M").Ready(true).ToNode()
	runningPod := buildPod("pod-running", "100m", "100M")

	cluster := NewFakeCluster()
	cluster.AddNodes(worker2)
	cluster.AddPod("worker-2", runningPod)

	// set up expectations
	mockClient := new(mocks.KubeClient)
	mockClient.On("GetNode", "worker-2").Return(worker2, nil)
	mockClient.On("ListPods", metav1.NamespaceAll, nodeNameSelector("worker-2")).Return(
		&apiv1.PodList{Items: cluster.Pods["worker-2"]}, nil)

	nodeScaler := NewNodeScaler(mockClient)
	victimNode, err := nodeScaler.GetNode("worker-2")
	assert.Nil(t, err, "unexpectedly failed to get victim node")

	// drain expectations
	// mark unschedulable: UpdateNode(node *apiv1.Node)
	mockClient.On("UpdateNode",
		mock.MatchedBy(nodeWithTaintArg("worker-2", NodeAboutToBeDrainedTaint))).Return(nil)
	// fail eviction of pod-running ...
	mockClient.On("EvictPod",
		mock.MatchedBy(podEvictionArg(runningPod))).Return(newError("pod could not be evicted"))
	// ... this should fail the entire drain process and the node
	// should be marked schedulable again
	mockClient.On("UpdateNode",
		mock.MatchedBy(nodeWithoutTaintArg("worker-2", NodeAboutToBeDrainedTaint))).Return(nil)

	err = nodeScaler.DrainNode(victimNode)
	assert.NotNil(t, err, "expected node drain to fail")

	mock.AssertExpectationsForObjects(t)
}

// mock argument matcher which checks that a function was called with a *apiv1.Node
// argument with a certain name and a certain taint set.
func nodeWithTaintArg(expectedNodeName string, expectedTaintKey string) func(node *apiv1.Node) bool {
	return func(node *apiv1.Node) bool {
		if expectedNodeName != node.Name {
			return false
		}
		for _, taint := range node.Spec.Taints {
			if taint.Key == expectedTaintKey {
				return true
			}
		}
		return false
	}
}

// mock argument matcher which checks that a function was called with a *apiv1.Node
// argument with a certain name and a certain taint *not* set.
func nodeWithoutTaintArg(expectedNodeName string, expectedAbsentTaintKey string) func(node *apiv1.Node) bool {
	return func(node *apiv1.Node) bool {
		if expectedNodeName != node.Name {
			return false
		}
		for _, taint := range node.Spec.Taints {
			if taint.Key == expectedAbsentTaintKey {
				return false
			}
		}
		return true
	}
}

// mock argument matcher which checks that a function was called with a *apiv1.Node
// argument with a certain name and a certain taint set.
func podEvictionArg(expectedPodToEvict *apiv1.Pod) func(eviction *policy.Eviction) bool {
	return func(eviction *policy.Eviction) bool {
		if expectedPodToEvict.Name != eviction.ObjectMeta.Name {
			return false
		}
		if expectedPodToEvict.Namespace != eviction.ObjectMeta.Namespace {
			return false
		}
		return true
	}
}

func nodeNameSelector(nodeName string) metav1.ListOptions {
	return metav1.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": nodeName}).String(),
	}
}

// prepareClientResponses prepares a mock KubeClient with responses that
// reflect the state of a a FakeCluster
func prepareClientResponses(mockClient *mocks.KubeClient, cluster *FakeCluster) {
	// prepare ListNodes() call
	var nodes []apiv1.Node
	for _, node := range cluster.Nodes {
		nodes = append(nodes, *node)
	}
	mockClient.On("ListNodes").Return(&apiv1.NodeList{Items: nodes}, nil)

	// prepare GetNode() calls
	for _, node := range cluster.Nodes {
		mockClient.On("GetNode", node.Name).Return(node, nil)
	}

	// prepare ListPods() calls
	for _, node := range cluster.Nodes {
		mockClient.On("ListPods", metav1.NamespaceAll, nodeNameSelector(node.Name)).Return(
			&apiv1.PodList{Items: cluster.Pods[node.Name]}, nil)
	}

	// preare PodDisruptionBudgets() call
	mockClient.On("PodDisruptionBudgets", "").Return(cluster.PodDisruptionBudgetList, nil)
}
