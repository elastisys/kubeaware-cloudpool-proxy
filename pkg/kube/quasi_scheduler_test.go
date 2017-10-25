package kube

import (
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/client-go/pkg/api/v1"
)

// QuasiPodScheduler.{FreeMem,FreeCPU} should return the total amount of free mem/CPU over all schedulable nodes.
func TestFreeMemAndFreeCPU(t *testing.T) {
	tests := []struct {
		name            string
		nodes           []*NodeInfo
		expectedFreeMem *resource.Quantity
		expectedFreeCPU *resource.Quantity
	}{
		{
			name: "single empty node",
			nodes: []*NodeInfo{
				buildNodeInfo(NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()),
			},
			expectedFreeMem: parseQuantity("1000M"),
			expectedFreeCPU: parseQuantity("1000m"),
		},
		{
			name: "multiple empty nodes",
			nodes: []*NodeInfo{
				buildNodeInfo(NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()),
				buildNodeInfo(NewTestNode("worker-2").CPU("2000m").Mem("2000M").Ready(true).ToNode()),
			},
			expectedFreeMem: parseQuantity("3000M"),
			expectedFreeCPU: parseQuantity("3000m"),
		},
		{
			name: "single non-empty node",
			nodes: []*NodeInfo{
				buildNodeInfo(NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
					buildPod("p1", "250m", "500M")),
			},
			expectedFreeMem: parseQuantity("500M"),
			expectedFreeCPU: parseQuantity("750m"),
		},
		{
			name: "multiple non-empty nodes",
			nodes: []*NodeInfo{
				buildNodeInfo(NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
					buildPod("p1", "250m", "500M")),
				buildNodeInfo(NewTestNode("worker-2").CPU("1000m").Mem("2000M").Ready(true).ToNode(),
					buildPod("p2", "500m", "1000M"), buildPod("p3", "300m", "1000M")),
			},
			expectedFreeMem: parseQuantity("500M"),
			expectedFreeCPU: parseQuantity("950m"),
		},
		{
			name: "multiple full nodes",
			nodes: []*NodeInfo{
				buildNodeInfo(NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
					buildPod("p1", "1000m", "1000M")),
				buildNodeInfo(NewTestNode("worker-1").CPU("1000m").Mem("2000M").Ready(true).ToNode(),
					buildPod("p2", "750m", "1500M"), buildPod("p3", "250m", "500M")),
			},
			expectedFreeMem: parseQuantity("0M"),
			expectedFreeCPU: parseQuantity("0m"),
		},
	}

	for _, test := range tests {
		scheduler := NewQuasiScheduler(test.nodes, nil)
		assert.Equal(t, test.expectedFreeMem.MilliValue(), scheduler.FreeMem().MilliValue(), "unexpected amount of free memory")
		assert.Equal(t, test.expectedFreeCPU.MilliValue(), scheduler.FreeCPU().MilliValue(), "unexpected amount of free memory")
	}
}

// QuasiPodScheduler.NodeLoad should (using the default load function) be the sum of allocated cpu and mem fractions.
func TestNodeLoad(t *testing.T) {
	tests := []struct {
		name             string
		node             *NodeInfo
		expectedNodeLoad float64
	}{
		{
			name:             "empty node",
			node:             buildNodeInfo(NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()),
			expectedNodeLoad: (0.0 / 1000.0) + (0.0 / 1000.0),
		},
		{
			name: "non-empty node",
			node: buildNodeInfo(NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
				buildPod("p1", "250m", "500M")),
			expectedNodeLoad: (250.0 / 1000.0) + (500.0 / 1000.0),
		},
		{
			name: "full node",
			node: buildNodeInfo(NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
				buildPod("p1", "300m", "800M"), buildPod("p2", "700m", "200M")),
			expectedNodeLoad: (1000.0 / 1000.0) + (1000.0 / 1000.0),
		},
	}

	for _, test := range tests {
		scheduler := NewQuasiScheduler([]*NodeInfo{test.node}, nil)
		assert.Equal(t, test.expectedNodeLoad, scheduler.NodeLoad(test.node), "unexpected node load")
	}
}

