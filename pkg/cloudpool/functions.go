package cloudpool

// MachinePredicate is a predicate function that tests a Machine for a certain condition.
type MachinePredicate func(*Machine) bool

// Not is a machine predicate that always negates a given predicate.
func Not(predicate MachinePredicate) MachinePredicate {
	return func(machine *Machine) bool {
		return !predicate(machine)
	}
}

// IsMachineAllocated returns true if a machine is in one of machine
// states: `REQUESTED`, `PENDING`, or `RUNNING`.
func IsMachineAllocated(machine *Machine) bool {
	return machine.MachineState == MachineStateRequested ||
		machine.MachineState == MachineStatePending ||
		machine.MachineState == MachineStateRunning
}

// IsMachineActive is a predicate that returns true for any machine that is both allocated
// (in one of machine states: `REQUESTED`, `PENDING`, or `RUNNING`) and has an active
// membership status.
func IsMachineActive(machine *Machine) bool {
	return IsMachineAllocated(machine) && machine.MembershipStatus.Active
}

// FilterMachines filters a given list of machines, returning a list with
// only the machines that satisfy all predicates.
func FilterMachines(machines []Machine, predicates ...MachinePredicate) (filtered []Machine) {
	for _, machine := range machines {
		satisfiesAllPredicates := true
		for _, predicate := range predicates {
			if !predicate(&machine) {
				satisfiesAllPredicates = false
				break
			}
		}

		if satisfiesAllPredicates {
			filtered = append(filtered, machine)
		}
	}

	return filtered
}
