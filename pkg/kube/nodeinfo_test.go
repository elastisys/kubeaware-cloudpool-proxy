package kube

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"k8s.io/apimachinery/pkg/api/resource"
)

func TestNodeInfoToString(t *testing.T) {
	node := &NodeInfo{Node: *NewTestNode("node1").CPU("1000m").Mem("1000M").ToNode()}

	expectedStringRepr := node.ObjectMeta.Name
	assert.Equal(t, expectedStringRepr, node.String())
}

// Verify correctness of the NodeInfo resource calculation methods.
func TestNodeInfoResourceCalcuations(t *testing.T) {
	tests := []struct {
		name                   string
		node                   *NodeInfo
		expectedAllocatableCPU resource.Quantity
		expectedAllocatableMem resource.Quantity
		expectedAllocatedCPU   resource.Quantity
		expectedAllocatedMem   resource.Quantity
		expectedFreeCPU        resource.Quantity
		expectedFreeMem        resource.Quantity
	}{
		{
			name: "empty node",
			node: buildNodeInfo(NewTestNode("worker-1").CPU("2000m").Mem("4000M").ToNode()),
			expectedAllocatableCPU: *parseQuantity("2000m"),
			expectedAllocatableMem: *parseQuantity("4000M"),
			expectedAllocatedCPU:   *parseQuantity("0m"),
			expectedAllocatedMem:   *parseQuantity("0M"),
			expectedFreeCPU:        *parseQuantity("2000m"),
			expectedFreeMem:        *parseQuantity("4000M"),
		},
		{
			name: "empty node - pods without resource requests",
			node: buildNodeInfo(NewTestNode("worker-1").CPU("2000m").Mem("4000M").ToNode(),
				buildPod("pod1", "", ""), buildPod("pod2", "", "")),
			expectedAllocatableCPU: *parseQuantity("2000m"),
			expectedAllocatableMem: *parseQuantity("4000M"),
			expectedAllocatedCPU:   *parseQuantity("0m"),
			expectedAllocatedMem:   *parseQuantity("0M"),
			expectedFreeCPU:        *parseQuantity("2000m"),
			expectedFreeMem:        *parseQuantity("4000M"),
		},
		{
			name: "semi-full node - pods with resource requests",
			node: buildNodeInfo(NewTestNode("worker-1").CPU("2000m").Mem("4000M").ToNode(),
				buildPod("pod1", "250m", "500M"), buildPod("pod2", "1200m", "1000M")),
			expectedAllocatableCPU: *parseQuantity("2000m"),
			expectedAllocatableMem: *parseQuantity("4000M"),
			expectedAllocatedCPU:   *parseQuantity("1450m"),
			expectedAllocatedMem:   *parseQuantity("1500M"),
			expectedFreeCPU:        *parseQuantity("550m"),
			expectedFreeMem:        *parseQuantity("2500M"),
		},
		{
			name: "semi-full node - pods with and without resource requests",
			node: buildNodeInfo(NewTestNode("worker-1").CPU("2000m").Mem("4000M").ToNode(),
				buildPod("pod1", "250m", "500M"), buildPod("pod2", "", ""), buildPod("pod3", "500m", "750M")),
			expectedAllocatableCPU: *parseQuantity("2000m"),
			expectedAllocatableMem: *parseQuantity("4000M"),
			expectedAllocatedCPU:   *parseQuantity("750m"),
			expectedAllocatedMem:   *parseQuantity("1250M"),
			expectedFreeCPU:        *parseQuantity("1250m"),
			expectedFreeMem:        *parseQuantity("2750M"),
		},
		{
			name: "full node - single pod",
			node: buildNodeInfo(NewTestNode("worker-1").CPU("2000m").Mem("4000M").ToNode(),
				buildPod("pod1", "2000m", "4000M")),
			expectedAllocatableCPU: *parseQuantity("2000m"),
			expectedAllocatableMem: *parseQuantity("4000M"),
			expectedAllocatedCPU:   *parseQuantity("2000m"),
			expectedAllocatedMem:   *parseQuantity("4000M"),
			expectedFreeCPU:        *parseQuantity("0m"),
			expectedFreeMem:        *parseQuantity("0M"),
		},
	}

	for _, test := range tests {
		assert.Equal(t, 0, test.expectedAllocatableCPU.Cmp(test.node.AllocatableCPU()), "%s: expected and actual allocatable cpu differ", test.name)
		assert.Equal(t, 0, test.expectedAllocatableMem.Cmp(test.node.AllocatableMem()), "%s: expected and actual allocatable mem differ", test.name)
		assert.Equal(t, 0, test.expectedAllocatedCPU.Cmp(test.node.AllocatedCPU()), "%s: expected and actual allocated cpu differ", test.name)
		assert.Equal(t, 0, test.expectedAllocatedMem.Cmp(test.node.AllocatedMem()), "%s: expected and actual allocated mem differ", test.name)
		assert.Equal(t, 0, test.expectedFreeCPU.Cmp(test.node.FreeCPU()), "%s: expected and actual free cpu differ", test.name)
		assert.Equal(t, 0, test.expectedFreeMem.Cmp(test.node.FreeMem()), "%s: expected and actual free mem differ", test.name)
	}
}