// It should be possible to pass a custom node load function to QuasiPodScheduler
func TestCustomNodeLoadFunction(t *testing.T) {
	cpuFractionLoadFunc := func(node *NodeInfo) float64 {
		allocatableCPU := node.Status.Allocatable.Cpu().MilliValue()
		freeCPU := node.FreeCPU()
		allocatedCPU := allocatableCPU - freeCPU.MilliValue()
		cpuFraction := float64(allocatedCPU) / float64(allocatableCPU)
		return cpuFraction
	}

	tests := []struct {
		name             string
		node             *NodeInfo
		expectedNodeLoad float64
	}{
		{
			name:             "empty node",
			node:             buildNodeInfo(NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode()),
			expectedNodeLoad: (0.0 / 1000.0),
		},
		{
			name: "non-empty node",
			node: buildNodeInfo(NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
				buildPod("p1", "250m", "500M")),
			expectedNodeLoad: (250.0 / 1000.0),
		},
		{
			name: "full node",
			node: buildNodeInfo(NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
				buildPod("p1", "300m", "800M"), buildPod("p2", "700m", "200M")),
			expectedNodeLoad: (300.0 / 1000.0) + (700.0 / 1000.0),
		},
	}

	for _, test := range tests {
		scheduler := NewQuasiScheduler([]*NodeInfo{test.node}, cpuFractionLoadFunc)
		assert.Equal(t, test.expectedNodeLoad, scheduler.NodeLoad(test.node), "unexpected node load")
	}
}

// When there aren't any schedulable nodes, scheduling should fail with
// a ClusterCapacityError
func TestScheduleNoNodes(t *testing.T) {
	emptyNodeList := []*NodeInfo{}
	scheduler := NewQuasiScheduler(emptyNodeList, nil)

	podToSchedule := *buildPod("p1", "100m", "100M")
	_, err := scheduler.Schedule(podToSchedule)

	assert.NotNil(t, err, "expected scheduling to fail")
	require.IsType(t, &NoSchedulableNodesError{}, err, "unexpected error type")
	// make sure error string is built without errors
	require.NotNil(t, err.Error())
}

