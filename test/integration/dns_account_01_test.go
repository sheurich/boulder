

package integration

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"testing"

	"github.com/eggsampler/acme/v3"
	"github.com/letsencrypt/boulder/features"
	challTestSrvClient "github.com/letsencrypt/boulder/test/chall-test-srv-client"
)

func TestDNSAccount01HappyPath(t *testing.T) {
	t.Parallel()
	
	features.Set(features.Config{DNSAccount01Enabled: true})
	defer features.Reset()
	
	domain := random_domain()
	c, err := makeClient()
	if err != nil {
		t.Fatalf("creating client: %s", err)
	}
	
	idents := []acme.Identifier{{Type: "dns", Value: domain}}
	
	order, err := c.Client.NewOrder(c.Account, idents)
	if err != nil {
		t.Fatalf("creating new order: %s", err)
	}
	
	authzURL := order.Authorizations[0]
	auth, err := c.Client.FetchAuthorization(c.Account, authzURL)
	if err != nil {
		t.Fatalf("fetching authorization: %s", err)
	}
	
	chal, ok := auth.ChallengeMap[acme.ChallengeTypeDNS01]
	if !ok {
		t.Fatalf("no DNS challenge found")
	}
	
	accountURL := c.Account.URL
	hashBytes := sha256.Sum256([]byte(accountURL))
	label := base32.StdEncoding.EncodeToString(hashBytes[:10])
	
	validationName := "_" + label + "._acme-challenge." + domain
	
	_, err = challTestSrvClient.NewClient("").AddDNS01Response(validationName, chal.KeyAuthorization)
	if err != nil {
		t.Fatalf("adding DNS response: %s", err)
	}
	
	_, err = c.Client.UpdateChallenge(c.Account, chal)
	if err != nil {
		t.Fatalf("updating challenge: %s", err)
	}
	
	_, err = challTestSrvClient.NewClient("").RemoveDNS01Response(validationName)
	if err != nil {
		t.Fatalf("removing DNS response: %s", err)
	}
	
	csrKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %s", err)
	}
	
	csr, err := makeCSR(csrKey, idents, true)
	if err != nil {
		t.Fatalf("making CSR: %s", err)
	}
	
	order, err = c.Client.FinalizeOrder(c.Account, order, csr)
	if err != nil {
		t.Fatalf("finalizing order: %s", err)
	}
	
	certs, err := c.Client.FetchCertificates(c.Account, order.Certificate)
	if err != nil {
		t.Fatalf("fetching certificates: %s", err)
	}
	
	if len(certs) == 0 {
		t.Fatal("no certificates returned")
	}
	
	found := false
	for _, name := range certs[0].DNSNames {
		if name == domain {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("certificate doesn't contain domain %s", domain)
	}
}

func TestDNSAccount01FeatureDisabled(t *testing.T) {
	t.Parallel()
	
	features.Set(features.Config{DNSAccount01Enabled: false})
	defer features.Reset()
	
	domain := random_domain()
	c, err := makeClient()
	if err != nil {
		t.Fatalf("creating client: %s", err)
	}
	
	idents := []acme.Identifier{{Type: "dns", Value: domain}}
	
	order, err := c.Client.NewOrder(c.Account, idents)
	if err != nil {
		t.Fatalf("creating new order: %s", err)
	}
	
	authzURL := order.Authorizations[0]
	auth, err := c.Client.FetchAuthorization(c.Account, authzURL)
	if err != nil {
		t.Fatalf("fetching authorization: %s", err)
	}
	
	chal, ok := auth.ChallengeMap[acme.ChallengeTypeDNS01]
	if !ok {
		t.Fatalf("no DNS challenge found")
	}
	
	accountURL := c.Account.URL
	hashBytes := sha256.Sum256([]byte(accountURL))
	label := base32.StdEncoding.EncodeToString(hashBytes[:10])
	
	validationName := "_" + label + "._acme-challenge." + domain
	
	_, err = challTestSrvClient.NewClient("").AddDNS01Response(validationName, chal.KeyAuthorization)
	if err != nil {
		t.Fatalf("adding DNS response: %s", err)
	}
	
	_, err = c.Client.UpdateChallenge(c.Account, chal)
	if err == nil {
		t.Fatal("expected challenge to fail when feature is disabled")
	}
	
	_, err = challTestSrvClient.NewClient("").RemoveDNS01Response(validationName)
	if err != nil {
		t.Fatalf("removing DNS response: %s", err)
	}
}
