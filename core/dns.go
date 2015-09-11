// Copyright 2015 ISRG.  All rights reserved
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package core

import (
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"

	"github.com/letsencrypt/boulder/Godeps/_workspace/src/github.com/miekg/dns"
)

var (
	// Private CIDRs to ignore per RFC1918 and RFC5735

	// RFC1918
	// 10.0.0.0/8
	rfc1918_10 = net.IPNet{
		IP:   []byte{10, 0, 0, 0},
		Mask: []byte{255, 0, 0, 0},
	}
	// 172.16.0.0/12
	rfc1918_172_16 = net.IPNet{
		IP:   []byte{172, 16, 0, 0},
		Mask: []byte{255, 240, 0, 0},
	}
	// 192.168.0.0/16
	rfc1918_192_168 = net.IPNet{
		IP:   []byte{192, 168, 0, 0},
		Mask: []byte{255, 255, 0, 0},
	}

	// RFC5735
	// 127.0.0.0/8
	rfc5735_127 = net.IPNet{
		IP:   []byte{127, 0, 0, 0},
		Mask: []byte{255, 0, 0, 0},
	}
)

// DNSResolverImpl represents a client that talks to an external resolver
type DNSResolverImpl struct {
	DNSClient                *dns.Client
	Servers                  []string
	allowRestrictedAddresses bool
}

// NewDNSResolverImpl constructs a new DNS resolver object that utilizes the
// provided list of DNS servers for resolution.
func NewDNSResolverImpl(dialTimeout time.Duration, servers []string) *DNSResolverImpl {
	dnsClient := new(dns.Client)

	// Set timeout for underlying net.Conn
	dnsClient.DialTimeout = dialTimeout

	return &DNSResolverImpl{
		DNSClient:                dnsClient,
		Servers:                  servers,
		allowRestrictedAddresses: false,
	}
}

// NewTestDNSResolverImpl constructs a new DNS resolver object that utilizes the
// provided list of DNS servers for resolution and will allow loopback addresses.
// This constructor should *only* be called from tests (unit or integration).
func NewTestDNSResolverImpl(dialTimeout time.Duration, servers []string) *DNSResolverImpl {
	resolver := NewDNSResolverImpl(dialTimeout, servers)
	resolver.allowRestrictedAddresses = true
	return resolver
}

// ExchangeOne performs a single DNS exchange with a randomly chosen server
// out of the server list, returning the response, time, and error (if any).
// This method sets the DNSSEC OK bit on the message to true before sending
// it to the resolver in case validation isn't the resolvers default behaviour.
func (dnsResolver *DNSResolverImpl) ExchangeOne(hostname string, qtype uint16) (rsp *dns.Msg, rtt time.Duration, err error) {
	m := new(dns.Msg)
	// Set question type
	m.SetQuestion(dns.Fqdn(hostname), qtype)
	// Set DNSSEC OK bit for resolver
	m.SetEdns0(4096, true)

	if len(dnsResolver.Servers) < 1 {
		err = fmt.Errorf("Not configured with at least one DNS Server")
		return
	}

	// Randomly pick a server
	chosenServer := dnsResolver.Servers[rand.Intn(len(dnsResolver.Servers))]

	return dnsResolver.DNSClient.Exchange(m, chosenServer)
}

// LookupTXT sends a DNS query to find all TXT records associated with
// the provided hostname.
func (dnsResolver *DNSResolverImpl) LookupTXT(hostname string) ([]string, time.Duration, error) {
	var txt []string
	r, rtt, err := dnsResolver.ExchangeOne(hostname, dns.TypeTXT)
	if err != nil {
		return nil, 0, err
	}
	if r.Rcode != dns.RcodeSuccess {
		err = fmt.Errorf("DNS failure: %d-%s for TXT query", r.Rcode, dns.RcodeToString[r.Rcode])
		return nil, 0, err
	}

	for _, answer := range r.Answer {
		if answer.Header().Rrtype == dns.TypeTXT {
			if txtRec, ok := answer.(*dns.TXT); ok {
				txt = append(txt, strings.Join(txtRec.Txt, ""))
			}
		}
	}

	return txt, rtt, err
}

func isPrivateV4(ip net.IP) bool {
	return rfc1918_10.Contains(ip) || rfc1918_172_16.Contains(ip) || rfc1918_192_168.Contains(ip) || rfc5735_127.Contains(ip)
}

