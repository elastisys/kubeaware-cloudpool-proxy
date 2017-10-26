package proxy

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"testing"
	"time"

	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/cloudpool"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiv1 "k8s.io/client-go/pkg/api/v1"

	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/kube"
	"github.com/elastisys/kubeaware-cloudpool-proxy/pkg/proxy/mocks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type TestMachine cloudpool.Machine

func NewTestMachine(id string) *TestMachine {
	return &TestMachine{
		ID:               id,
		MachineState:     cloudpool.MachineStateRunning,
		MembershipStatus: cloudpool.MembershipStatus{Active: true, Evictable: true},
	}
}

func (m *TestMachine) SetMachineState(state string) *TestMachine {
	m.MachineState = state
	return m
}

func (m *TestMachine) SetMembershipStatus(active, evictable bool) *TestMachine {
	m.MembershipStatus = cloudpool.MembershipStatus{Active: active, Evictable: evictable}
	return m
}

func (m *TestMachine) ToMachine() *cloudpool.Machine {
	machine := cloudpool.Machine(*m)
	return &machine
}

func buildTestNode(name string) *kube.NodeInfo {
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
	}

	return &kube.NodeInfo{Node: node}
}

// Calling Forward() on the proxy should simply pass the request to
// the CloudPoolClient and return whatever it returns.
func TestForward(t *testing.T) {
	mockCloudPool := new(mocks.CloudPoolClient)
	mockNodeScaler := new(mocks.NodeScaler)

	req, _ := http.NewRequest("GET", "https://cloudpool:8443/pool/size", nil)

	// set up mock expectations
	expectedResponse := &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       ioutil.NopCloser(bytes.NewReader([]byte(`{"key": "value"}`))),
	}
	mockCloudPool.On("Forward", req).Return(expectedResponse, nil)

	proxy := New(mockCloudPool, mockNodeScaler)
	resp, err := proxy.Forward(req)
	assert.Nil(t, err, "Forward failed unexpectedly")
	assert.Equal(t, expectedResponse, resp)

	// verify that call was passed on to cloudpool client
	mockCloudPool.AssertExpectations(t)
	mockNodeScaler.AssertExpectations(t)
}

// TerminateMachine should (after ensuring that the machine is both
// recognized as a cloudpool member and a kubernetes node, and is
// safe to evacuate) evacuate the node, delete it from kubernetes,
// and then terminate the node in the backend cloudpool.
func TestTerminateMachine(t *testing.T) {
	mockCloudPool := new(mocks.CloudPoolClient)
	mockNodeScaler := new(mocks.NodeScaler)

	// set up expectations
	machine1 := NewTestMachine("i-1").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	node1 := buildTestNode("i-1")

	kubeWorkers := []*kube.NodeInfo{node1}
	mockNodeScaler.On("ListWorkerNodes").Return(kubeWorkers, nil)
	mockCloudPool.On("GetMachine", "i-1").Return(machine1, nil)
	mockNodeScaler.On("IsScaleDownCandidate", node1).Return(true, nil)
	mockNodeScaler.On("DrainNode", node1).Return(nil)
	mockNodeScaler.On("DeleteNode", node1).Return(nil)
	mockCloudPool.On("TerminateMachine", "i-1", true).Return(nil)

	proxy := New(mockCloudPool, mockNodeScaler)
	err := proxy.TerminateMachine("i-1", true)
	assert.Nil(t, err, "TerminateMachine failed unexpectedly")

	//  verify that expected calls were made on the clients
	mockCloudPool.AssertExpectations(t)
	mockNodeScaler.AssertExpectations(t)
}

// TerminateMachine() should fail on an attempt to terminate a machine
// without a kubernetes node counterpart. Rationale: the machine may
// not (yet) have registered as a node in the cluster. To prevent a
// a race-condition where the node just manages to register before it
// gets terminated in the cloud (and kubernetes having to detect for
// itself that the machine no longer exists) we turn down the request.
// The request can be retried later, and in the case where the machine
// is badly configured (and never succeeds to register) it can be killed
// by other means.
func TestTerminateMachineNotInKubernetesCluster(t *testing.T) {
	mockCloudPool := new(mocks.CloudPoolClient)
	mockNodeScaler := new(mocks.NodeScaler)

	// set up expectations
	// no kubernetes node counterpart to machine i-1
	kubeWorkers := []*kube.NodeInfo{}
	mockNodeScaler.On("ListWorkerNodes").Return(kubeWorkers, nil)

	proxy := New(mockCloudPool, mockNodeScaler)
	err := proxy.TerminateMachine("i-1", true)
	assert.NotNil(t, err, "TerminateMachine expected to fail")
	assert.IsType(t, &NodeNotFoundError{}, err, "unexpected error type")

	// make sure error string is built without errors
	require.NotNil(t, err.Error())

	//  verify that expected calls were made on the clients
	mockCloudPool.AssertExpectations(t)
	mockNodeScaler.AssertExpectations(t)
}