// VerifySchedulingViable should check if a group of pods can
// (theoretically, considering the total sum of free cluster
// resources) fit on the schedulable nodes.
func TestVerifySchedulingViable(t *testing.T) {

	tests := []struct {
		nodes                    []*NodeInfo
		podsToSchedule           []apiv1.Pod
		expectedToFail           bool
		insufficientResourceType string
		expectedWantedResource   *resource.Quantity
		expectedFreeResource     *resource.Quantity
	}{
		// cluster with sufficient (total amount) of resources
		{
			nodes: []*NodeInfo{
				buildNodeInfo(
					NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
					buildPod("pod-1-1", "500m", "500M")),
				buildNodeInfo(
					NewTestNode("worker-2").CPU("2000m").Mem("2000M").Ready(true).ToNode(),
					buildPod("pod-2-1", "1000m", "1000M")),
			},
			podsToSchedule: []apiv1.Pod{
				*buildPod("pod-1", "500m", "500M"),
				*buildPod("pod-2", "500m", "500M"),
				*buildPod("pod-3", "500m", "500M")},
			expectedToFail:           false,
			insufficientResourceType: "",
			expectedWantedResource:   nil,
			expectedFreeResource:     nil,
		},
		// cluster with insufficient CPU
		{
			nodes: []*NodeInfo{
				buildNodeInfo(
					NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
					buildPod("pod-1-1", "500m", "500M")),
				buildNodeInfo(
					NewTestNode("worker-2").CPU("2000m").Mem("2000M").Ready(true).ToNode(),
					buildPod("pod-2-1", "1000m", "1000M")),
			},
			podsToSchedule: []apiv1.Pod{
				*buildPod("pod-1", "500m", "500M"),
				*buildPod("pod-2", "500m", "500M"),
				*buildPod("pod-3", "501m", "500M")},
			expectedToFail:           true,
			insufficientResourceType: ResourceTypeCPU,
			expectedWantedResource:   parseQuantity("1501m"),
			expectedFreeResource:     parseQuantity("1500m"),
		},
		//cluster with insufficient memory
		{
			nodes: []*NodeInfo{
				buildNodeInfo(
					NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
					buildPod("pod-1-1", "500m", "500M")),
				buildNodeInfo(
					NewTestNode("worker-2").CPU("2000m").Mem("2000M").Ready(true).ToNode(),
					buildPod("pod-2-1", "1000m", "1000M")),
			},
			podsToSchedule: []apiv1.Pod{
				*buildPod("pod-1", "500m", "500M"),
				*buildPod("pod-2", "500m", "500M"),
				*buildPod("pod-3", "500m", "501M")},
			expectedToFail:           true,
			insufficientResourceType: ResourceTypeMem,
			expectedWantedResource:   parseQuantity("1501M"),
			expectedFreeResource:     parseQuantity("1500M"),
		},
	}

	for _, test := range tests {
		scheduler := NewQuasiScheduler(test.nodes, nil)
		err := scheduler.VerifySchedulingViable(test.podsToSchedule)
		if test.expectedToFail {
			require.NotNil(t, err, "expected scheduling feasability check to fail")
			require.IsType(t, &ClusterCapacityError{}, err, "unexpected error type")
			reason := err.(*ClusterCapacityError).InsufficientResource
			assert.Equal(t, test.insufficientResourceType, reason.Type)
			assert.Equal(t, test.expectedWantedResource.MilliValue(), reason.Wanted.MilliValue())
			assert.Equal(t, test.expectedFreeResource.MilliValue(), reason.Free.MilliValue())
		} else {
			require.Nil(t, err, "scheduling feasability check failed unexpectedly")
		}
	}
}

// A PodSchedulingError should be returned when a pod requests CPU
// that is available in the cluster as a whole, but not in a single
// chunk on any one node.
func TestSchedulePodThatDoesNotFitAnyCPUGap(t *testing.T) {
	nodeList := []*NodeInfo{
		buildNodeInfo(
			NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
			buildPod("pod-1-1", "800m", "800M")),
		buildNodeInfo(
			NewTestNode("worker-2").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
			buildPod("pod-2-1", "900m", "800M")),
	}
	scheduler := NewQuasiScheduler(nodeList, nil)

	podToSchedule := *buildPod("new-1", "250m", "100M")
	_, err := scheduler.Schedule(podToSchedule)

	assert.NotNil(t, err, "expected scheduling to fail")
	assert.IsType(t, &PodSchedulingError{}, err, "unexpected error type")
	schedulingErr := err.(*PodSchedulingError)
	nodeMismatches := schedulingErr.NodeMismatches

	// worker-1 scheduling mismatch error
	mismatch := nodeMismatches["worker-1"]
	assert.IsType(t, &InsufficientResourceError{}, mismatch.Reason)
	reason := mismatch.Reason.(*InsufficientResourceError)
	assert.Equal(t, ResourceTypeCPU, reason.Type)
	assert.Equal(t, parseQuantity("250m").MilliValue(), reason.Wanted.MilliValue())
	assert.Equal(t, parseQuantity("200m").MilliValue(), reason.Free.MilliValue())

	// worker-2 scheduling mismatch error
	mismatch = nodeMismatches["worker-2"]
	assert.IsType(t, &InsufficientResourceError{}, mismatch.Reason)
	reason = mismatch.Reason.(*InsufficientResourceError)
	assert.Equal(t, ResourceTypeCPU, reason.Type)
	assert.Equal(t, parseQuantity("250m").MilliValue(), reason.Wanted.MilliValue())
	assert.Equal(t, parseQuantity("100m").MilliValue(), reason.Free.MilliValue())
}

