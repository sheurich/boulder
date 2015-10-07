// Copyright 2015 ISRG.  All rights reserved
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package publisher

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/letsencrypt/boulder/core"
	"github.com/letsencrypt/boulder/mocks"
	"github.com/letsencrypt/boulder/test"
)

var testLeaf = `-----BEGIN CERTIFICATE-----
MIIHAjCCBeqgAwIBAgIQfwAAAQAAAUtRVNy9a8fMcDANBgkqhkiG9w0BAQsFADBa
MQswCQYDVQQGEwJVUzESMBAGA1UEChMJSWRlblRydXN0MRcwFQYDVQQLEw5UcnVz
dElEIFNlcnZlcjEeMBwGA1UEAxMVVHJ1c3RJRCBTZXJ2ZXIgQ0EgQTUyMB4XDTE1
MDIwMzIxMjQ1MVoXDTE4MDIwMjIxMjQ1MVowfzEYMBYGA1UEAxMPbGV0c2VuY3J5
cHQub3JnMSkwJwYDVQQKEyBJTlRFUk5FVCBTRUNVUklUWSBSRVNFQVJDSCBHUk9V
UDEWMBQGA1UEBxMNTW91bnRhaW4gVmlldzETMBEGA1UECBMKQ2FsaWZvcm5pYTEL
MAkGA1UEBhMCVVMwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQDGE6T8
LcmS6g8lH/1Y5orXeZOva4gthrS+VmJUWlz3K4Er5q8CmVFTmD/rYL6tA31JYCAi
p2bVQ8z/PgWYGosuMzox2OO9MqnLwTTG074sCHTZi4foFb6KacS8xVu25u8RRBd8
1WJNlw736FO0pJUkkE3gDSPz1QTpw3gc6n7SyppaFr40D5PpK3PPoNCPfoz2bFtH
m2KRsUH924LRfitUZdI68kxJP7QG1SAbdZxA/qDcfvDSgCYW5WNmMKS4v+GHuMkJ
gBe20tML+hItmF5S9mYm/GbkFLG8YwWZrytUZrSjxmuL9nj3MaBrAPQw3/T582ry
KM8+z188kbnA7A+BAgMBAAGjggOdMIIDmTAOBgNVHQ8BAf8EBAMCBaAwggInBgNV
HSAEggIeMIICGjCCAQsGCmCGSAGG+S8ABgMwgfwwQAYIKwYBBQUHAgEWNGh0dHBz
Oi8vc2VjdXJlLmlkZW50cnVzdC5jb20vY2VydGlmaWNhdGVzL3BvbGljeS90cy8w
gbcGCCsGAQUFBwICMIGqGoGnVGhpcyBUcnVzdElEIFNlcnZlciBDZXJ0aWZpY2F0
ZSBoYXMgYmVlbiBpc3N1ZWQgaW4gYWNjb3JkYW5jZSB3aXRoIElkZW5UcnVzdCdz
IFRydXN0SUQgQ2VydGlmaWNhdGUgUG9saWN5IGZvdW5kIGF0IGh0dHBzOi8vc2Vj
dXJlLmlkZW50cnVzdC5jb20vY2VydGlmaWNhdGVzL3BvbGljeS90cy8wggEHBgZn
gQwBAgIwgfwwQAYIKwYBBQUHAgEWNGh0dHBzOi8vc2VjdXJlLmlkZW50cnVzdC5j
b20vY2VydGlmaWNhdGVzL3BvbGljeS90cy8wgbcGCCsGAQUFBwICMIGqGoGnVGhp
cyBUcnVzdElEIFNlcnZlciBDZXJ0aWZpY2F0ZSBoYXMgYmVlbiBpc3N1ZWQgaW4g
YWNjb3JkYW5jZSB3aXRoIElkZW5UcnVzdCdzIFRydXN0SUQgQ2VydGlmaWNhdGUg
UG9saWN5IGZvdW5kIGF0IGh0dHBzOi8vc2VjdXJlLmlkZW50cnVzdC5jb20vY2Vy
dGlmaWNhdGVzL3BvbGljeS90cy8wHQYDVR0OBBYEFNLAuFI2ugD0U24OgEPtX6+p
/xJQMEUGA1UdHwQ+MDwwOqA4oDaGNGh0dHA6Ly92YWxpZGF0aW9uLmlkZW50cnVz
dC5jb20vY3JsL3RydXN0aWRjYWE1Mi5jcmwwgYQGCCsGAQUFBwEBBHgwdjAwBggr
BgEFBQcwAYYkaHR0cDovL2NvbW1lcmNpYWwub2NzcC5pZGVudHJ1c3QuY29tMEIG
CCsGAQUFBzAChjZodHRwOi8vdmFsaWRhdGlvbi5pZGVudHJ1c3QuY29tL2NlcnRz
L3RydXN0aWRjYWE1Mi5wN2MwHQYDVR0lBBYwFAYIKwYBBQUHAwEGCCsGAQUFBwMC
MB8GA1UdIwQYMBaAFKJWJDzQ1BW56L94oxMQWEguFlThMC8GA1UdEQQoMCaCD2xl
dHNlbmNyeXB0Lm9yZ4ITd3d3LmxldHNlbmNyeXB0Lm9yZzANBgkqhkiG9w0BAQsF
AAOCAQEAgEmnzpYncB/E5SCHa5cnGorvNNE6Xsp3YXK9fJBT2++chQTkyFYpE12T
TR+cb7CTdRiYErNHXV8Hl/XTK8mxGxK8KXM9zUDlfrl7yBnyGTl2Sk8qJwA2kGuu
X9KA1o3MFkKMD809ITAlvPoQpml1Ke0aFo4NLO/LJKnJpkyF8L+JQrkfLNHpKYn3
PvnyJnurVTXDOIwQw8HVXbw6UKAad87e1hKGLYOpsaaKCLaNw1vg8uI+O9mv1MC6
FTfP1pSlr11s+Ih4YancuJud41rT8lXCUbDs1Uws9pPdVzLt8zk5M0vbHmTCljbg
UC5XkUmEvadMfgWslIQD0r6+BRRS+A==
-----END CERTIFICATE-----`

