package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyActivity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		category         EventCategory
		delta            string
		addressIsWatched bool
		expected         ActivityType
	}{
		{"TRANSFER positive watched → DEPOSIT", EventCategoryTransfer, "100", true, ActivityDeposit},
		{"TRANSFER negative watched → WITHDRAWAL", EventCategoryTransfer, "-50", true, ActivityWithdrawal},
		{"TRANSFER zero watched → WITHDRAWAL", EventCategoryTransfer, "0", true, ActivityWithdrawal},
		{"TRANSFER positive not-watched → OTHER", EventCategoryTransfer, "100", false, ActivityOther},
		{"STAKE negative → STAKE", EventCategoryStake, "-1000", true, ActivityStake},
		{"STAKE positive → UNSTAKE", EventCategoryStake, "1000", true, ActivityUnstake},
		{"STAKE zero → STAKE", EventCategoryStake, "0", true, ActivityStake},
		{"REWARD → CLAIM_REWARD", EventCategoryReward, "500", true, ActivityClaimReward},
		{"FEE → FEE", EventCategoryFee, "-100", true, ActivityFee},
		{"fee_execution_l2 → FEE_EXECUTION_L2", EventCategoryFeeExecutionL2, "-200", true, ActivityFeeExecutionL2},
		{"fee_data_l1 → FEE_DATA_L1", EventCategoryFeeDataL1, "-50", true, ActivityFeeDataL1},
		{"SWAP → SWAP", EventCategorySwap, "100", true, ActivitySwap},
		{"MINT → MINT", EventCategoryMint, "1000", true, ActivityMint},
		{"BURN → BURN", EventCategoryBurn, "-500", true, ActivityBurn},
		{"unknown → OTHER", EventCategory("UNKNOWN"), "100", true, ActivityOther},
		{"empty category → OTHER", EventCategory(""), "100", true, ActivityOther},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ClassifyActivity(tc.category, tc.delta, tc.addressIsWatched)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestDeltaIsPositive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		delta    string
		positive bool
	}{
		{"100", true},
		{"1", true},
		{"-1", false},
		{"-100", false},
		{"0", false},
		{"", false},
		{"invalid", false},
		{" 42 ", true},
	}

	for _, tc := range tests {
		t.Run(tc.delta, func(t *testing.T) {
			assert.Equal(t, tc.positive, deltaIsPositive(tc.delta))
		})
	}
}