// TerminateMachine() should fail on an attempt to terminate a node
// without a cloudpool counterpart. Rationale: we don't want to
// unintentionally leave a cloud VM running. The request can be
// retried later.
func TestTerminateMachineNotInCloudPool(t *testing.T) {
	mockCloudPool := new(mocks.CloudPoolClient)
	mockNodeScaler := new(mocks.NodeScaler)

	// set up expectations
	node1 := buildTestNode("i-1")

	kubeWorkers := []*kube.NodeInfo{node1}
	mockNodeScaler.On("ListWorkerNodes").Return(kubeWorkers, nil)
	mockCloudPool.On("GetMachine", "i-1").Return(nil, fmt.Errorf("machine not found"))

	proxy := New(mockCloudPool, mockNodeScaler)
	err := proxy.TerminateMachine("i-1", true)
	assert.NotNil(t, err, "TerminateMachine expected to fail")
	assert.IsType(t, &CloudPoolClientError{}, err, "unexpected error type")

	// make sure error string is built without errors
	require.NotNil(t, err.Error())

	//  verify that expected calls were made on the clients
	mockCloudPool.AssertExpectations(t)
	mockNodeScaler.AssertExpectations(t)
}

// If the NodeScaler deems a node not safe to scale down, the proxy
// must not proceed with draining the node.
func TestTerminateMachineThatIsNotSafeToScaleDown(t *testing.T) {
	mockCloudPool := new(mocks.CloudPoolClient)
	mockNodeScaler := new(mocks.NodeScaler)

	// set up expectations
	machine1 := NewTestMachine("i-1").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	node1 := buildTestNode("i-1")

	kubeWorkers := []*kube.NodeInfo{node1}
	mockNodeScaler.On("ListWorkerNodes").Return(kubeWorkers, nil)
	mockCloudPool.On("GetMachine", "i-1").Return(machine1, nil)
	// note: not a scale-down candidate
	mockNodeScaler.On("IsScaleDownCandidate", node1).Return(false, fmt.Errorf("node not ready"))

	proxy := New(mockCloudPool, mockNodeScaler)
	err := proxy.TerminateMachine("i-1", true)
	assert.NotNil(t, err, "TerminateMachine expected to fail")
	assert.IsType(t, &ScaleDownError{}, err, "unexpected error type")

	// make sure error string is built without errors
	require.NotNil(t, err.Error())

	//  verify that expected calls were made on the clients
	mockCloudPool.AssertExpectations(t)
	mockNodeScaler.AssertExpectations(t)
}

// When a larger machine pool size is requested, the request
// should be immediately forwarded to the backend cloudpool.
func TestSetDesiredSizeOnScaleUp(t *testing.T) {
	mockCloudPool := new(mocks.CloudPoolClient)
	mockNodeScaler := new(mocks.NodeScaler)

	now := time.Now().UTC()

	// set up expectations
	machine1 := NewTestMachine("i-1").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	machine2 := NewTestMachine("i-2").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	pool := &cloudpool.MachinePoolMessage{
		Machines:  []cloudpool.Machine{*machine1, *machine2},
		Timestamp: now,
	}
	node1 := buildTestNode("i-1")
	node2 := buildTestNode("i-2")

	kubeWorkers := []*kube.NodeInfo{node1, node2}
	mockNodeScaler.On("ListWorkerNodes").Return(kubeWorkers, nil)
	mockCloudPool.On("GetMachinePool").Return(pool, nil)
	// request should be forwarded to backend cloudpool
	mockCloudPool.On("SetDesiredSize", 3).Return(nil)

	proxy := New(mockCloudPool, mockNodeScaler)
	err := proxy.SetDesiredSize(3)
	assert.Nil(t, err, "SetDesiredSize failed unexpectedly")

	//  verify that expected calls were made on the clients
	mockCloudPool.AssertExpectations(t)
	mockNodeScaler.AssertExpectations(t)
}

