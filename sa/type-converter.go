// Copyright 2015 ISRG.  All rights reserved
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package sa

import (
	"encoding/json"
	"errors"
	"fmt"

	jose "github.com/letsencrypt/boulder/Godeps/_workspace/src/github.com/letsencrypt/go-jose"
	gorp "github.com/letsencrypt/boulder/Godeps/_workspace/src/gopkg.in/gorp.v1"

	"github.com/letsencrypt/boulder/core"
)

// BoulderTypeConverter is used by Gorp for storing objects in DB.
type BoulderTypeConverter struct{}

// ToDb converts a Boulder object to one suitable for the DB representation.
func (tc BoulderTypeConverter) ToDb(val interface{}) (interface{}, error) {
	switch t := val.(type) {
	case core.AcmeIdentifier, []core.Challenge, []*core.AcmeURL, [][]int:
		jsonBytes, err := json.Marshal(t)
		if err != nil {
			return nil, err
		}
		return string(jsonBytes), nil
	case jose.JsonWebKey:
		jsonBytes, err := t.MarshalJSON()
		if err != nil {
			return "", err
		}
		return string(jsonBytes), nil
	case core.AcmeStatus:
		return string(t), nil
	case core.OCSPStatus:
		return string(t), nil
	case core.JSONBuffer:
		return []byte(t), nil
	default:
		return val, nil
	}
}

// FromDb converts a DB representation back into a Boulder object.
func (tc BoulderTypeConverter) FromDb(target interface{}) (gorp.CustomScanner, bool) {
	switch target.(type) {
	case *core.AcmeIdentifier, *[]core.Challenge, *[]*core.AcmeURL, *[][]int, core.JSONBuffer:
		binder := func(holder, target interface{}) error {
			s, ok := holder.(*string)
			if !ok {
				return errors.New("FromDb: Unable to convert *string")
			}
			b := []byte(*s)
			return json.Unmarshal(b, target)
		}
		return gorp.CustomScanner{Holder: new(string), Target: target, Binder: binder}, true
	case *jose.JsonWebKey:
		binder := func(holder, target interface{}) error {
			s, ok := holder.(*string)
			if !ok {
				return fmt.Errorf("FromDb: Unable to convert %T to *string", holder)
			}
			if *s == "" {
				return errors.New("FromDb: Empty JWK field.")
			}
			b := []byte(*s)
			k, ok := target.(*jose.JsonWebKey)
			if !ok {
				return fmt.Errorf("FromDb: Unable to convert %T to *jose.JsonWebKey", target)
			}
			return k.UnmarshalJSON(b)
		}
		return gorp.CustomScanner{Holder: new(string), Target: target, Binder: binder}, true
	case *core.AcmeStatus:
		binder := func(holder, target interface{}) error {
			s, ok := holder.(*string)
			if !ok {
				return fmt.Errorf("FromDb: Unable to convert %T to *string", holder)
			}
			st, ok := target.(*core.AcmeStatus)
			if !ok {
				return fmt.Errorf("FromDb: Unable to convert %T to *core.AcmeStatus", target)
			}

			*st = core.AcmeStatus(*s)
			return nil
		}
		return gorp.CustomScanner{Holder: new(string), Target: target, Binder: binder}, true
	case *core.OCSPStatus:
		binder := func(holder, target interface{}) error {
			s, ok := holder.(*string)
			if !ok {
				return fmt.Errorf("FromDb: Unable to convert %T to *string", holder)
			}
			st, ok := target.(*core.OCSPStatus)
			if !ok {
				return fmt.Errorf("FromDb: Unable to convert %T to *core.OCSPStatus", target)
			}

			*st = core.OCSPStatus(*s)
			return nil
		}
		return gorp.CustomScanner{Holder: new(string), Target: target, Binder: binder}, true
	default:
		return gorp.CustomScanner{}, false
	}
}
