package policy

import (
	"testing"

	"github.com/letsencrypt/boulder/core"
	"github.com/letsencrypt/boulder/features"
	"github.com/letsencrypt/boulder/identifier"
	blog "github.com/letsencrypt/boulder/log"
	"github.com/letsencrypt/boulder/test"
)

func TestChallengeTypesForDNSAccount01(t *testing.T) {
	enabledChallenges := map[core.AcmeChallenge]bool{
		core.ChallengeTypeHTTP01:    true,
		core.ChallengeTypeDNS01:     true,
		core.ChallengeTypeTLSALPN01: true,
	}

	pa, err := New(enabledChallenges, blog.NewMock())
	test.AssertNotError(t, err, "Couldn't create policy implementation")

	regIdent := identifier.NewDNS("example.com")
	challenges, _ := pa.ChallengeTypesFor(regIdent)

	for _, challenge := range challenges {
		if challenge == core.ChallengeTypeDNSAccount01 {
			t.Error("DNS-ACCOUNT-01 challenge was offered despite being disabled")
		}
	}

	wildcardIdent := identifier.NewDNS("*.example.com")
	challenges, _ = pa.ChallengeTypesFor(wildcardIdent)

	for _, challenge := range challenges {
		if challenge == core.ChallengeTypeDNSAccount01 {
			t.Error("DNS-ACCOUNT-01 challenge was offered for wildcard despite being disabled")
		}
	}

	features.Set(features.Config{DNSAccount01Enabled: true})
	defer features.Reset()

	challenges, _ = pa.ChallengeTypesFor(regIdent)

	foundDNSAccount01 := false
	for _, challenge := range challenges {
		if challenge == core.ChallengeTypeDNSAccount01 {
			foundDNSAccount01 = true
			break
		}
	}
	if !foundDNSAccount01 {
		t.Error("DNS-ACCOUNT-01 challenge was not offered despite being enabled")
	}

	challenges, _ = pa.ChallengeTypesFor(wildcardIdent)

	foundDNSAccount01 = false
	for _, challenge := range challenges {
		if challenge == core.ChallengeTypeDNSAccount01 {
			foundDNSAccount01 = true
			break
		}
	}
	if !foundDNSAccount01 {
		t.Error("DNS-ACCOUNT-01 challenge was not offered for wildcard despite being enabled")
	}
}
