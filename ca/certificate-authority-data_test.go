// Copyright 2015 ISRG.  All rights reserved
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ca

import (
	"testing"

	"github.com/letsencrypt/boulder/core"
	"github.com/letsencrypt/boulder/sa"
	"github.com/letsencrypt/boulder/test"
)

func TestGetSetSequenceOutsideTx(t *testing.T) {
	cadb, cleanUp := caDBImpl(t)
	defer cleanUp()
	tx, err := cadb.Begin()
	test.AssertNotError(t, err, "Could not begin")
	tx.Commit()
	_, err = cadb.IncrementAndGetSerial(tx)
	test.AssertError(t, err, "Not permitted")

	tx2, err := cadb.Begin()
	test.AssertNotError(t, err, "Could not begin")
	tx2.Rollback()
	_, err = cadb.IncrementAndGetSerial(tx2)
	test.AssertError(t, err, "Not permitted")
}

func TestGetSetSequenceNumber(t *testing.T) {
	cadb, cleanUp := caDBImpl(t)
	defer cleanUp()
	tx, err := cadb.Begin()
	test.AssertNotError(t, err, "Could not begin")

	num, err := cadb.IncrementAndGetSerial(tx)
	test.AssertNotError(t, err, "Could not get number")

	num2, err := cadb.IncrementAndGetSerial(tx)
	test.AssertNotError(t, err, "Could not get number")
	test.Assert(t, num+1 == num2, "Numbers should be incrementing")

	err = tx.Commit()
	test.AssertNotError(t, err, "Could not commit")
}

func caDBImpl(t *testing.T) (core.CertificateAuthorityDatabase, func()) {
	dbMap, err := sa.NewDbMap(dbConnStr)
	if err != nil {
		t.Fatalf("Could not construct dbMap: %s", err)
	}

	cadb, err := NewCertificateAuthorityDatabaseImpl(dbMap)
	if err != nil {
		t.Fatalf("Could not construct CA DB: %s", err)
	}

	// We intentionally call CreateTablesIfNotExists twice before
	// returning because of the weird insert inside it. The
	// CADatabaseImpl code expects the existence of a single row in
	// its serialIds table or else it errors. CreateTablesIfNotExists
	// currently inserts that row and TruncateTables will remove
	// it. But we need to make sure the tables exist before
	// TruncateTables can be called to reset the table. So, two calls
	// to CreateTablesIfNotExists.

	err = cadb.CreateTablesIfNotExists()
	if err != nil {
		t.Fatalf("Could not construct tables: %s", err)
	}
	err = dbMap.TruncateTables()
	if err != nil {
		t.Fatalf("Could not truncate tables: %s", err)
	}
	err = cadb.CreateTablesIfNotExists()
	if err != nil {
		t.Fatalf("Could not construct tables: %s", err)
	}
	cleanUp := func() {
		if err := dbMap.TruncateTables(); err != nil {
			t.Fatalf("Could not truncate tables after the test: %s", err)
		}
		dbMap.Db.Close()
	}

	return cadb, cleanUp
}