// LookupHost sends a DNS query to find all A records associated with the provided
// hostname. This method assumes that the external resolver will chase CNAME/DNAME
// aliases and return relevant A records.
func (dnsResolver *DNSResolverImpl) LookupHost(hostname string) ([]net.IP, time.Duration, error) {
	var addrs []net.IP

	r, rtt, err := dnsResolver.ExchangeOne(hostname, dns.TypeA)
	if err != nil {
		return addrs, rtt, err
	}
	if r.Rcode != dns.RcodeSuccess {
		err = fmt.Errorf("DNS failure: %d-%s for A query", r.Rcode, dns.RcodeToString[r.Rcode])
		return nil, rtt, err
	}

	for _, answer := range r.Answer {
		if answer.Header().Rrtype == dns.TypeA {
			if a, ok := answer.(*dns.A); ok && a.A.To4() != nil && (!isPrivateV4(a.A) || dnsResolver.allowRestrictedAddresses) {
				addrs = append(addrs, a.A)
			}
		}
	}

	return addrs, rtt, nil
}

// LookupCNAME returns the target name if a CNAME record exists for
// the given domain name. If the CNAME does not exist (NXDOMAIN,
// NXRRSET, or a successful response with no CNAME records), it
// returns the empty string and a nil error.
func (dnsResolver *DNSResolverImpl) LookupCNAME(hostname string) (string, time.Duration, error) {
	r, rtt, err := dnsResolver.ExchangeOne(hostname, dns.TypeCNAME)
	if err != nil {
		return "", 0, err
	}
	if r.Rcode == dns.RcodeNXRrset || r.Rcode == dns.RcodeNameError {
		return "", rtt, nil
	}
	if r.Rcode != dns.RcodeSuccess {
		err = fmt.Errorf("DNS failure: %d-%s for CNAME query", r.Rcode, dns.RcodeToString[r.Rcode])
		return "", rtt, err
	}

	for _, answer := range r.Answer {
		if cname, ok := answer.(*dns.CNAME); ok {
			return cname.Target, rtt, nil
		}
	}

	return "", rtt, nil
}

// LookupDNAME is LookupCNAME, but for DNAME.
func (dnsResolver *DNSResolverImpl) LookupDNAME(hostname string) (string, time.Duration, error) {
	r, rtt, err := dnsResolver.ExchangeOne(hostname, dns.TypeDNAME)
	if err != nil {
		return "", 0, err
	}
	if r.Rcode == dns.RcodeNXRrset || r.Rcode == dns.RcodeNameError {
		return "", rtt, nil
	}
	if r.Rcode != dns.RcodeSuccess {
		err = fmt.Errorf("DNS failure: %d-%s for DNAME query", r.Rcode, dns.RcodeToString[r.Rcode])
		return "", rtt, err
	}

	for _, answer := range r.Answer {
		if cname, ok := answer.(*dns.DNAME); ok {
			return cname.Target, rtt, nil
		}
	}

	return "", rtt, nil
}

// LookupCAA sends a DNS query to find all CAA records associated with
// the provided hostname. If the response code from the resolver is
// SERVFAIL an empty slice of CAA records is returned.
func (dnsResolver *DNSResolverImpl) LookupCAA(hostname string) ([]*dns.CAA, time.Duration, error) {
	r, rtt, err := dnsResolver.ExchangeOne(hostname, dns.TypeCAA)
	if err != nil {
		return nil, 0, err
	}

	// On resolver validation failure, or other server failures, return empty an
	// set and no error.
	var CAAs []*dns.CAA
	if r.Rcode == dns.RcodeServerFailure {
		return CAAs, rtt, nil
	}

	for _, answer := range r.Answer {
		if answer.Header().Rrtype == dns.TypeCAA {
			if caaR, ok := answer.(*dns.CAA); ok {
				CAAs = append(CAAs, caaR)
			}
		}
	}
	return CAAs, rtt, nil
}

// LookupMX sends a DNS query to find a MX record associated hostname and returns the
// record target.
func (dnsResolver *DNSResolverImpl) LookupMX(hostname string) ([]string, time.Duration, error) {
	r, rtt, err := dnsResolver.ExchangeOne(hostname, dns.TypeMX)
	if err != nil {
		return nil, 0, err
	}
	if r.Rcode != dns.RcodeSuccess {
		err = fmt.Errorf("DNS failure: %d-%s for MX query", r.Rcode, dns.RcodeToString[r.Rcode])
		return nil, rtt, err
	}

	var results []string
	for _, answer := range r.Answer {
		if mx, ok := answer.(*dns.MX); ok {
			results = append(results, mx.Mx)
		}
	}

	return results, rtt, nil
}