// A PodSchedulingError should be returned when a pod requests memory
// that is available in the cluster as a whole, but not in a single
// chunk on any one node.
func TestSchedulePodThatDoesNotFitAnyMemGap(t *testing.T) {
	nodeList := []*NodeInfo{
		buildNodeInfo(
			NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
			buildPod("pod-1-1", "800m", "800M")),
		buildNodeInfo(
			NewTestNode("worker-2").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
			buildPod("pod-2-1", "900m", "900M")),
	}
	scheduler := NewQuasiScheduler(nodeList, nil)

	// requests more memory than is available on any one node
	podToSchedule := *buildPod("new-1", "10m", "201M")
	_, err := scheduler.Schedule(podToSchedule)

	assert.NotNil(t, err, "expected scheduling to fail")
	assert.IsType(t, &PodSchedulingError{}, err, "unexpected error type")
	schedulingErr := err.(*PodSchedulingError)
	nodeMismatches := schedulingErr.NodeMismatches

	// worker-1 scheduling mismatch error
	mismatch := nodeMismatches["worker-1"]
	assert.IsType(t, &InsufficientResourceError{}, mismatch.Reason)
	reason := mismatch.Reason.(*InsufficientResourceError)
	assert.Equal(t, ResourceTypeMem, reason.Type)
	assert.Equal(t, parseQuantity("201M").MilliValue(), reason.Wanted.MilliValue())
	assert.Equal(t, parseQuantity("200M").MilliValue(), reason.Free.MilliValue())

	// worker-2 scheduling mismatch error
	mismatch = nodeMismatches["worker-2"]
	assert.IsType(t, &InsufficientResourceError{}, mismatch.Reason)
	reason = mismatch.Reason.(*InsufficientResourceError)
	assert.Equal(t, ResourceTypeMem, reason.Type)
	assert.Equal(t, parseQuantity("201M").MilliValue(), reason.Wanted.MilliValue())
	assert.Equal(t, parseQuantity("100M").MilliValue(), reason.Free.MilliValue())
}

// Exercises scheduling of a sequence of pods onto a set of nodes.
func TestScheduleMultiplePods(t *testing.T) {
	nodeList := []*NodeInfo{
		buildNodeInfo(
			NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
			buildPod("pod-1-1", "500m", "500M")),
		buildNodeInfo(
			NewTestNode("worker-2").CPU("1000m").Mem("1000M").Ready(true).ToNode(),
			buildPod("pod-2-1", "300m", "300M")),
	}
	scheduler := NewQuasiScheduler(nodeList, nil)

	// worker-1: 500m/500M, worker-2: 300m/300M

	placement, err := scheduler.Schedule(*buildPod("new-1", "300m", "300M"))
	assert.Nil(t, err, "unexpected scheduling error")
	// should end up on least loaded node => worker-2
	assert.Equal(t, "worker-2", placement.Node, "unexpected placement of pod")

	// worker-1: 500m/500M, worker-2: 600m/600M

	placement, err = scheduler.Schedule(*buildPod("new-2", "300m", "300M"))
	assert.Nil(t, err, "unexpected scheduling error")
	// should end up on least loaded node => worker-1
	assert.Equal(t, "worker-1", placement.Node, "unexpected placement of pod")

	// worker-1: 800m/800M, worker-2: 600m/600M

	placement, err = scheduler.Schedule(*buildPod("new-3", "300m", "300M"))
	assert.Nil(t, err, "unexpected scheduling error")
	// should end up on least loaded node => worker-2
	assert.Equal(t, "worker-2", placement.Node, "unexpected placement of pod")

	// worker-1: 800m/800M, worker-2: 900m/900M

	// should fail: insufficent cpu
	placement, err = scheduler.Schedule(*buildPod("new-4", "300m", "100M"))
	assert.NotNil(t, err, "expected scheduling to fail")
	assert.IsType(t, &PodSchedulingError{}, err, "unexpected error type")
	schedulingErr := err.(*PodSchedulingError)
	nodeMismatches := schedulingErr.NodeMismatches
	// verify worker-1 scheduling mismatch reason
	assertInsufficentResourceError(t,
		&InsufficientResourceError{Type: ResourceTypeCPU, Wanted: parseQuantity("300m"), Free: parseQuantity("200m")},
		nodeMismatches["worker-1"].Reason)
	// verify worker-2 scheduling mismatch reason
	assertInsufficentResourceError(t,
		&InsufficientResourceError{Type: ResourceTypeCPU, Wanted: parseQuantity("300m"), Free: parseQuantity("100m")},
		nodeMismatches["worker-2"].Reason)

	// worker-1: 800m/800M, worker-2: 900m/900M

	// should fail: insufficent memory
	placement, err = scheduler.Schedule(*buildPod("new-4", "100m", "201M"))
	assert.NotNil(t, err, "expected scheduling to fail")
	assert.IsType(t, &PodSchedulingError{}, err, "unexpected error type")
	schedulingErr = err.(*PodSchedulingError)
	nodeMismatches = schedulingErr.NodeMismatches
	// verify worker-1 scheduling mismatch reason
	assertInsufficentResourceError(t,
		&InsufficientResourceError{Type: ResourceTypeMem, Wanted: parseQuantity("201M"), Free: parseQuantity("200M")},
		nodeMismatches["worker-1"].Reason)
	// verify worker-2 scheduling mismatch reason
	assertInsufficentResourceError(t,
		&InsufficientResourceError{Type: ResourceTypeMem, Wanted: parseQuantity("201M"), Free: parseQuantity("100M")},
		nodeMismatches["worker-2"].Reason)
}

