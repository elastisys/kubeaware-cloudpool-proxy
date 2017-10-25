package kube

import (
	"encoding/json"

	"github.com/golang/glog"
	"k8s.io/apimachinery/pkg/api/resource"
	apiv1 "k8s.io/client-go/pkg/api/v1"
)

// NodeInfo carries extended information about a Node, in the form of all Pods
// scheduled onto the node.
type NodeInfo struct {
	apiv1.Node
	Pods []apiv1.Pod
}

// String fulfills the Stringer interface
func (n *NodeInfo) String() string {
	return n.Name
}

// DetailedString returns a detailed JSON representation of the node.
func (n *NodeInfo) DetailedString() string {
	bytes, _ := json.MarshalIndent(n, "", "    ")
	return string(bytes)
}

// FreeMem calculates the amount of free memory on the node (currently available for scheduling).
func (n *NodeInfo) FreeMem() resource.Quantity {
	free := n.AllocatableMem().DeepCopy()
	free.Sub(n.AllocatedMem())
	return free
}

// FreeCPU calculates the amount of free CPU on the node (currently available for scheduling).
func (n *NodeInfo) FreeCPU() resource.Quantity {
	free := n.AllocatableCPU().DeepCopy()
	free.Sub(n.AllocatedCPU())
	return free
}

// AllocatableCPU returns the total amount of allocatable memory on the node.
func (n *NodeInfo) AllocatableCPU() resource.Quantity {
	return n.Status.Allocatable["cpu"]
}

// AllocatedCPU returns the total amount of CPU allocated by pods on the node.
func (n *NodeInfo) AllocatedCPU() resource.Quantity {
	var totalCPU resource.Quantity
	for _, pod := range n.Pods {
		for _, container := range pod.Spec.Containers {
			requestedCPU := container.Resources.Requests["cpu"]
			totalCPU.Add(requestedCPU)
			glog.V(8).Infof("pod %s, container %s requested %s cpu", pod.Name, container.Name, requestedCPU.String())
		}
	}
	return totalCPU
}

// AllocatableMem returns the total amount of allocatable memory on the node.
func (n *NodeInfo) AllocatableMem() resource.Quantity {
	return n.Status.Allocatable["memory"]
}

// AllocatedMem returns the total amount of memory allocated by pods on the node.
func (n *NodeInfo) AllocatedMem() resource.Quantity {
	var totalMem resource.Quantity
	for _, pod := range n.Pods {
		for _, container := range pod.Spec.Containers {
			requestedMem := container.Resources.Requests["memory"]
			totalMem.Add(requestedMem)
			glog.V(8).Infof("pod %s, container %s requested %s mem", pod.Name, container.Name, requestedMem.String())
		}
	}
	return totalMem
}
