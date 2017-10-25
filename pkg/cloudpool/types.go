package cloudpool

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	MachineStateRequested   = "REQUESTED"
	MachineStateRejected    = "REJECTED"
	MachineStatePending     = "PENDING"
	MachineStateRunning     = "RUNNING"
	MachineStateTerminating = "TERMINATING"
	MachineStateTerminated  = "TERMINATED"
)

// ErrorMessage is a cloudpool REST API message sent to convey an error
type ErrorMessage struct {
	// Message is a human-readable message intended for presentation.
	Message string `json:"message"`
	// Detail holds error details (could be a stack-trace or more
	// specific error information)
	Detail string `json:"detail"`
}

func (err *ErrorMessage) Error() string {
	return err.String()
}

func (err *ErrorMessage) String() string {
	return fmt.Sprintf("%s: %s", err.Message, err.Detail)
}

// SetDesiredSizeMessage is a cloudpool REST API message sent when POSTing
// to /pool/size
type SetDesiredSizeMessage struct {
	// DesiredSize is the desiredSize of the cloudpool
	DesiredSize int `json:"desiredSize"`
}

func (s *SetDesiredSizeMessage) String() string {
	bytes, _ := json.MarshalIndent(s, "", "  ")
	return string(bytes)
}

// TerminateMachineMessage is a cloudpool REST API message sent when POSTing
// to /pool/terminate
type TerminateMachineMessage struct {
	// MachineID is the cloudprovider-specific id of the machine to be terminated.
	MachineID string `json:"machineId"`
	// DecrementDesiredSize specifies if the desired size of the machine pool
	// should be decremented after terminating the machine (that is, it
	// controls if a replacement machine should be launched)
	DecrementDesiredSize bool `json:"decrementDesiredSize"`
}

func (t *TerminateMachineMessage) String() string {
	bytes, _ := json.MarshalIndent(t, "", "  ")
	return string(bytes)
}

// PoolSize is a REST API message describes the current cloudpool size
type PoolSizeMessage struct {
	// Timestamp is the time at which the pool size observation was made.
	Timestamp time.Time `json:"timestamp"`
	// DesiredSize is the currently desired size of the machine pool.
	DesiredSize int `json:"desiredSize"`
	// Allocated is the number of allocated machines in the pool (machines in
	// state REQUESTED, PENDING or RUNNING).
	Allocated int `json:"allocated"`
	// Active is the number of active machines in the pool. That is, the number of
	// allocated machines that have also been marked with an active membership status.
	Active int `json:"active"`
}

func (p *PoolSizeMessage) String() string {
	bytes, _ := json.MarshalIndent(p, "", "  ")
	return string(bytes)
}

type MachinePoolMessage struct {
	// Machines are state snapshots of the members of the machine pool.
	Machines []Machine `json:"machines"`
	// Timestamp is the time at which the pool observation was made.
	Timestamp time.Time `json:"timestamp"`
}

func (p *MachinePoolMessage) String() string {
	bytes, _ := json.MarshalIndent(p, "", "  ")
	return string(bytes)
}

// Machine represents a machine in a MachinePoolMessage.
type Machine struct {
	// ID is the identifier of the machine.
	ID string `json:"id"`
	// MachineState represents the execution state of the machine.
	MachineState string `json:"machineState"`
	// MembershipStatus represents the membership status of the machine.
	MembershipStatus MembershipStatus `json:"membershipStatus"`
	// ServiceState is the operational state of the service running on the machine.
	ServiceState string `json:"serviceState"`
	// CloudProvider is the name of the cloud provider that this machine originates from, for example AWS-EC2.
	CloudProvider string `json:"cloudProvider"`
	// Region is the name of the cloud region/zone/data center where this machine is located.
	Region string `json:"region"`
	// MachineSize is the size of the machine. For example, m1.medium for an Amazon EC2 machine.
	MachineSize string `json:"machineSize"`
	// RequestTime represents the request time of the machine if one can be determined by the underlying infrastructure.
	RequestTime *time.Time `json:"requestTime"`
	// LaunchTime represents the launch time of the machine if it has been launched.
	LaunchTime *time.Time `json:"launchTime"`
	// PublicIPs is the list of public IP addresses associated with this machine.
	PublicIPs []string `json:"publicIps"`
	// PrivateIPs is the list of private IP addresses associated with this machine.
	PrivateIPs []string `json:"privateIps"`
	// Metadata is additional cloud provider-specific meta data about the machine.
	Metadata *json.RawMessage `json:"metadata"`
}

func (m Machine) String() string {
	bytes, _ := json.MarshalIndent(m, "", "  ")
	return string(bytes)
}

// MembershipStatus indicates if a Machine needs to be given special treatment.
// It can, for example, be set to protect the machine from being terminated (by
// settings its evictability status) or to mark a machine as being in need of
// replacement by flagging it as an inactive pool member.
type MembershipStatus struct {
	// Indicates if this is an active (working) MachinePool member.
	// A  false value indicates to the cloudpool that a replacement machine
	// needs to be launched.
	Active bool `json:"active"`
	// Evicatble indicates if this Machine is a blessed member of the MachinePool.
	// If true, the cloudpool may not terminate this machine.
	Evictable bool `json:"evictable"`
}

func (m *MembershipStatus) String() string {
	bytes, _ := json.MarshalIndent(m, "", "  ")
	return string(bytes)
}