// For nodes that are tainted with taints of effect "NoSchedule" or "NoExecute",
// pods must have matching tolerations for each of those taints in order for the
// pod to be scheduled onto the node.
func TestScheduleWithTaints(t *testing.T) {
	// set up a node with several taints
	node := buildNodeInfo(NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode())
	node.Spec.Taints = []apiv1.Taint{
		apiv1.Taint{Key: "disk-pressure", Value: "value", Effect: apiv1.TaintEffectNoExecute},
		apiv1.Taint{Key: "database", Value: "value", Effect: apiv1.TaintEffectNoSchedule},
		// since effect is only "PreferNoSchedule", the scheduler diregards from this taint
		apiv1.Taint{Key: "high-end", Value: "value", Effect: apiv1.TaintEffectPreferNoSchedule},
	}
	nodeList := []*NodeInfo{node}

	scheduler := NewQuasiScheduler(nodeList, nil)

	// pod-1: no tolerations => should never be scheduled onto node
	pod1 := buildPod("pod-1", "100m", "100M")
	pod1.Spec.Tolerations = []apiv1.Toleration{}
	// pod-2: tolerates database-node taint, but not disk-pressure taint
	pod2 := buildPod("pod-2", "100m", "100M")
	pod2.Spec.Tolerations = []apiv1.Toleration{
		apiv1.Toleration{Key: "disk-pressure", Operator: apiv1.TolerationOpExists, Effect: apiv1.TaintEffectNoExecute},
	}
	// pod-3: tolerates database-node and disk-pressure taint
	pod3 := buildPod("pod-3", "100m", "100M")
	pod3.Spec.Tolerations = []apiv1.Toleration{
		apiv1.Toleration{Key: "disk-pressure", Operator: apiv1.TolerationOpExists, Effect: apiv1.TaintEffectNoExecute},
		// empty effect => match any effect
		apiv1.Toleration{Key: "database", Operator: apiv1.TolerationOpEqual, Value: "value", Effect: ""},
	}

	// schedule pod-1 => should fail due to not tolerating disk-pressure taint
	_, err := scheduler.Schedule(*pod1)
	assert.NotNil(t, err, "scheduling expected to fail")
	assert.IsType(t, &PodSchedulingError{}, err, "unexpected error type")
	schedulingErr := err.(*PodSchedulingError)
	assertTaintTolerationError(t,
		&TaintTolerationError{PodName: "pod-1", Node: "worker-1",
			Taint: apiv1.Taint{Key: "disk-pressure", Value: "value", Effect: apiv1.TaintEffectNoExecute}},
		schedulingErr.NodeMismatches["worker-1"].Reason)

	// schedule pod-2 => should fail due to not tolerating database taint
	_, err = scheduler.Schedule(*pod2)
	assert.NotNil(t, err, "scheduling expected to fail")
	assert.IsType(t, &PodSchedulingError{}, err, "unexpected error type")
	schedulingErr = err.(*PodSchedulingError)
	assertTaintTolerationError(t,
		&TaintTolerationError{PodName: "pod-2", Node: "worker-1",
			Taint: apiv1.Taint{Key: "database", Value: "value", Effect: apiv1.TaintEffectNoSchedule}},
		schedulingErr.NodeMismatches["worker-1"].Reason)

	// schedule pod-3 => should succeeed
	placement, err := scheduler.Schedule(*pod3)
	assert.Nil(t, err, "scheduling unexpectedly failed")
	assert.Equal(t, &PodPlacement{Node: "worker-1", Pod: "pod-3"}, placement)
}

