package challtestsrvclient

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

const (
	addPKIValidation = "add-pki-validation01"
	delPKIValidation = "del-pki-validation01"
)

// AddPKIValidation01Response adds an ACME pki-validation-01 challenge
// response for the filename derived from the key authorization. The filename
// is base64url(SHA-256(keyAuthorization)), ensuring uniqueness per challenge.
func (c *Client) AddPKIValidation01Response(keyauth string) ([]byte, error) {
	if keyauth == "" {
		return nil, fmt.Errorf(
			"AddPKIValidation01Response: empty key authorization")
	}
	hash := sha256.Sum256([]byte(keyauth))
	filename := base64.RawURLEncoding.EncodeToString(hash[:])
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
	if keyauth == "" {
		return nil, fmt.Errorf(
			"RemovePKIValidation01Response: empty key authorization")
	}
	hash := sha256.Sum256([]byte(keyauth))
	filename := base64.RawURLEncoding.EncodeToString(hash[:])
	payload := map[string]string{"filename": filename}
	resp, err := c.postURL(delPKIValidation, payload)
	if err != nil {
		return nil, fmt.Errorf(
			"while removing pki-validation-01 challenge response for filename %q (payload: %v): %w",
			filename, payload, err)
	}
	return resp, nil
}