var testIntermediate = `-----BEGIN CERTIFICATE-----
MIIG3zCCBMegAwIBAgIQAJv84kD9Vb7ZJp4MASwbdzANBgkqhkiG9w0BAQsFADBK
MQswCQYDVQQGEwJVUzESMBAGA1UEChMJSWRlblRydXN0MScwJQYDVQQDEx5JZGVu
VHJ1c3QgQ29tbWVyY2lhbCBSb290IENBIDEwHhcNMTQwMzIwMTgwNTM4WhcNMjIw
MzIwMTgwNTM4WjBaMQswCQYDVQQGEwJVUzESMBAGA1UEChMJSWRlblRydXN0MRcw
FQYDVQQLEw5UcnVzdElEIFNlcnZlcjEeMBwGA1UEAxMVVHJ1c3RJRCBTZXJ2ZXIg
Q0EgQTUyMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAl2nXmZiFAj/p
JkJ26PRzP6kyRCaQeC54V5EZoF12K0n5k1pdWs6C88LY5Uw2eisdDdump/6REnzt
cgG3jKHF2syd/gn7V+IURw/onpGPlC2AMpOTA/UoeGi6fg9CtDF6BRQiUzPko61s
j6++Y2uyMp/ZF7nJ4GB8mdYx4eSgtz+vsjKsfoyc3ALr4bwfFJy8kfey+0Lz4SAr
y7+P87NwY/r3dSgCq8XUsO3qJX+HzTcUloM8QAIboJ4ZR3/zsMzFJWC4NRLxUesX
3Pxbpdmb70BM13dx6ftFi37y42mwQmYXRpA6zUY98bAJb9z/7jNhyvzHLjztXgrR
vyISaYBLIwIDAQABo4ICrzCCAqswgYkGCCsGAQUFBwEBBH0wezAwBggrBgEFBQcw
AYYkaHR0cDovL2NvbW1lcmNpYWwub2NzcC5pZGVudHJ1c3QuY29tMEcGCCsGAQUF
BzAChjtodHRwOi8vdmFsaWRhdGlvbi5pZGVudHJ1c3QuY29tL3Jvb3RzL2NvbW1l
cmNpYWxyb290Y2ExLnA3YzAfBgNVHSMEGDAWgBTtRBnA0/AGi+6ke75C5yZUyI42
djAPBgNVHRMBAf8EBTADAQH/MIIBMQYDVR0gBIIBKDCCASQwggEgBgRVHSAAMIIB
FjBQBggrBgEFBQcCAjBEMEIWPmh0dHBzOi8vc2VjdXJlLmlkZW50cnVzdC5jb20v
Y2VydGlmaWNhdGVzL3BvbGljeS90cy9pbmRleC5odG1sMAAwgcEGCCsGAQUFBwIC
MIG0GoGxVGhpcyBUcnVzdElEIFNlcnZlciBDZXJ0aWZpY2F0ZSBoYXMgYmVlbiBp
c3N1ZWQgaW4gYWNjb3JkYW5jZSB3aXRoIElkZW5UcnVzdCdzIFRydXN0SUQgQ2Vy
dGlmaWNhdGUgUG9saWN5IGZvdW5kIGF0IGh0dHBzOi8vc2VjdXJlLmlkZW50cnVz
dC5jb20vY2VydGlmaWNhdGVzL3BvbGljeS90cy9pbmRleC5odG1sMEoGA1UdHwRD
MEEwP6A9oDuGOWh0dHA6Ly92YWxpZGF0aW9uLmlkZW50cnVzdC5jb20vY3JsL2Nv
bW1lcmNpYWxyb290Y2ExLmNybDA7BgNVHSUENDAyBggrBgEFBQcDAQYIKwYBBQUH
AwIGCCsGAQUFBwMFBggrBgEFBQcDBgYIKwYBBQUHAwcwDgYDVR0PAQH/BAQDAgGG
MB0GA1UdDgQWBBSiViQ80NQVuei/eKMTEFhILhZU4TANBgkqhkiG9w0BAQsFAAOC
AgEAm4oWcizMGDsjzYFKfWUKferHD1Vusclu4/dra0PCx3HctXJMnuXc4Ngvn6Ab
BcanG0Uht+bkuC4TaaS3QMCl0LwcsIzlfRzDJdxIpREWHH8yoNoPafVN3u2iGiyT
5qda4Ej4WQgOmmNiluZPk8a4d4MkAxyQdVF/AVVx6Or+9d+bkQenjPSxWVmi/bfW
RBXq2AcD8Ej7AIU15dRnLEkESmJm4xtV2aqmCd0SSBGhJHYLcInUPzWVg1zcB5EQ
78GOTue8UrZvbcYhOufHG0k5JX5HVoVZ6GSXKqn5kqbcHXT6adVoWT/BxZruZiKQ
qkryoZoSywt7dDdDhpC2+oAOC+XwX2HJp2mrPaAea1+E4LM9C9iEDtjsn5FfsBz0
VRbMRdaoayXzOlTRhF3pGU2LLCmrXy/pqpqAGYPxyHr3auRn9fjv77UMEqVFdfOc
CspkK71IGqM9UwwMtCZBp0fK/Xv9o1d85paXcJ/aH8zg6EK4UkuXDFnLsg1LrIru
+YHeHOeSaXJlcjzwWVY/Exe5HymtqGH8klMhy65bjtapNt76+j2CJgxOdPEiTy/l
9LH5ujlo5qgemXE3ePwYZ9D3iiJThTf3tWkvdbz2wCPJAy2EHS0FxHMfx5sXsFsa
OY8B7wwvZTLzU6WWs781TJXx2CE04PneeeArLpVLkiGIWjk=
-----END CERTIFICATE-----`