// Pods with nodeSelectors must only be allowed to be scheduled onto
// nodes with matching labels.
func TestScheduleWithNodeSelectors(t *testing.T) {
	// set up a node with a few labels
	node := buildNodeInfo(NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode())
	node.ObjectMeta.Labels = map[string]string{"high-performance": "true", "disktype": "ssd"}
	nodeList := []*NodeInfo{node}

	scheduler := NewQuasiScheduler(nodeList, nil)

	tests := []struct {
		// the nodeSelector for the pod
		nodeSelector map[string]string
		// true if expected to match node labels
		expectedMatch bool
	}{
		{
			nodeSelector:  map[string]string{},
			expectedMatch: true,
		},
		{
			nodeSelector:  map[string]string{"high-performance": "true"},
			expectedMatch: true,
		},
		{
			nodeSelector:  map[string]string{"disktype": "ssd"},
			expectedMatch: true,
		},
		{
			nodeSelector:  map[string]string{"high-performance": "true", "disktype": "ssd"},
			expectedMatch: true,
		},
		// node does not have a label of this type
		{
			nodeSelector:  map[string]string{"cputype": "gpu"},
			expectedMatch: false,
		},
		// node disktype label has a different value
		{
			nodeSelector:  map[string]string{"disktype": "magnetic"},
			expectedMatch: false,
		},
		// node does not have a low-cost label
		{
			nodeSelector:  map[string]string{"high-performance": "true", "disktype": "ssd", "low-cost": "true"},
			expectedMatch: false,
		},
	}

	for _, test := range tests {
		pod := buildPod("pod-x", "10m", "10M")
		pod.Spec.NodeSelector = test.nodeSelector

		placement, err := scheduler.Schedule(*pod)
		if test.expectedMatch {
			assert.Nil(t, err, "scheduling unexpectedly failed")
			assert.Equal(t, &PodPlacement{Node: "worker-1", Pod: "pod-x"}, placement, "unexpected placement")
		} else {
			assert.NotNil(t, err, "scheduling expected to fail")
			assert.IsType(t, &PodSchedulingError{}, err, "unexpected error type")
			schedulingErr := err.(*PodSchedulingError)
			assertNodeSelectorMismatchError(t,
				&NodeSelectorMismatchError{Node: &node.Node, Pod: pod},
				schedulingErr.NodeMismatches["worker-1"].Reason)
		}
	}
}

