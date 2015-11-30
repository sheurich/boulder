package dns

import (
	"net"

	"github.com/letsencrypt/boulder/probs"
)

const detailDNSTimeout = "DNS query timed out"
const detailDNSNetFailure = "DNS networking error"
const detailServerFailure = "Server failure at resolver"

// ProblemDetailsFromDNSError checks the error returned from Lookup...
// methods and tests if the error was an underlying net.OpError or an error
// caused by resolver returning SERVFAIL or other invalid Rcodes and returns
// the relevant core.ProblemDetails.
func ProblemDetailsFromDNSError(err error) *probs.ProblemDetails {
	problem := &probs.ProblemDetails{Type: probs.ConnectionProblem}
	if netErr, ok := err.(*net.OpError); ok {
		if netErr.Timeout() {
			problem.Detail = detailDNSTimeout
		} else {
			problem.Detail = detailDNSNetFailure
		}
	} else {
		problem.Detail = detailServerFailure
	}
	return problem
}