const issuerPath = "../test/test-ca.pem"

var log = mocks.UseMockLog()

func getPort(hs *httptest.Server) (int, error) {
	url, err := url.Parse(hs.URL)
	if err != nil {
		return 0, err
	}
	_, portString, err := net.SplitHostPort(url.Host)
	if err != nil {
		return 0, err
	}
	port, err := strconv.ParseInt(portString, 10, 64)
	if err != nil {
		return 0, err
	}
	return int(port), nil
}

func logSrv() *httptest.Server {
	m := http.NewServeMux()
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		decoder := json.NewDecoder(r.Body)
		var jsonReq ctSubmissionRequest
		err := decoder.Decode(&jsonReq)
		if err != nil {
			return
		}
		// Submissions should always contain at least one cert
		if len(jsonReq.Chain) >= 1 {
			fmt.Fprint(w, `{"signature":"BAMASDBGAiEAknaySJVdB3FqG9bUKHgyu7V9AdEabpTc71BELUp6/iECIQDObrkwlQq6Azfj5XOA5E12G/qy/WuRn97z7qMSXXc82Q=="}`)
		}
	})

	server := httptest.NewUnstartedServer(m)
	server.Start()
	return server
}

func retryableLogSrv(retries int, after *int) *httptest.Server {
	hits := 0
	m := http.NewServeMux()
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if hits >= retries {
			fmt.Fprint(w, `{"signature":"BAMASDBGAiEAknaySJVdB3FqG9bUKHgyu7V9AdEabpTc71BELUp6/iECIQDObrkwlQq6Azfj5XOA5E12G/qy/WuRn97z7qMSXXc82Q=="}`)
		} else {
			hits++
			if after != nil {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", *after))
			}
			w.WriteHeader(http.StatusRequestTimeout)
		}
	})

	server := httptest.NewUnstartedServer(m)
	server.Start()
	return server
}