// Pods with node affinity constraints must only be allowed to be scheduled onto
// nodes with matching labels.
func TestScheduleWithNodeAffinity(t *testing.T) {
	// set up a node with a few labels
	node := buildNodeInfo(NewTestNode("worker-1").CPU("1000m").Mem("1000M").Ready(true).ToNode())
	node.ObjectMeta.Labels = map[string]string{"high-performance": "true", "disktype": "ssd", "prio": "10"}
	nodeList := []*NodeInfo{node}

	scheduler := NewQuasiScheduler(nodeList, nil)

	tests := []struct {
		// the nodeSelector for the pod
		matchExpressions []apiv1.NodeSelectorRequirement
		// true if expected to match node labels
		expectedMatch bool
	}{
		// represent a pod without node affinity constraints
		{
			matchExpressions: []apiv1.NodeSelectorRequirement{},
			expectedMatch:    true,
		},
		{
			matchExpressions: []apiv1.NodeSelectorRequirement{
				apiv1.NodeSelectorRequirement{
					Key: "high-performance", Operator: apiv1.NodeSelectorOpExists,
				},
			},
			expectedMatch: true,
		},
		{
			matchExpressions: []apiv1.NodeSelectorRequirement{
				apiv1.NodeSelectorRequirement{
					Key: "disktype", Operator: apiv1.NodeSelectorOpIn, Values: []string{"ssd", "magnetic"},
				},
			},
			expectedMatch: true,
		},
		{
			matchExpressions: []apiv1.NodeSelectorRequirement{
				apiv1.NodeSelectorRequirement{
					Key: "prio", Operator: apiv1.NodeSelectorOpGt, Values: []string{"5"},
				},
			},
			expectedMatch: true,
		},
		{
			matchExpressions: []apiv1.NodeSelectorRequirement{
				apiv1.NodeSelectorRequirement{
					Key: "prio", Operator: apiv1.NodeSelectorOpLt, Values: []string{"15"},
				},
			},
			expectedMatch: true,
		},
		// multiple constraints
		{
			matchExpressions: []apiv1.NodeSelectorRequirement{
				apiv1.NodeSelectorRequirement{
					Key: "disktype", Operator: apiv1.NodeSelectorOpIn, Values: []string{"ssd"},
				},
				apiv1.NodeSelectorRequirement{
					Key: "high-performance", Operator: apiv1.NodeSelectorOpExists,
				},
			},
			expectedMatch: true,
		},
		// node affinity prevents this pod from being scheduled: prio < 5
		{
			matchExpressions: []apiv1.NodeSelectorRequirement{
				apiv1.NodeSelectorRequirement{
					Key: "prio", Operator: apiv1.NodeSelectorOpLt, Values: []string{"5"},
				},
			},
			expectedMatch: false,
		},
		{
			matchExpressions: []apiv1.NodeSelectorRequirement{
				apiv1.NodeSelectorRequirement{
					Key: "disktype", Operator: apiv1.NodeSelectorOpNotIn, Values: []string{"ssd"},
				},
			},
			expectedMatch: false,
		},
		// node affinity prevents this pod from being scheduled: high-performane not exists
		{
			matchExpressions: []apiv1.NodeSelectorRequirement{
				apiv1.NodeSelectorRequirement{
					Key: "prio", Operator: apiv1.NodeSelectorOpGt, Values: []string{"5"},
				},
				apiv1.NodeSelectorRequirement{
					Key: "high-performance", Operator: apiv1.NodeSelectorOpDoesNotExist,
				},
			},
			expectedMatch: false,
		},
	}

	for _, test := range tests {
		pod := buildPod("pod-x", "10m", "10M")
		if len(test.matchExpressions) > 0 {
			pod.Spec.Affinity = &apiv1.Affinity{
				NodeAffinity: &apiv1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &apiv1.NodeSelector{
						NodeSelectorTerms: []apiv1.NodeSelectorTerm{
							apiv1.NodeSelectorTerm{
								MatchExpressions: test.matchExpressions,
							},
						},
					},
				},
			}
		}

		placement, err := scheduler.Schedule(*pod)
		if test.expectedMatch {
			require.Nil(t, err, "scheduling unexpectedly failed")
			assert.Equal(t, &PodPlacement{Node: "worker-1", Pod: "pod-x"}, placement, "unexpected placement")
		} else {
			require.NotNil(t, err, "scheduling expected to fail")
			require.IsType(t, &PodSchedulingError{}, err, "unexpected error type")
			schedulingErr := err.(*PodSchedulingError)
			assertNodeAffinityMismatchError(t,
				&NodeAffinityMismatchError{Node: &node.Node, Pod: pod},
				schedulingErr.NodeMismatches["worker-1"].Reason)
		}
	}
}

