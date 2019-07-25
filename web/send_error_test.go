package web

import (
	"errors"
	"net/http/httptest"
	"testing"

	berrors "github.com/letsencrypt/boulder/errors"
	"github.com/letsencrypt/boulder/identifier"
	"github.com/letsencrypt/boulder/log"
	"github.com/letsencrypt/boulder/test"
)

func TestSendErrorSubProblemNamespace(t *testing.T) {
	rw := httptest.NewRecorder()
	prob := ProblemDetailsForError((&berrors.BoulderError{
		Type:   berrors.Malformed,
		Detail: "bad",
	}).WithSubErrors(
		[]berrors.SubBoulderError{
			berrors.SubBoulderError{
				Identifier: identifier.DNSIdentifier("example.com"),
				BoulderError: &berrors.BoulderError{
					Type:   berrors.Malformed,
					Detail: "nop",
				},
			},
			berrors.SubBoulderError{
				Identifier: identifier.DNSIdentifier("what about example.com"),
				BoulderError: &berrors.BoulderError{
					Type:   berrors.Malformed,
					Detail: "nah",
				},
			},
		}),
		"dfoop",
	)
	SendError(log.NewMock(), "namespace:test:", rw, &RequestEvent{}, prob, errors.New("it bad"))

	body := rw.Body.String()
	test.AssertUnmarshaledEquals(t, body, `{
		"type": "namespace:test:malformed",
		"detail": "dfoop :: bad",
		"status": 400,
		"subproblems": [
		  {
			"type": "namespace:test:namespace:test:malformed",
			"detail": "dfoop :: nop",
			"status": 400,
			"Identifier": {
			  "type": "dns",
			  "value": "example.com"
			}
		  },
		  {
			"type": "namespace:test:namespace:test:malformed",
			"detail": "dfoop :: nah",
			"status": 400,
			"Identifier": {
			  "type": "dns",
			  "value": "what about example.com"
			}
		  }
		]
	  }`)
}
