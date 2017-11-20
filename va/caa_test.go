package va

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/miekg/dns"

	"github.com/letsencrypt/boulder/core"
	"github.com/letsencrypt/boulder/probs"
	"github.com/letsencrypt/boulder/test"

	vapb "github.com/letsencrypt/boulder/va/proto"
)

// caaMockDNS implements the `dns.DNSClient` interface with a set of useful test
// answers for CAA queries.
type caaMockDNS struct{}

func (mock caaMockDNS) LookupTXT(_ context.Context, hostname string) ([]string, []string, error) {
	return nil, nil, nil
}

func (mock caaMockDNS) LookupHost(_ context.Context, hostname string) ([]net.IP, error) {
	ip := net.ParseIP("127.0.0.1")
	return []net.IP{ip}, nil
}

func (mock caaMockDNS) LookupMX(_ context.Context, domain string) ([]string, error) {
	return nil, nil
}

func (mock caaMockDNS) LookupCAA(_ context.Context, domain string) ([]*dns.CAA, error) {
	var results []*dns.CAA
	var record dns.CAA
	switch strings.TrimRight(domain, ".") {
	case "caa-timeout.com":
		return nil, fmt.Errorf("error")
	case "reserved.com":
		record.Tag = "issue"
		record.Value = "ca.com"
		results = append(results, &record)
	case "critical.com":
		record.Flag = 1
		record.Tag = "issue"
		record.Value = "ca.com"
		results = append(results, &record)
	case "present.com", "present.servfail.com":
		record.Tag = "issue"
		record.Value = "letsencrypt.org"
		results = append(results, &record)
	case "com":
		// com has no CAA records.
		return nil, nil
	case "servfail.com", "servfail.present.com":
		return results, fmt.Errorf("SERVFAIL")
	case "multi-crit-present.com":
		record.Flag = 1
		record.Tag = "issue"
		record.Value = "ca.com"
		results = append(results, &record)
		secondRecord := record
		secondRecord.Value = "letsencrypt.org"
		results = append(results, &secondRecord)
	case "unknown-critical.com":
		record.Flag = 128
		record.Tag = "foo"
		record.Value = "bar"
		results = append(results, &record)
	case "unknown-critical2.com":
		record.Flag = 1
		record.Tag = "foo"
		record.Value = "bar"
		results = append(results, &record)
	case "unknown-noncritical.com":
		record.Flag = 0x7E // all bits we don't treat as meaning "critical"
		record.Tag = "foo"
		record.Value = "bar"
		results = append(results, &record)
	case "present-with-parameter.com":
		record.Tag = "issue"
		record.Value = "  letsencrypt.org  ;foo=bar;baz=bar"
		results = append(results, &record)
	case "unsatisfiable.com":
		record.Tag = "issue"
		record.Value = ";"
		results = append(results, &record)
	}
	return results, nil
}

func TestCAATimeout(t *testing.T) {
	va, _ := setup(nil, 0)
	va.dnsClient = caaMockDNS{}
	err := va.checkCAA(ctx, core.AcmeIdentifier{Type: core.IdentifierDNS, Value: "caa-timeout.com"})
	if err.Type != probs.ConnectionProblem {
		t.Errorf("Expected timeout error type %s, got %s", probs.ConnectionProblem, err.Type)
	}
	expected := "error"
	if err.Detail != expected {
		t.Errorf("checkCAA: got %#v, expected %#v", err.Detail, expected)
	}
}

