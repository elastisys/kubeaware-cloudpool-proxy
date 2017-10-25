package cloudpool

import (
	"reflect"
	"strings"
	"testing"
)

func machine(id string, machineState string) *Machine {
	return &Machine{
		ID:           id,
		MachineState: machineState,
	}
}

func TestIsMachineAllocatedPredicate(t *testing.T) {
	tests := []struct {
		machine  Machine
		expected bool
	}{
		{machine: *machine("i-1", MachineStateRejected), expected: false},
		{machine: *machine("i-2", MachineStateRequested), expected: true},
		{machine: *machine("i-3", MachineStatePending), expected: true},
		{machine: *machine("i-4", MachineStateRunning), expected: true},
		{machine: *machine("i-5", MachineStateTerminating), expected: false},
		{machine: *machine("i-6", MachineStateTerminated), expected: false},
	}
	for _, test := range tests {
		got := IsMachineAllocated(&test.machine)
		if got != test.expected {
			t.Errorf("machine state %s: got allocated: %v, expected allocated: %v", test.machine.MachineState, got, test.expected)
		}
	}
}

func TestIsMachineActivePredicate(t *testing.T) {
	tests := []struct {
		machine  *Machine
		expected bool
	}{
		{machine: &Machine{MachineState: MachineStateRejected, MembershipStatus: MembershipStatus{Active: true}}, expected: false},
		{machine: &Machine{MachineState: MachineStateRejected, MembershipStatus: MembershipStatus{Active: false}}, expected: false},

		{machine: &Machine{MachineState: MachineStateRequested, MembershipStatus: MembershipStatus{Active: true}}, expected: true},
		{machine: &Machine{MachineState: MachineStateRequested, MembershipStatus: MembershipStatus{Active: false}}, expected: false},

		{machine: &Machine{MachineState: MachineStatePending, MembershipStatus: MembershipStatus{Active: true}}, expected: true},
		{machine: &Machine{MachineState: MachineStatePending, MembershipStatus: MembershipStatus{Active: false}}, expected: false},

		{machine: &Machine{MachineState: MachineStateRunning, MembershipStatus: MembershipStatus{Active: true}}, expected: true},
		{machine: &Machine{MachineState: MachineStateRunning, MembershipStatus: MembershipStatus{Active: false}}, expected: false},

		{machine: &Machine{MachineState: MachineStateTerminating, MembershipStatus: MembershipStatus{Active: true}}, expected: false},
		{machine: &Machine{MachineState: MachineStateTerminating, MembershipStatus: MembershipStatus{Active: false}}, expected: false},

		{machine: &Machine{MachineState: MachineStateTerminated, MembershipStatus: MembershipStatus{Active: true}}, expected: false},
		{machine: &Machine{MachineState: MachineStateTerminated, MembershipStatus: MembershipStatus{Active: false}}, expected: false},
	}
	for _, test := range tests {
		got := IsMachineActive(test.machine)
		if got != test.expected {
			t.Errorf("machine %v: got active: %v, expected active: %v", test.machine, got, test.expected)
		}
	}
}

func TestNotPredicate(t *testing.T) {
	alwaysTruePredicate := func(machine *Machine) bool { return true }
	alwaysFalsePredicate := func(machine *Machine) bool { return false }

	m := &Machine{}
	if Not(alwaysTruePredicate)(m) != false {
		t.Errorf("Not on a predicate returning true should always return false")
	}

	if Not(alwaysFalsePredicate)(m) != true {
		t.Errorf("Not on a predicate returning false should always return true")
	}
}

func TestFilterMachines(t *testing.T) {
	// two simple predicates
	startsWithI := func(machine *Machine) bool { return strings.HasPrefix(machine.ID, "i") }
	contains3 := func(machine *Machine) bool { return strings.Contains(machine.ID, "3") }

	tests := []struct {
		name       string
		predicates []MachinePredicate
		input      []Machine
		expected   []Machine
	}{
		// no predicates
		{
			name:       "no predicates",
			predicates: []MachinePredicate{},
			input:      []Machine{Machine{ID: "i-1"}, Machine{ID: "i-2"}, Machine{ID: "i-3"}},
			expected:   []Machine{Machine{ID: "i-1"}, Machine{ID: "i-2"}, Machine{ID: "i-3"}},
		},
		// single predicate
		{
			name:       "single predicate",
			predicates: []MachinePredicate{startsWithI},
			input:      []Machine{Machine{ID: "i-1"}, Machine{ID: "x-2"}, Machine{ID: "x-3"}},
			expected:   []Machine{Machine{ID: "i-1"}},
		},
		// multiple predicates: one entry maching zero predicates, one matching one, and one satisyfying all predicates
		{
			name:       "multiple predicates",
			predicates: []MachinePredicate{startsWithI, contains3},
			input:      []Machine{Machine{ID: "i-1"}, Machine{ID: "x-2"}, Machine{ID: "i-3"}},
			expected:   []Machine{Machine{ID: "i-3"}},
		},
	}

	for _, test := range tests {
		got := FilterMachines(test.input, test.predicates...)
		if !reflect.DeepEqual(got, test.expected) {
			t.Errorf("%s: got %s, expected %s", test.name, got, test.expected)
		}
	}
}
