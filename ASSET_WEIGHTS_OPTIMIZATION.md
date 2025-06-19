# Asset Weights Optimization for Historical Info

## Overview

This optimization addresses the performance issue in `TrackHistoricalInfo` where asset weights are calculated for every validator by iterating through all their delegations at each block end.

## Problem

Currently, `TrackHistoricalInfo`:
1. Gets the last 100 active validators
2. For each validator, calculates `GetValidatorAssetWeightsFromDelegations` by iterating through ALL delegations
3. This becomes expensive as the number of delegations grows

Additionally, inactive validators (those not in the current epoch's active set but still having delegations) are not tracked in historical info.

## Solution

### 1. Epoch-based Validator Tracking

- **Active Validators**: Current epoch validators (max 100)
- **Inactive Validators**: Validators with delegations but not currently active
- Both are stored in `HistoricalInfo` for complete asset weight tracking

### 2. Pre-calculated Asset Weights (Future Enhancement)

The foundation is laid for pre-calculated asset weights:
- Similar to the existing boost optimization in `delegation.go`
- Asset weights totals would be maintained incrementally during delegation operations
- Fallback to full calculation when cache is not available

### 3. Protobuf Changes

```protobuf
message HistoricalInfo {
  tendermint.types.Header header = 1;
  repeated Validator valset = 2;
  repeated Validator inactive_valset = 3;  // New field
}
```

## Implementation

### Files Modified

1. **proto/cosmos/staking/v1beta1/staking.proto**
   - Added `inactive_valset` field to `HistoricalInfo`

2. **x/staking/keeper/historical_info.go**
   - Added epoch-based tracking with `trackWithEpochs()`
   - Separated active and inactive validator handling
   - Added `optimizedAssetWeights()` for future optimization

3. **x/staking/types/historical_info.go**
   - Added `NewHistoricalInfoWithInactiveValidators()`

### Usage

The system automatically detects if epochs are enabled:
- **With epochs**: Separates active/inactive validators
- **Without epochs**: Uses legacy behavior

## Next Steps

1. **Regenerate Protobuf**: Run `buf generate` to update Go types
2. **Enable Pre-calculation**: Implement asset weights caching similar to boost optimization
3. **Update Queries**: Modify queries to handle both active and inactive validators

## Performance Impact

- **Before**: O(total_delegations) per validator per block
- **After**: O(1) per validator per block (with pre-calculation)
- **Storage**: Minimal increase for inactive validator tracking 