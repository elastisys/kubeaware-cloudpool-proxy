package cloudpool

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// Verify the validation of TerminateMachineMessages
func TestValidateTerminateMachineMessage(t *testing.T) {
	// TerminateMachineMessage with MachineID => should pass validation
	m := TerminateMachineMessage{MachineID: "i-1234567"}
	err := m.Validate()
	require.Nil(t, err, "expected validation to succeed")

	// TerminateMachineMessage missing MachineID => should fail validation
	m = TerminateMachineMessage{}
	err = m.Validate()
	require.NotNil(t, err, "expected validation to fail due to missing machineId")
	require.Equal(t, fmt.Errorf("terminateMachine message did not specify a machineId"), err, "unexpected validation error")
}

// Verify the validation of SetDesiredSizeMessages
func TestValidateSetDesiredSizeMessage(t *testing.T) {
	// SetDesiredSizeMessage with desiredSize => should pass validation
	desiredSize := 1
	m := SetDesiredSizeMessage{DesiredSize: &desiredSize}
	err := m.Validate()
	require.Nil(t, err, "expected validation to succeed")

	// SetDesiredSizeMessage missing desiredSize => should fail validation
	m = SetDesiredSizeMessage{}
	err = m.Validate()
	require.NotNil(t, err, "expected validation to fail due to missing desiredSize")
	require.Equal(t, fmt.Errorf("setDesiredSize message did not specify a desiredSize"), err, "unexpected validation error")

	// SetDesiredSizeMessage with negative desiredSize => should fail validation
	desiredSize = -1
	m = SetDesiredSizeMessage{DesiredSize: &desiredSize}
	err = m.Validate()
	require.NotNil(t, err, "expected validation to fail due to negative desiredSize")
	require.Equal(t, fmt.Errorf("setDesiredSize message: desiredSize must be non-negative"), err, "unexpected validation error")
}
