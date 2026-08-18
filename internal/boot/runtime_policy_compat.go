package boot

import (
	"corvus/internal/capability"
	"corvus/internal/runtimepolicy"
)

// deriveLegacyProfile maps resolved runtime policy to the legacy capability
// profile enum. This temporary adapter preserves skill filtering and
// capability routing behavior during migration.
func deriveLegacyProfile(policy runtimepolicy.Policy) capability.Profile {
	if policy.Exposure == runtimepolicy.ExposureDeferred {
		return capability.ProfileEconomy
	}
	if policy.Completion == runtimepolicy.CompletionVerified {
		return capability.ProfileDelivery
	}
	return capability.ProfileBalanced
}
