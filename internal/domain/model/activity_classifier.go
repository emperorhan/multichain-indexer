package model

import (
	"math/big"
	"strings"
)

// ClassifyActivity maps an EventCategory + delta sign + watched-address
// membership into one of the 12 canonical ActivityType values.
func ClassifyActivity(category EventCategory, delta string, addressIsWatched bool) ActivityType {
	switch category {
	case EventCategoryTransfer:
		if !addressIsWatched {
			return ActivityOther
		}
		if deltaIsPositive(delta) {
			return ActivityDeposit
		}
		return ActivityWithdrawal

	case EventCategoryStake:
		if deltaIsPositive(delta) {
			return ActivityUnstake
		}
		return ActivityStake

	case EventCategoryReward:
		return ActivityClaimReward

	case EventCategoryFee:
		return ActivityFee

	case EventCategoryFeeExecutionL2:
		return ActivityFeeExecutionL2

	case EventCategoryFeeDataL1:
		return ActivityFeeDataL1

	case EventCategorySwap:
		return ActivitySwap

	case EventCategoryMint:
		return ActivityMint

	case EventCategoryBurn:
		return ActivityBurn

	default:
		return ActivityOther
	}
}

func deltaIsPositive(delta string) bool {
	trimmed := strings.TrimSpace(delta)
	if trimmed == "" {
		return false
	}
	v := new(big.Int)
	if _, ok := v.SetString(trimmed, 10); !ok {
		return false
	}
	return v.Sign() > 0
}
