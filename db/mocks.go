package db

import (
	"database/sql"

	"gopkg.in/go-gorp/gorp.v2"
)

// These interfaces exist to aid in mocking database operations for unit tests.
//
// By convention, any function that takes a OneSelector, Selector,
// Inserter, Execer, or SelectExecer as as an argument expects
// that a context has already been applied to the relevant DbMap or
// Transaction object.

// A `dbOneSelector` is anything that provides a `SelectOne` function.
type OneSelector interface {
	SelectOne(interface{}, string, ...interface{}) error
}

// A `Selector` is anything that provides a `Select` function.
type Selector interface {
	Select(interface{}, string, ...interface{}) ([]interface{}, error)
}

// A `Inserter` is anything that provides an `Insert` function
type Inserter interface {
	Insert(list ...interface{}) error
}

// A `Execer` is anything that provides an `Exec` function
type Execer interface {
	Exec(string, ...interface{}) (sql.Result, error)
}

// SelectExecer offers a subset of gorp.SqlExecutor's methods: Select and
// Exec.
type SelectExecer interface {
	Selector
	Execer
}

// DatabaseMap offers the full combination of OneSelector, Inserter,
// SelectExecer, and a Begin function for creating a Transaction.
type DatabaseMap interface {
	OneSelector
	Inserter
	SelectExecer
	Begin() (*gorp.Transaction, error)
}

// Transaction offers the combination of OneSelector, Inserter, SelectExecer
// interface as well as Delete, Get, and Update.
type Transaction interface {
	OneSelector
	Inserter
	SelectExecer
	Delete(...interface{}) (int64, error)
	Get(interface{}, ...interface{}) (interface{}, error)
	Update(...interface{}) (int64, error)
}
