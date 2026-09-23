package tools

import "testing"

// The ladder's step caps are what an outer budget must cover: if this sum grows,
// callers deriving their budget from HardLadderBudget() follow automatically, and
// the last step can never be clipped before it runs.
func TestHardLadderBudgetMatchesTheStepSum(t *testing.T) {
	want := hardRenderTimeout + hardRenderTimeout + extendedRenderTimeout
	if got := HardLadderBudget(); got != want {
		t.Errorf("HardLadderBudget() = %v, want the step sum %v", got, want)
	}
	if waiting := extendedNetworkIdle + extendedSettle; HardLadderBudget() < waiting {
		t.Errorf("the extended-settle step asks for %v of waiting alone, which must fit inside %v", waiting, HardLadderBudget())
	}
}
