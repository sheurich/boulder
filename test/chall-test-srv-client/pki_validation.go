package challtestsrvclient

import (
	"fmt"
	"strings"
)

const (
	addPKIValidation = "add-pki-validation01"
	delPKIValidation = "del-pki-validation01"
)

// AddPKIValidation01Response adds an ACME pki-validation-01 challenge
// response for the provided filename under the challenge test server's
// /.well-known/pki-validation/ path. The filename should be the account
// key thumbprint from the second half of the key authorization.
func (c *Client) AddPKIValidation01Response(keyauth string) ([]byte, error) {
	parts := strings.SplitN(keyauth, ".", 2)
	if len(parts) != 2 || parts[1] == "" {
		return nil, fmt.Errorf(
			"AddPKIValidation01Response: malformed key authorization %q", keyauth)
	}
	filename := parts[1]
	payload := map[string]string{"filename": filename, "content": keyauth}
	resp, err := c.postURL(addPKIValidation, payload)
	if err != nil {
		return nil, fmt.Errorf(
			"while adding pki-validation-01 challenge response for filename %q (payload: %v): %w",
			filename, payload, err)
	}
	return resp, nil
}

// RemovePKIValidation01Response removes an ACME pki-validation-01 challenge
// response for the filename derived from the provided key authorization.
func (c *Client) RemovePKIValidation01Response(keyauth string) ([]byte, error) {
	parts := strings.SplitN(keyauth, ".", 2)
	if len(parts) != 2 || parts[1] == "" {
		return nil, fmt.Errorf(
			"RemovePKIValidation01Response: malformed key authorization %q", keyauth)
	}
	filename := parts[1]
	payload := map[string]string{"filename": filename}
	resp, err := c.postURL(delPKIValidation, payload)
	if err != nil {
		return nil, fmt.Errorf(
			"while removing pki-validation-01 challenge response for filename %q (payload: %v): %w",
			filename, payload, err)
	}
	return resp, nil
}