func TestCAAChecking(t *testing.T) {
	type CAATest struct {
		Domain  string
		Present bool
		Valid   bool
	}
	tests := []CAATest{
		// Reserved
		{"reserved.com", true, false},
		// Critical
		{"critical.com", true, false},
		{"nx.critical.com", true, false},
		// Good (absent)
		{"absent.com", false, true},
		{"example.co.uk", false, true},
		// Good (present)
		{"present.com", true, true},
		{"present.servfail.com", true, true},
		// Good (multiple critical, one matching)
		{"multi-crit-present.com", true, true},
		// Bad (unknown critical)
		{"unknown-critical.com", true, false},
		{"unknown-critical2.com", true, false},
		// Good (unknown noncritical, no issue/issuewild records)
		{"unknown-noncritical.com", true, true},
		// Good (issue record with unknown parameters)
		{"present-with-parameter.com", true, true},
		// Bad (unsatisfiable issue record)
		{"unsatisfiable.com", true, false},
	}

	va, _ := setup(nil, 0)
	va.dnsClient = caaMockDNS{}
	for _, caaTest := range tests {
		present, valid, err := va.checkCAARecords(ctx, core.AcmeIdentifier{Type: "dns", Value: caaTest.Domain})
		if err != nil {
			t.Errorf("checkCAARecords error for %s: %s", caaTest.Domain, err)
		}
		if present != caaTest.Present {
			t.Errorf("checkCAARecords presence mismatch for %s: got %t expected %t", caaTest.Domain, present, caaTest.Present)
		}
		if valid != caaTest.Valid {
			t.Errorf("checkCAARecords validity mismatch for %s: got %t expected %t", caaTest.Domain, valid, caaTest.Valid)
		}
	}

	present, valid, err := va.checkCAARecords(ctx, core.AcmeIdentifier{Type: "dns", Value: "servfail.com"})
	test.AssertError(t, err, "servfail.com")
	test.Assert(t, !present, "Present should be false")
	test.Assert(t, !valid, "Valid should be false")

	_, _, err = va.checkCAARecords(ctx, core.AcmeIdentifier{Type: "dns", Value: "servfail.com"})
	if err == nil {
		t.Errorf("Should have returned error on CAA lookup, but did not: %s", "servfail.com")
	}

	present, valid, err = va.checkCAARecords(ctx, core.AcmeIdentifier{Type: "dns", Value: "servfail.present.com"})
	test.AssertError(t, err, "servfail.present.com")
	test.Assert(t, !present, "Present should be false")
	test.Assert(t, !valid, "Valid should be false")

	_, _, err = va.checkCAARecords(ctx, core.AcmeIdentifier{Type: "dns", Value: "servfail.present.com"})
	if err == nil {
		t.Errorf("Should have returned error on CAA lookup, but did not: %s", "servfail.present.com")
	}
}

// TestIsCAAValidErrMessage tests that an error result from `va.IsCAAValid`
// includes the domain name that was being checked in the failure detail.
func TestIsCAAValidErrMessage(t *testing.T) {
	va, _ := setup(nil, 0)
	va.dnsClient = caaMockDNS{}

	// Call IsCAAValid with a domain we know fails with a generic error from the
	// caaMockDNS.
	domain := "caa-timeout.com"
	resp, err := va.IsCAAValid(ctx, &vapb.IsCAAValidRequest{
		Domain: &domain,
	})

	// The lookup itself should not return an error
	test.AssertNotError(t, err, "Unexpected error calling IsCAAValidRequest")
	// The result should not be nil
	test.AssertNotNil(t, resp, "Response to IsCAAValidRequest was nil")
	// The result's Problem should not be nil
	test.AssertNotNil(t, resp.Problem, "Response Problem was nil")
	// The result's Problem should be an error message that includes the domain.
	test.AssertEquals(t, *resp.Problem.Detail, fmt.Sprintf("While processing CAA for %s: error", domain))
}

func TestCAAFailure(t *testing.T) {
	chall := createChallenge(core.ChallengeTypeTLSSNI01)
	hs := tlssni01Srv(t, chall)
	defer hs.Close()

	va, _ := setup(hs, 0)
	va.dnsClient = caaMockDNS{}

	_, prob := va.validateChallengeAndCAA(ctx, dnsi("reserved.com"), chall)
	test.AssertEquals(t, prob.Type, probs.CAAProblem)
}

func TestParseResults(t *testing.T) {
	r := []caaResult{}
	s, err := parseResults(r)
	test.Assert(t, s == nil, "set is not nil")
	test.Assert(t, err == nil, "error is not nil")
	test.AssertNotError(t, err, "no error should be returned")
	r = []caaResult{{nil, errors.New("")}, {[]*dns.CAA{{Value: "test"}}, nil}}
	s, err = parseResults(r)
	test.Assert(t, s == nil, "set is not nil")
	test.AssertEquals(t, err.Error(), "")
	expected := dns.CAA{Value: "other-test"}
	r = []caaResult{{[]*dns.CAA{&expected}, nil}, {[]*dns.CAA{{Value: "test"}}, nil}}
	s, err = parseResults(r)
	test.AssertEquals(t, len(s.Unknown), 1)
	test.Assert(t, s.Unknown[0] == &expected, "Incorrect record returned")
	test.AssertNotError(t, err, "no error should be returned")
}