// The Proxy should return a CloudPoolClientError on failure to pass
// on a SetDesiredSize request
func TestSetDesiredSizeForwardingFailure(t *testing.T) {
	mockCloudPool := new(mocks.CloudPoolClient)
	mockNodeScaler := new(mocks.NodeScaler)

	now := time.Now().UTC()

	// set up expectations
	machine1 := NewTestMachine("i-1").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	machine2 := NewTestMachine("i-2").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	pool := &cloudpool.MachinePoolMessage{
		Machines:  []cloudpool.Machine{*machine1, *machine2},
		Timestamp: now,
	}
	node1 := buildTestNode("i-1")
	node2 := buildTestNode("i-2")

	kubeWorkers := []*kube.NodeInfo{node1, node2}
	mockNodeScaler.On("ListWorkerNodes").Return(kubeWorkers, nil)
	mockCloudPool.On("GetMachinePool").Return(pool, nil)
	// note: CloudPoolClient fails to call SetDesiredSize()
	mockCloudPool.On("SetDesiredSize", 3).Return(fmt.Errorf("error occured"))

	proxy := New(mockCloudPool, mockNodeScaler)
	err := proxy.SetDesiredSize(3)
	assert.NotNil(t, err, "SetDesiredSize expected to fail")
	assert.IsType(t, &CloudPoolClientError{}, err, "unexpected error type")

	//  verify that expected calls were made on the clients
	mockCloudPool.AssertExpectations(t)
	mockNodeScaler.AssertExpectations(t)
}

// When an equally sized machine pool is requested, the request
// should be immediately forwarded to the backend cloudpool.
func TestSetDesiredSizeOnStayPut(t *testing.T) {
	mockCloudPool := new(mocks.CloudPoolClient)
	mockNodeScaler := new(mocks.NodeScaler)

	now := time.Now().UTC()

	// set up expectations
	machine1 := NewTestMachine("i-1").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	machine2 := NewTestMachine("i-2").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	pool := &cloudpool.MachinePoolMessage{
		Machines:  []cloudpool.Machine{*machine1, *machine2},
		Timestamp: now,
	}
	node1 := buildTestNode("i-1")
	node2 := buildTestNode("i-2")

	kubeWorkers := []*kube.NodeInfo{node1, node2}
	mockNodeScaler.On("ListWorkerNodes").Return(kubeWorkers, nil)
	mockCloudPool.On("GetMachinePool").Return(pool, nil)
	// request should be forwarded to backend cloudpool
	mockCloudPool.On("SetDesiredSize", 2).Return(nil)

	proxy := New(mockCloudPool, mockNodeScaler)
	err := proxy.SetDesiredSize(2)
	assert.Nil(t, err, "SetDesiredSize failed unexpectedly")

	//  verify that expected calls were made on the clients
	mockCloudPool.AssertExpectations(t)
	mockNodeScaler.AssertExpectations(t)
}

// Verify that when a SetDesiredSize call is received that suggests
// a scale-down the proxy checks to see if any scale-down candidates
// exist, selects one (the least loaded node), evacuates that node,
// deletes the node from Kubernetes, and then terminates the cloud
// machine via the cloudpool.
func TestSetDesiredSizeOnScaleDown(t *testing.T) {
	mockCloudPool := new(mocks.CloudPoolClient)
	mockNodeScaler := new(mocks.NodeScaler)

	now := time.Now().UTC()

	// set up expectations
	machine1 := NewTestMachine("i-1").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	machine2 := NewTestMachine("i-2").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	pool := &cloudpool.MachinePoolMessage{
		Machines:  []cloudpool.Machine{*machine1, *machine2},
		Timestamp: now,
	}
	node1 := buildTestNode("i-1")
	node2 := buildTestNode("i-2")

	kubeWorkers := []*kube.NodeInfo{node1, node2}
	mockNodeScaler.On("ListWorkerNodes").Return(kubeWorkers, nil)
	mockCloudPool.On("GetMachinePool").Return(pool, nil)
	mockNodeScaler.On("IsScaleDownCandidate", node1).Return(true, nil)
	mockNodeScaler.On("IsScaleDownCandidate", node2).Return(true, nil)
	mockNodeScaler.On("NodeLoad", node1).Return(0.5, nil)
	mockNodeScaler.On("NodeLoad", node2).Return(0.3, nil)
	mockNodeScaler.On("DrainNode", node2).Return(nil)
	mockNodeScaler.On("DeleteNode", node2).Return(nil)
	mockCloudPool.On("TerminateMachine", "i-2", true).Return(nil)

	proxy := New(mockCloudPool, mockNodeScaler)
	err := proxy.SetDesiredSize(1)
	assert.Nil(t, err, "SetDesiredSize failed unexpectedly")

	//  verify that expected calls were made on the clients
	mockCloudPool.AssertExpectations(t)
	mockNodeScaler.AssertExpectations(t)
}