func emptyLogSrv() *httptest.Server {
	m := http.NewServeMux()
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		decoder := json.NewDecoder(r.Body)
		var jsonReq ctSubmissionRequest
		err := decoder.Decode(&jsonReq)
		if err != nil {
			return
		}
		// Submissions should always contain at least one cert
		if len(jsonReq.Chain) >= 1 {
			fmt.Fprint(w, `{"signature":""}`)
		}
	})

	server := httptest.NewUnstartedServer(m)
	server.Start()
	return server
}

func setup(t *testing.T, port, retries int) (PublisherImpl, *x509.Certificate) {
	intermediatePEM, _ := pem.Decode([]byte(testIntermediate))

	pub, err := NewPublisherImpl(CTConfig{
		Logs: []LogDescription{LogDescription{URI: fmt.Sprintf("http://localhost:%d", port)}},
		SubmissionBackoffString:    "0s",
		IntermediateBundleFilename: issuerPath,
		SubmissionRetries:          retries,
	})
	test.AssertNotError(t, err, "Couldn't create new Publisher")
	pub.issuerBundle = append(pub.issuerBundle, base64.StdEncoding.EncodeToString(intermediatePEM.Bytes))
	pub.SA = &mocks.StorageAuthority{}

	leafPEM, _ := pem.Decode([]byte(testLeaf))
	leaf, err := x509.ParseCertificate(leafPEM.Bytes)
	test.AssertNotError(t, err, "Couldn't parse leafPEM.Bytes")

	return pub, leaf
}

func TestNewPublisherImpl(t *testing.T) {
	// Allowed
	ctConf := CTConfig{SubmissionBackoffString: "0s", IntermediateBundleFilename: issuerPath}
	_, err := NewPublisherImpl(ctConf)
	test.AssertNotError(t, err, "Couldn't create new Publisher")

	ctConf = CTConfig{Logs: []LogDescription{LogDescription{URI: "http://localhost"}}, SubmissionBackoffString: "0s", IntermediateBundleFilename: issuerPath}
	_, err = NewPublisherImpl(ctConf)
	test.AssertNotError(t, err, "Couldn't create new Publisher")
}