// make sure String() method works properly
func TestToString(t *testing.T) {
	placement := PodPlacement{Node: "worker-1", Pod: "pod-1"}
	assert.Equal(t, "pod-1 -> worker-1", placement.String())
}

// make sure Error() method works properly
func TestToErrorString(t *testing.T) {
	resourceErr := InsufficientResourceError{Type: ResourceTypeMem, Wanted: parseQuantity("200M"), Free: parseQuantity("100M")}
	assert.Equal(t,
		"insufficient memory: wanted: 200M, free: 100M",
		resourceErr.Error())

	clusterCapErr := ClusterCapacityError{InsufficientResource: resourceErr}
	assert.Equal(t,
		"insufficient room on cluster nodes: insufficient memory: wanted: 200M, free: 100M",
		clusterCapErr.Error())

	nodeMismatchErr := NodeMismatchError{Node: "worker-1", PodName: "pod-1", Reason: fmt.Errorf("something failed")}
	assert.Equal(t,
		"pod pod-1 could not be scheduled onto node worker-1: something failed",
		nodeMismatchErr.Error())

	podSchedErr := PodSchedulingError{
		PodName: "pod-1",
		NodeMismatches: map[string]NodeMismatchError{
			"worker-1": NodeMismatchError{Node: "worker-1", PodName: "pod-1", Reason: fmt.Errorf("something failed")},
		},
	}
	assert.Equal(t,
		`failed to reschedule pod pod-1:
  worker-1: pod pod-1 could not be scheduled onto node worker-1: something failed
`,
		podSchedErr.Error())
}

func assertInsufficentResourceError(t *testing.T, expected *InsufficientResourceError, actual error) {
	assert.IsType(t, &InsufficientResourceError{}, actual)
	resourceErr := actual.(*InsufficientResourceError)

	assert.Equal(t, expected.Type, resourceErr.Type)
	assert.Equal(t, expected.Wanted.MilliValue(), resourceErr.Wanted.MilliValue())
	assert.Equal(t, expected.Free.MilliValue(), resourceErr.Free.MilliValue())

	// make sure Error() method works without panicking
	actual.Error()
}

func assertTaintTolerationError(t *testing.T, expected *TaintTolerationError, actual error) {
	assert.IsType(t, &TaintTolerationError{}, actual)
	taintErr := actual.(*TaintTolerationError)

	assert.Equal(t, expected.Node, taintErr.Node)
	assert.Equal(t, expected.PodName, taintErr.PodName)
	assert.Equal(t, expected.Taint.Key, taintErr.Taint.Key)
	assert.Equal(t, expected.Taint.Value, taintErr.Taint.Value)
	assert.Equal(t, expected.Taint.Effect, taintErr.Taint.Effect)

	// make sure Error() method works without panicking
	actual.Error()
}

func assertNodeSelectorMismatchError(t *testing.T, expected *NodeSelectorMismatchError, actual error) {
	assert.IsType(t, &NodeSelectorMismatchError{}, actual)
	selectorErr := actual.(*NodeSelectorMismatchError)

	assert.Equal(t, expected.Node, selectorErr.Node)
	assert.Equal(t, expected.Pod, selectorErr.Pod)

	// make sure Error() method works without panicking
	actual.Error()
}

func assertNodeAffinityMismatchError(t *testing.T, expected *NodeAffinityMismatchError, actual error) {
	assert.IsType(t, &NodeAffinityMismatchError{}, actual)
	affinityErr := actual.(*NodeAffinityMismatchError)

	assert.Equal(t, expected.Node, affinityErr.Node)
	assert.Equal(t, expected.Pod, affinityErr.Pod)

	// make sure Error() method works without panicking
	actual.Error()
}