// SetDesiredSize should fail on scale-down if no node can be found that can
// be "safely" scaled down.
func TestSetDesiredSizeOnScaleDownWhenNoSafeCandidateIsFound(t *testing.T) {
	mockCloudPool := new(mocks.CloudPoolClient)
	mockNodeScaler := new(mocks.NodeScaler)

	now := time.Now().UTC()

	// set up expectations
	machine1 := NewTestMachine("i-1").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	machine2 := NewTestMachine("i-2").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	pool := &cloudpool.MachinePoolMessage{
		Machines:  []cloudpool.Machine{*machine1, *machine2},
		Timestamp: now,
	}
	node1 := buildTestNode("i-1")
	node2 := buildTestNode("i-2")

	kubeWorkers := []*kube.NodeInfo{node1, node2}
	mockNodeScaler.On("ListWorkerNodes").Return(kubeWorkers, nil)
	mockCloudPool.On("GetMachinePool").Return(pool, nil)
	// note: NodeScaler deems nodes not safe to scale down
	mockNodeScaler.On("IsScaleDownCandidate", node1).Return(false, fmt.Errorf("node not ready"))
	mockNodeScaler.On("IsScaleDownCandidate", node2).Return(false, fmt.Errorf("node not ready"))

	proxy := New(mockCloudPool, mockNodeScaler)
	err := proxy.SetDesiredSize(1)
	assert.NotNil(t, err, "SetDesiredSize expectede to fail")
	assert.IsType(t, &NoViableScaleDownCandidateError{}, err, "unexpected error type")

	//  verify that expected calls were made on the clients
	mockCloudPool.AssertExpectations(t)
	mockNodeScaler.AssertExpectations(t)
}

// Only machines that are active (REQUESTED, PENDING, RUNNING) and
// with an evictable membership status are to be considered as
// scale-down candidates.
func TestSetDesiredSizeShouldOnlyConsiderActiveEvictableMachines(t *testing.T) {
	mockCloudPool := new(mocks.CloudPoolClient)
	mockNodeScaler := new(mocks.NodeScaler)

	now := time.Now().UTC()

	//
	// set up expectations
	//

	desiredSize := 2

	// note: not active (TERMINATING)
	machine1 := NewTestMachine("i-1").SetMachineState(cloudpool.MachineStateTerminating).
		SetMembershipStatus(true, true).ToMachine()
	// note: not evictable
	machine2 := NewTestMachine("i-2").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, false).ToMachine()
	machine3 := NewTestMachine("i-3").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	machine4 := NewTestMachine("i-4").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()

	pool := &cloudpool.MachinePoolMessage{
		Machines:  []cloudpool.Machine{*machine1, *machine2, *machine3, *machine4},
		Timestamp: now,
	}
	node1 := buildTestNode("i-1")
	node2 := buildTestNode("i-2")
	node3 := buildTestNode("i-3")
	node4 := buildTestNode("i-4")

	kubeWorkers := []*kube.NodeInfo{node1, node2, node3, node4}
	mockNodeScaler.On("ListWorkerNodes").Return(kubeWorkers, nil)
	mockCloudPool.On("GetMachinePool").Return(pool, nil)
	// note: by this time, node2 and node 3 should have been filtered out
	mockNodeScaler.On("IsScaleDownCandidate", node3).Return(true, nil)
	mockNodeScaler.On("IsScaleDownCandidate", node4).Return(true, nil)
	mockNodeScaler.On("NodeLoad", node3).Return(0.2, nil)
	mockNodeScaler.On("NodeLoad", node4).Return(0.3, nil)
	mockNodeScaler.On("DrainNode", node3).Return(nil)
	mockNodeScaler.On("DeleteNode", node3).Return(nil)
	mockCloudPool.On("TerminateMachine", "i-3", true).Return(nil)

	proxy := New(mockCloudPool, mockNodeScaler)
	err := proxy.SetDesiredSize(desiredSize)
	assert.Nil(t, err, "SetDesiredSize failed unexpectedly")

	//  verify that expected calls were made on the clients
	mockCloudPool.AssertExpectations(t)
	mockNodeScaler.AssertExpectations(t)
}

