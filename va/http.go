package va

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/letsencrypt/boulder/core"
	berrors "github.com/letsencrypt/boulder/errors"
	"github.com/letsencrypt/boulder/probs"
)

// shavedDialContext shaves 10ms off of the context it was given before
// calling the default DialContext. This helps us be able to differentiate
// between timeouts during connect and timeouts after connect.
func shavedDialContext(
	ctx context.Context,
	network,
	addr string) (net.Conn, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		// Shouldn't happen: All requests should have a deadline by this point.
		deadline = time.Now().Add(100 * time.Second)
	} else {
		// Set the context deadline slightly shorter than the HTTP deadline, so we
		// get a useful error rather than a generic "deadline exceeded" error. This
		// lets us give a more specific error to the subscriber.
		deadline = deadline.Add(-10 * time.Millisecond)
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	// Invoke the default transport's original DialContext function using the
	// reconstructed context.
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("DefaultTransport was not an http.Transport")
	}
	return defaultTransport.DialContext(ctx, network, addr)
}

// redirectChecker is a function that can be used for an HTTP Client's
// checkRedirect function.
type redirectChecker func(*http.Request, []*http.Request) error

// newHTTPClient constructs a HTTP client with a custom transport suitable for
// HTTP-01 validation. The provided checkRedirect function is used as the
// client's checkRedirect handler.
func newHTTPClient(checkRedirect redirectChecker) http.Client {
	// Construct a one-off HTTP client with a custom transport.
	return http.Client{
		Transport: &http.Transport{
			DialContext: shavedDialContext,
			// We are talking to a client that does not yet have a certificate,
			// so we accept a temporary, invalid one.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			// We don't expect to make multiple requests to a client, so close
			// connection immediately.
			DisableKeepAlives: true,
			// We don't want idle connections, but 0 means "unlimited," so we pick 1.
			MaxIdleConns:        1,
			IdleConnTimeout:     time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
		CheckRedirect: checkRedirect,
	}
}

// httpValidationURL constructs a URL for the given IP address, path and port
// combination. The port is omitted from the URL if it is the default HTTP
// port or the default HTTPS port. The protocol scheme of the URL is HTTP unless
// useHTTPS is true. UseHTTPS should only be true when constructing validation
// URLs based on a redirect from an initial HTTP validation request.
func httpValidationURL(validationIP net.IP, path string, port int, useHTTPS bool) *url.URL {
	urlHost := validationIP.String()

	// If the port is something other than the conventional HTTP or HTTPS port,
	// put it in the URL explicitly using `net.JoinHostPort`.
	if port != 80 && port != 443 {
		urlHost = net.JoinHostPort(validationIP.String(), strconv.Itoa(port))
	}

	// if the validation IP is an IPv6 address, and we aren't using
	// `net.JoinHostPort` then we have to manually surround the IPv6 address
	// with square brackets to make a valid IPv6 URL (e.g "http://[::1]/foo" not
	// "http://::1/foo")
	if (port == 80 || port == 443) && validationIP.To4() == nil {
		urlHost = fmt.Sprintf("[%s]", urlHost)
	}

	scheme := "http"
	if useHTTPS {
		scheme = "https"
	}

	return &url.URL{
		Scheme: scheme,
		Host:   urlHost,
		Path:   path,
	}
}

// httpValidationTarget bundles all of the information needed to make an HTTP-01
// validation request against a target.
type httpValidationTarget struct {
	// the hostname being validated
	host string
	// the port for the validation request
	port int
	// the path for the validation request
	path string
	// all of the IP addresses available for the host
	available []net.IP
	// the IP addresses that were tried for validation previously that were cycled
	// out of cur by calls to nextIP()
	tried []net.IP
	// the IP addresses that will be drawn from by calls to nextIP() to set curIP
	next []net.IP
	// the current IP address being used for validation (if any)
	cur net.IP
}

// nextIP changes the cur IP by removing the first entry from the next slice and
// setting it to cur. If cur was previously set the value will be added to the
// tried slice to keep track of IPs that were previously used. If nextIP() is
// called but vt.next is empty an error is returned.
func (vt *httpValidationTarget) nextIP() error {
	if len(vt.next) == 0 {
		return fmt.Errorf(
			"host %q has no IP addresses remaining to use",
			vt.host)
	}
	vt.tried = append(vt.tried, vt.cur)
	vt.cur = vt.next[0]
	vt.next = vt.next[1:]
	return nil
}

// ip returns the current *net.IP for the validation target. It may return nil
// if all possible IPs have been expended by calls to nextIP.
func (vt *httpValidationTarget) ip() net.IP {
	return vt.cur
}

// newHTTPValidationTarget creates a httpValidationTarget for the given host,
// port, and path. This involves querying DNS for the IP addresses for the host.
// An error is returned if there are no usable IP addresses or if the DNS
// lookups fail.
func (va *ValidationAuthorityImpl) newHTTPValidationTarget(
	ctx context.Context,
	host string,
	port int,
	path string) (*httpValidationTarget, error) {
	// Resolve IP addresses for the hostname
	addrs, err := va.getAddrs(ctx, host)
	if err != nil {
		// Convert the error into a ConnectionFailureError so it is presented to the
		// end user in a problem after being fed through detailedError.
		return nil, berrors.ConnectionFailureError(err.Error())
	}

	target := &httpValidationTarget{
		host:      host,
		port:      port,
		path:      path,
		available: addrs,
	}

	// Separate the addresses into the available v4 and v6 addresses
	v4Addrs, v6Addrs := availableAddresses(addrs)
	hasV6Addrs := len(v6Addrs) > 0
	hasV4Addrs := len(v4Addrs) > 0

	if !hasV6Addrs && !hasV4Addrs {
		// If there are no v6 addrs and no v4addrs there was a bug with getAddrs or
		// availableAddresses and we need to return an error.
		return nil, fmt.Errorf("host %q has no IPv4 or IPv6 addresses", host)
	} else if !hasV6Addrs && hasV4Addrs {
		// If there are no v6 addrs and there are v4 addrs then use the first v4
		// address. There's no fallback address.
		target.next = []net.IP{v4Addrs[0]}
	} else if hasV6Addrs && hasV4Addrs {
		// If there are both v6 addrs and v4 addrs then use the first v6 address and
		// fallback with the first v4 address.
		target.next = []net.IP{v6Addrs[0], v4Addrs[0]}
	} else if hasV6Addrs && !hasV4Addrs {
		// If there are just v6 addrs then use the first v6 address. There's no
		// fallback address.
		target.next = []net.IP{v6Addrs[0]}
	}

	// Advance the target using nextIP to populate the cur IP before returning
	_ = target.nextIP()
	return target, nil
}

// extractRequestTarget extracts the hostname and port specified in the provided
// HTTP redirect request. If the request's URL's protocol schema is not HTTP or
// HTTPS an error is returned. If an explicit port is specified in the request's
// URL and it isn't the VA's HTTP or HTTPS port, an error is returned. If the
// request's URL's Host is a bare IPv4 or IPv6 address and not a domain name an
// error is returned.
func (va *ValidationAuthorityImpl) extractRequestTarget(req *http.Request) (string, int, error) {
	// A nil request is certainly not a valid redirect and has no port to extract.
	if req == nil {
		return "", 0, fmt.Errorf("redirect HTTP request was nil")
	}

	reqScheme := req.URL.Scheme

	// The redirect request must use HTTP or HTTPs protocol schemes regardless of the port..
	if reqScheme != "http" && reqScheme != "https" {
		return "", 0, berrors.ConnectionFailureError(
			"Invalid protocol scheme in redirect target. "+
				`Only "http" and "https" protocol schemes are supported, not %q`, reqScheme)
	}

	// Try and split an explicit port number from the request URL host. If there is
	// one we need to make sure its a valid port. If there isn't one we need to
	// pick the port based on the reqScheme default port.
	reqHost := req.URL.Host
	reqPort := 0
	if h, p, err := net.SplitHostPort(reqHost); err == nil {
		reqHost = h
		reqPort, err = strconv.Atoi(p)
		if err != nil {
			return "", 0, err
		}

		// The explicit port must match the VA's configured HTTP or HTTPS port.
		if reqPort != va.httpPort && reqPort != va.httpsPort {
			return "", 0, berrors.ConnectionFailureError(
				"Invalid port in redirect target. Only ports %d and %d are supported, not %d",
				va.httpPort, va.httpsPort, reqPort)
		}
	} else if reqScheme == "http" {
		reqPort = va.httpPort
	} else if reqScheme == "https" {
		reqPort = va.httpsPort
	} else {
		// This shouldn't happen but defensively return an internal server error in
		// case it does.
		return "", 0, fmt.Errorf("unable to determine redirect HTTP request port")
	}

	// Check that the request host isn't a bare IP address. We only follow
	// redirects to hostnames.
	if net.ParseIP(reqHost) != nil {
		return "", 0, berrors.ConnectionFailureError(
			"Invalid host in redirect target %q. "+
				"Only domain names are supported, not IP addresses", reqHost)
	}

	return reqHost, reqPort, nil
}

// setupHTTPValidation can be used in two ways:
// 1) To create and setup the initial validation request for a target by
//    providing a nil req.
// 2) To mutate an existing HTTP request to use a URL/Host based on resolved IP
//    addresses.
// The second is helpful when processing redirect requests.
func (va *ValidationAuthorityImpl) setupHTTPValidation(
	ctx context.Context,
	req *http.Request,
	target *httpValidationTarget) (*http.Request, core.ValidationRecord, error) {
	if target == nil {
		// This is the only case where returning an empty validation record makes
		// sense - we can't construct a better one, something has gone quite wrong.
		return nil,
			core.ValidationRecord{},
			fmt.Errorf("httpValidationTarget can not be nil")
	}

	// Construct a base validation record with the target's information.
	record := core.ValidationRecord{
		Hostname:          target.host,
		Port:              strconv.Itoa(target.port),
		AddressesResolved: target.available,
	}

	// Build a URL with the target's IP address and port
	targetIP := target.ip()
	if targetIP == nil {
		return nil, record, fmt.Errorf(
			"host %q has no IP addresses remaining to use",
			target.host)
	}

	var useHTTPS bool
	// If we are mutating an existing redirected request and the original request
	// URL uses HTTPS then we must construct a validation URL using HTTPS. In all
	// other cases we construct an HTTP URL.
	if req != nil && req.URL.Scheme == "https" {
		useHTTPS = true
	}

	record.AddressUsed = targetIP
	url := httpValidationURL(targetIP, target.path, target.port, useHTTPS)
	record.URL = url.String()

	// If there's no provided HTTP request to mutate (e.g. a redirect request
	// we're following as part of a validation) then construct a new initial HTTP
	// GET request for the validation.
	if req == nil {
		var err error
		req, err = http.NewRequest("GET", url.String(), nil)
		if err != nil {
			return nil, record, err
		}
		// Immediately reconstruct the request using the validation context
		req = req.WithContext(ctx)
		if va.userAgent != "" {
			req.Header.Set("User-Agent", va.userAgent)
		}
		// Some of our users use mod_security. Mod_security sees a lack of Accept
		// headers as bot behavior and rejects requests. While this is a bug in
		// mod_security's rules (given that the HTTP specs disagree with that
		// requirement), we add the Accept header now in order to fix our
		// mod_security users' mysterious breakages. See
		// <https://github.com/SpiderLabs/owasp-modsecurity-crs/issues/265> and
		// <https://github.com/letsencrypt/boulder/issues/1019>. This was done
		// because it's a one-line fix with no downside. We're not likely to want to
		// do many more things to satisfy misunderstandings around HTTP.
		req.Header.Set("Accept", "*/*")
	}

	// Override the request's target URL and Host
	req.URL = url
	req.Host = target.host
	return req, record, nil
}

// fetchHTTPSimple invokes processHTTPValidation and if an error result is
// returned, converts it to a problem. Otherwise the results from
// processHTTPValidation are returned.
func (va *ValidationAuthorityImpl) fetchHTTPSimple(
	ctx context.Context,
	host string,
	path string) ([]byte, []core.ValidationRecord, *probs.ProblemDetails) {
	body, records, err := va.processHTTPValidation(ctx, host, path)
	if err != nil {
		// Use detailedError to convert the error into a problem
		return body, records, detailedError(err)
	}
	return body, records, nil
}

// fallbackErr returns true only for net.OpError instances where the op is equal
// to "dial", or url.Error instances wrapping such an error. fallbackErr returns
// false for all other errors. By policy, only dial errors (not read or write
// errors) are eligble for fallback from an IPv6 to an IPv4 address.
func fallbackErr(err error) bool {
	// Err shouldn't ever be nil if we're considering it for fallback
	if err == nil {
		return false
	}

	switch err := err.(type) {
	case *url.Error:
		// URL Errors should be unwrapped and tested
		return fallbackErr(err.Err)
	case *net.OpError:
		// Net OpErrors are fallback errs only if the operation was a "dial"
		return err.Op == "dial"
	default:
		// All other errs are not fallback errs
		return false
	}
}

// processHTTPValidation performs an HTTP validation for the given host, port
// and path. If successful the body of the HTTP response is returned along with
// the validation records created during the validation. If not successful
// a non-nil error and potentially some ValidationRecords are returned.
func (va *ValidationAuthorityImpl) processHTTPValidation(
	ctx context.Context,
	host string,
	path string) ([]byte, []core.ValidationRecord, error) {

	// Create a target for the host, port and path
	target, err := va.newHTTPValidationTarget(ctx, host, va.httpPort, path)
	if err != nil {
		return nil, nil, err
	}

	// Set up the initial validation request and a base validation record
	initialReq, baseRecord, err := va.setupHTTPValidation(ctx, nil, target)
	if err != nil {
		return nil, []core.ValidationRecord{}, err
	}

	va.log.AuditInfof("Attempting to validate HTTP-01 for %q with GET to %q",
		initialReq.Host, initialReq.URL.String())

	// Create a closure around records & numRedirects we can use with a HTTP
	// client to process redirects per our own policy (e.g. resolving IP
	// addresses explicitly, not following redirects to ports != [80,443], etc)
	records := []core.ValidationRecord{baseRecord}
	numRedirects := 0
	processRedirect := func(req *http.Request, via []*http.Request) error {
		va.log.Infof("processing a HTTP redirect from the server to %q\n", req.URL.String())
		// Only process up to maxRedirect redirects
		if numRedirects > maxRedirect {
			return berrors.ConnectionFailureError("Too many redirects")
		}
		numRedirects++
		va.metrics.http01Redirects.Inc()

		// Extract the redirect target's host and port. This will return an error if
		// the redirect request scheme, host or port is not acceptable.
		redirHost, redirPort, err := va.extractRequestTarget(req)
		if err != nil {
			return err
		}

		redirPath := req.URL.Path
		// Setup a validation target for the redirect host. This will resolve IP
		// addresses for the host explicitly.
		redirTarget, err := va.newHTTPValidationTarget(ctx, redirHost, redirPort, redirPath)
		if err != nil {
			return err
		}

		// Mutate the existing redirect request to use a URL and Host based on the
		// explicitly resolved target IPs. This will also give us a validationRecord
		// for the redirect which we should append to the records.
		_, redirRecord, err := va.setupHTTPValidation(ctx, req, redirTarget)
		records = append(records, redirRecord)
		if err != nil {
			return err
		}
		va.log.Infof("following redirect to host %q url %q\n", req.Host, req.URL.String())
		return nil
	}

	// Create a new HTTP client and check HTTP redirects it encounters with
	// processRedirect
	client := newHTTPClient(processRedirect)

	// Make the initial validation request. This may result in redirects being
	// followed.
	httpResponse, err := client.Do(initialReq)
	// If there was an error and its a kind of error we consider a fallback error,
	// then try to fallback.
	if err != nil && fallbackErr(err) {
		// Try to advance to another IP. If there was an error advancing we don't
		// have a fallback address to use and must return the original error.
		if ipErr := target.nextIP(); ipErr != nil {
			return nil, records, err
		}

		// setup another validation to retry the target with the new IP and append
		// the retry record.
		retryReq, retryRecord, err := va.setupHTTPValidation(ctx, nil, target)
		records = append(records, retryRecord)
		if err != nil {
			return nil, records, err
		}
		va.metrics.http01Fallbacks.Inc()

		// Perform the retry
		httpResponse, err = client.Do(retryReq)
		// If the retry still failed there isn't anything more to do, return the
		// error immediately.
		if err != nil {
			return nil, records, err
		}
	} else if err != nil {
		// if the error was not a fallbackErr then return immediately.
		return nil, records, err
	}

	// At this point we've made a successful request (be it from a retry or
	// otherwise) and can read and process the response body.
	body, err := ioutil.ReadAll(&io.LimitedReader{R: httpResponse.Body, N: maxResponseSize})
	closeErr := httpResponse.Body.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, records, berrors.UnauthorizedError("Error reading HTTP response body: %v", err)
	}
	// io.LimitedReader will silently truncate a Reader so if the
	// resulting payload is the same size as maxResponseSize fail
	if len(body) >= maxResponseSize {
		return nil, records, berrors.UnauthorizedError("Invalid response from %s [%s]: %q",
			records[len(records)-1].URL, records[len(records)-1].AddressUsed, body)
	}
	if httpResponse.StatusCode != 200 {
		return nil, records, berrors.UnauthorizedError("Invalid response from %s [%s]: %d",
			records[len(records)-1].URL, records[len(records)-1].AddressUsed, httpResponse.StatusCode)
	}
	return body, records, nil
}