func TestCheckSignature(t *testing.T) {
	// Based on a submission to the aviator log
	goodSigBytes, err := base64.StdEncoding.DecodeString("BAMASDBGAiEAknaySJVdB3FqG9bUKHgyu7V9AdEabpTc71BELUp6/iECIQDObrkwlQq6Azfj5XOA5E12G/qy/WuRn97z7qMSXXc82Q==")
	test.AssertNotError(t, err, "Couldn't decode signature")

	testReceipt := core.SignedCertificateTimestamp{
		Signature: goodSigBytes,
	}

	// Good signature
	err = testReceipt.CheckSignature()
	test.AssertNotError(t, err, "Valid signature check failed")

	// Invalid signature (too short, trailing garbage)
	testReceipt.Signature = goodSigBytes[1:]
	err = testReceipt.CheckSignature()
	test.AssertError(t, err, "Invalid signature check failed")
	testReceipt.Signature = append(goodSigBytes, []byte{0, 0, 1}...)
	err = testReceipt.CheckSignature()
	test.AssertError(t, err, "Invalid signature check failed")
}

func TestSubmitToCT(t *testing.T) {
	server := logSrv()
	defer server.Close()
	port, err := getPort(server)
	test.AssertNotError(t, err, "Failed to get test server port")

	pub, leaf := setup(t, port, 0)

	log.Clear()
	err = pub.SubmitToCT(leaf.Raw)
	test.AssertNotError(t, err, "Certificate submission failed")

	// No Intermediate
	pub.issuerBundle = []string{}
	log.Clear()
	err = pub.SubmitToCT(leaf.Raw)
	test.AssertNotError(t, err, "Certificate submission failed")
}

func TestGoodRetry(t *testing.T) {
	server := retryableLogSrv(1, nil)
	defer server.Close()
	port, err := getPort(server)
	test.AssertNotError(t, err, "Failed to get test server port")

	pub, leaf := setup(t, port, 1)

	log.Clear()
	err = pub.SubmitToCT(leaf.Raw)
	test.AssertNotError(t, err, "Certificate submission failed")
}

func TestFatalRetry(t *testing.T) {
	server := retryableLogSrv(1, nil)
	defer server.Close()
	port, err := getPort(server)
	test.AssertNotError(t, err, "Failed to get test server port")

	pub, leaf := setup(t, port, 0)

	log.Clear()
	err = pub.SubmitToCT(leaf.Raw)
	test.AssertEquals(t, len(log.GetAllMatching("Unable to submit certificate to CT log.*")), 1)
}

func TestUnexpectedError(t *testing.T) {
	pub, leaf := setup(t, 0, 0)

	log.Clear()
	_ = pub.SubmitToCT(leaf.Raw)
	test.AssertEquals(t, len(log.GetAllMatching("Unable to submit certificate to CT log.*")), 1)
}

func TestRetryAfter(t *testing.T) {
	retryAfter := 2
	server := retryableLogSrv(2, &retryAfter)
	defer server.Close()
	port, err := getPort(server)
	test.AssertNotError(t, err, "Failed to get test server port")

	pub, leaf := setup(t, port, 2)

	log.Clear()
	startedWaiting := time.Now()
	err = pub.SubmitToCT(leaf.Raw)
	test.AssertNotError(t, err, "Certificate submission failed")
	test.Assert(t, time.Since(startedWaiting) >= time.Duration(retryAfter*2)*time.Second, fmt.Sprintf("Submitter retried submission too fast: %s", time.Since(startedWaiting)))
}

func TestMultiLog(t *testing.T) {
	srvA := logSrv()
	defer srvA.Close()
	srvB := logSrv()
	defer srvB.Close()
	portA, err := getPort(srvA)
	test.AssertNotError(t, err, "Failed to get test server port")
	portB, err := getPort(srvB)
	test.AssertNotError(t, err, "Failed to get test server port")

	pub, leaf := setup(t, portA, 0)
	pub.ctLogs = append(pub.ctLogs, LogDescription{URI: fmt.Sprintf("http://localhost:%d", portB)})

	log.Clear()
	err = pub.SubmitToCT(leaf.Raw)
	test.AssertNotError(t, err, "Certificate submission failed")
}

func TestBadServer(t *testing.T) {
	srv := emptyLogSrv()
	defer srv.Close()
	port, err := getPort(srv)
	test.AssertNotError(t, err, "Failed to get test server port")

	pub, leaf := setup(t, port, 0)

	log.Clear()
	err = pub.SubmitToCT(leaf.Raw)
	test.AssertEquals(t, len(log.GetAllMatching("SCT signature is truncated")), 1)
}