// When the new desired size decreases the size by more than one node,
// the proxy will *attempt* to find that many candidates to evacuate
// ad scale down.
func TestSetDesiredSizeOnMultiScaleDown(t *testing.T) {
	mockCloudPool := new(mocks.CloudPoolClient)
	mockNodeScaler := new(mocks.NodeScaler)

	now := time.Now().UTC()

	//
	// set up expectations
	//

	desiredSize := 2

	machine1 := NewTestMachine("i-1").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	machine2 := NewTestMachine("i-2").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	machine3 := NewTestMachine("i-3").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	machine4 := NewTestMachine("i-4").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()

	pool := &cloudpool.MachinePoolMessage{
		Machines:  []cloudpool.Machine{*machine1, *machine2, *machine3, *machine4},
		Timestamp: now,
	}
	node1 := buildTestNode("i-1")
	node2 := buildTestNode("i-2")
	node3 := buildTestNode("i-3")
	node4 := buildTestNode("i-4")

	kubeWorkers := []*kube.NodeInfo{node1, node2, node3, node4}
	mockNodeScaler.On("ListWorkerNodes").Return(kubeWorkers, nil)
	mockCloudPool.On("GetMachinePool").Return(pool, nil)
	mockNodeScaler.On("IsScaleDownCandidate", node1).Return(true, nil)
	// note: node2 is not a scale-down candidate
	mockNodeScaler.On("IsScaleDownCandidate", node2).Return(false, fmt.Errorf("node not ready"))
	mockNodeScaler.On("IsScaleDownCandidate", node3).Return(true, nil)
	mockNodeScaler.On("IsScaleDownCandidate", node4).Return(true, nil)
	mockNodeScaler.On("NodeLoad", node1).Return(0.5, nil)
	mockNodeScaler.On("NodeLoad", node3).Return(0.2, nil)
	mockNodeScaler.On("NodeLoad", node4).Return(0.3, nil)
	// note: should pick node3 for deletion first
	mockNodeScaler.On("DrainNode", node3).Return(nil)
	mockNodeScaler.On("DeleteNode", node3).Return(nil)
	mockCloudPool.On("TerminateMachine", "i-3", true).Return(nil)
	// note: should pick node4 for deletion second
	mockNodeScaler.On("DrainNode", node4).Return(nil)
	mockNodeScaler.On("DeleteNode", node4).Return(nil)
	mockCloudPool.On("TerminateMachine", "i-4", true).Return(nil)

	proxy := New(mockCloudPool, mockNodeScaler)
	err := proxy.SetDesiredSize(desiredSize)
	assert.Nil(t, err, "SetDesiredSize failed unexpectedly")

	//  verify that expected calls were made on the clients
	mockCloudPool.AssertExpectations(t)
	mockNodeScaler.AssertExpectations(t)
}

