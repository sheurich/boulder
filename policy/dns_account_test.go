package policy

import (
	"testing"

	"github.com/letsencrypt/boulder/core"
	"github.com/letsencrypt/boulder/features"
	"github.com/letsencrypt/boulder/identifier"
	"github.com/letsencrypt/boulder/test"
)

func TestDNSAccount01FeatureFlag(t *testing.T) {
	pa := paImpl(t)

	// Test with feature flag disabled
	features.Reset()
	challenges, err := pa.ChallengeTypesFor(identifier.NewDNS("example.com"))
	test.AssertNotError(t, err, "Error getting challenge types")

	// Check that DNS-Account-01 is not included in the challenges
	for _, challengeType := range challenges {
		test.Assert(t, challengeType != core.ChallengeTypeDNSAccount01,
			"DNS-Account-01 challenge type should not be enabled when feature flag is disabled")
	}

	// Test with feature flag enabled
	features.Reset()
	features.Set(features.Config{DNSAccount01Challenge: true})
	challenges, err = pa.ChallengeTypesFor(identifier.NewDNS("example.com"))
	test.AssertNotError(t, err, "Error getting challenge types")

	// Check that DNS-Account-01 is included in the challenges
	found := false
	for _, challengeType := range challenges {
		if challengeType == core.ChallengeTypeDNSAccount01 {
			found = true
			break
		}
	}
	test.Assert(t, found, "DNS-Account-01 challenge type should be enabled when feature flag is enabled")

	// Reset features for other tests
	features.Reset()
}
