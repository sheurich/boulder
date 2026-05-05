package challtestsrv

import (
	"fmt"
	"net/http"
	"strings"
)

// pkiValidationPath is the BR §3.2.2.4.18 "Agreed-Upon Change to Website v2"
// well-known path prefix used by the pki-validation-01 ACME challenge type.
const pkiValidationPath = "/.well-known/pki-validation/"

// AddPKIValidation01Challenge adds a pki-validation-01 challenge response for
// the given filename. The content should be the exact string the VA expects
// in the response body (the ACME key authorization).
func (s *ChallSrv) AddPKIValidation01Challenge(filename, content string) {
	s.challMu.Lock()
	defer s.challMu.Unlock()
	s.pkiValidation01[filename] = content
}

// DeletePKIValidation01Challenge removes the pki-validation-01 challenge
// response for the given filename.
func (s *ChallSrv) DeletePKIValidation01Challenge(filename string) {
	s.challMu.Lock()
	defer s.challMu.Unlock()
	delete(s.pkiValidation01, filename)
}

// GetPKIValidation01Challenge returns the pki-validation-01 challenge response
// content for the given filename. Returns an empty string and false if the
// filename is not registered.
func (s *ChallSrv) GetPKIValidation01Challenge(filename string) (string, bool) {
	s.challMu.RLock()
	defer s.challMu.RUnlock()
	content, present := s.pkiValidation01[filename]
	return content, present
}

// maybeServePKIValidation01 writes a pki-validation-01 response body if the
// request path matches the well-known prefix and the filename is registered.
// Returns true if the request was handled (or matched the prefix without a
// stored response), false if the path did not match.
func (s *ChallSrv) maybeServePKIValidation01(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, pkiValidationPath) {
		return false
	}
	filename := r.URL.Path[len(pkiValidationPath):]
	if content, found := s.GetPKIValidation01Challenge(filename); found {
		fmt.Fprintf(w, "%s", content)
	}
	return true
}