// Verify that errors indicate which type of interactions failed when
// either kubernetes or cloudpool interactions go wrong.
func TestSetDesiredSizeOnClientErrors(t *testing.T) {

	machine1 := NewTestMachine("i-1").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	machine2 := NewTestMachine("i-2").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	pool := &cloudpool.MachinePoolMessage{
		Machines:  []cloudpool.Machine{*machine1, *machine2},
		Timestamp: time.Now().UTC(),
	}
	node1 := buildTestNode("i-1")
	node2 := buildTestNode("i-2")
	kubeWorkers := []*kube.NodeInfo{node1, node2}

	tests := []struct {
		// function that prepares mocks with expectations and return values
		prepareMocks func(mockCloudPool *mocks.CloudPoolClient, mockNodeScaler *mocks.NodeScaler)
		// the type of error that the Proxy is expected to return for the given mock responses
		expectedErrorType interface{}
	}{
		// NodeScaler fails to list worker nodes
		{
			prepareMocks: func(mockCloudPool *mocks.CloudPoolClient, mockNodeScaler *mocks.NodeScaler) {
				mockNodeScaler.On("ListWorkerNodes").Return(nil, fmt.Errorf("error occured"))
				mockCloudPool.On("GetMachinePool").Return(pool, nil)
			},
			expectedErrorType: &KubernetesClientError{},
		},
		// CloudPoolClient fails to list machines
		{
			prepareMocks: func(mockCloudPool *mocks.CloudPoolClient, mockNodeScaler *mocks.NodeScaler) {
				mockNodeScaler.On("ListWorkerNodes").Return(kubeWorkers, nil)
				mockCloudPool.On("GetMachinePool").Return(nil, fmt.Errorf("error occured"))
			},
			expectedErrorType: &CloudPoolClientError{},
		},
	}

	for _, test := range tests {
		mockCloudPool := new(mocks.CloudPoolClient)
		mockNodeScaler := new(mocks.NodeScaler)

		test.prepareMocks(mockCloudPool, mockNodeScaler)

		proxy := New(mockCloudPool, mockNodeScaler)
		err := proxy.SetDesiredSize(1)
		assert.NotNil(t, err, "SetDesiredSize expected to fail")
		assert.IsType(t, test.expectedErrorType, err, "unexpected error type")
	}
}

// When a scale-down is suggested but not enough candidates exist that are
// deemed safe to scale down, the Proxy should limit the scale-down to only
// those nodes that are safe to evict.
func TestSetDesiredSizeScaledownWhenNotEnoughCandidatesExist(t *testing.T) {
	mockCloudPool := new(mocks.CloudPoolClient)
	mockNodeScaler := new(mocks.NodeScaler)

	//
	// set up expectations
	//

	desiredSize := 1

	machine1 := NewTestMachine("i-1").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	machine2 := NewTestMachine("i-2").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	machine3 := NewTestMachine("i-3").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	machine4 := NewTestMachine("i-4").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()

	pool := &cloudpool.MachinePoolMessage{
		Machines:  []cloudpool.Machine{*machine1, *machine2, *machine3, *machine4},
		Timestamp: time.Now().UTC(),
	}
	node1 := buildTestNode("i-1")
	node2 := buildTestNode("i-2")
	node3 := buildTestNode("i-3")
	node4 := buildTestNode("i-4")

	kubeWorkers := []*kube.NodeInfo{node1, node2, node3, node4}
	mockNodeScaler.On("ListWorkerNodes").Return(kubeWorkers, nil)
	mockCloudPool.On("GetMachinePool").Return(pool, nil)
	// note: node1 is not a scale-down candidate
	mockNodeScaler.On("IsScaleDownCandidate", node1).Return(false, fmt.Errorf("node not ready"))
	// note: node2 is not a scale-down candidate
	mockNodeScaler.On("IsScaleDownCandidate", node2).Return(false, fmt.Errorf("node not ready"))
	// note: node3 is not a scale-down candidate
	mockNodeScaler.On("IsScaleDownCandidate", node3).Return(false, fmt.Errorf("node not ready"))
	mockNodeScaler.On("IsScaleDownCandidate", node4).Return(true, nil)
	mockNodeScaler.On("NodeLoad", node4).Return(0.3, nil)
	// note: should only pick node4 for deletion
	mockNodeScaler.On("DrainNode", node4).Return(nil)
	mockNodeScaler.On("DeleteNode", node4).Return(nil)
	mockCloudPool.On("TerminateMachine", "i-4", true).Return(nil)

	proxy := New(mockCloudPool, mockNodeScaler)
	err := proxy.SetDesiredSize(desiredSize)
	assert.Nil(t, err, "SetDesiredSize failed unexpectedly")

	//  verify that expected calls were made on the clients
	mockCloudPool.AssertExpectations(t)
	mockNodeScaler.AssertExpectations(t)
}

// When a node eviction/machine termination fails, Proxy.SetDesiredSize()
// must return a ScaleDownError
func TestSetDesiredSizeOnScaleDownError(t *testing.T) {

	machine1 := NewTestMachine("i-1").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	machine2 := NewTestMachine("i-2").SetMachineState(cloudpool.MachineStateRunning).
		SetMembershipStatus(true, true).ToMachine()
	pool := &cloudpool.MachinePoolMessage{
		Machines:  []cloudpool.Machine{*machine1, *machine2},
		Timestamp: time.Now().UTC(),
	}
	node1 := buildTestNode("i-1")
	node2 := buildTestNode("i-2")
	kubeWorkers := []*kube.NodeInfo{node1, node2}

	tests := []struct {
		// function that prepares mocks with expectations and return values
		prepareMocks func(mockCloudPool *mocks.CloudPoolClient, mockNodeScaler *mocks.NodeScaler)
		// the type of error that the Proxy is expected to return for the given mock responses
		expectedErrorType interface{}
	}{
		// note: drain fails
		{
			prepareMocks: func(mockCloudPool *mocks.CloudPoolClient, mockNodeScaler *mocks.NodeScaler) {
				mockNodeScaler.On("ListWorkerNodes").Return(kubeWorkers, nil)
				mockCloudPool.On("GetMachinePool").Return(pool, nil)
				mockNodeScaler.On("IsScaleDownCandidate", node1).Return(true, nil)
				mockNodeScaler.On("IsScaleDownCandidate", node2).Return(true, nil)
				mockNodeScaler.On("NodeLoad", node1).Return(0.3, nil)
				mockNodeScaler.On("NodeLoad", node2).Return(0.2, nil)
				mockNodeScaler.On("DrainNode", node2).Return(fmt.Errorf("drain node failed"))
				mockNodeScaler.On("DeleteNode", node2).Return(nil)
				mockCloudPool.On("TerminateMachine", "i-2", true).Return(nil)

			},
			expectedErrorType: &ScaleDownError{},
		},
		// note: delete node fails
		{
			prepareMocks: func(mockCloudPool *mocks.CloudPoolClient, mockNodeScaler *mocks.NodeScaler) {
				mockNodeScaler.On("ListWorkerNodes").Return(kubeWorkers, nil)
				mockCloudPool.On("GetMachinePool").Return(pool, nil)
				mockNodeScaler.On("IsScaleDownCandidate", node1).Return(true, nil)
				mockNodeScaler.On("IsScaleDownCandidate", node2).Return(true, nil)
				mockNodeScaler.On("NodeLoad", node1).Return(0.3, nil)
				mockNodeScaler.On("NodeLoad", node2).Return(0.2, nil)
				mockNodeScaler.On("DrainNode", node2).Return(nil)
				mockNodeScaler.On("DeleteNode", node2).Return(fmt.Errorf("delete node failed"))
				mockCloudPool.On("TerminateMachine", "i-2", true).Return(nil)

			},
			expectedErrorType: &ScaleDownError{},
		},
		// note: terminate machine fails
		{
			prepareMocks: func(mockCloudPool *mocks.CloudPoolClient, mockNodeScaler *mocks.NodeScaler) {
				mockNodeScaler.On("ListWorkerNodes").Return(kubeWorkers, nil)
				mockCloudPool.On("GetMachinePool").Return(pool, nil)
				mockNodeScaler.On("IsScaleDownCandidate", node1).Return(true, nil)
				mockNodeScaler.On("IsScaleDownCandidate", node2).Return(true, nil)
				mockNodeScaler.On("NodeLoad", node1).Return(0.3, nil)
				mockNodeScaler.On("NodeLoad", node2).Return(0.2, nil)
				mockNodeScaler.On("DrainNode", node2).Return(nil)
				mockNodeScaler.On("DeleteNode", node2).Return(nil)
				mockCloudPool.On("TerminateMachine", "i-2", true).Return(fmt.Errorf("terminate machine failed"))

			},
			expectedErrorType: &ScaleDownError{},
		},
	}

	for _, test := range tests {
		mockCloudPool := new(mocks.CloudPoolClient)
		mockNodeScaler := new(mocks.NodeScaler)

		test.prepareMocks(mockCloudPool, mockNodeScaler)

		proxy := New(mockCloudPool, mockNodeScaler)
		err := proxy.SetDesiredSize(1)
		assert.NotNil(t, err, "SetDesiredSize expected to fail")
		assert.IsType(t, test.expectedErrorType, err, "unexpected error type")
	}
}
