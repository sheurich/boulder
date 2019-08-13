package main

import (
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	blog "github.com/letsencrypt/boulder/log"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// errStat is a prometheus counter vector tracking the number of errors
	// experienced by the janitor during operation sliced by a table label and a
	// type label. Examples of possible type labels include "getWork" and
	// "deleteResource".
	errStat = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "janitor_errors",
			Help: "Number of errors by type the boulder-janitor has experienced.",
		},
		[]string{"table", "type"})
	// deletedStat is a prometheus counter vector tracking the number of rows
	// deleted by the janitor, sliced by a table label.
	deletedStat = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "janitor_deletions",
			Help: "Number of deletions by table the boulder-janitor has performed.",
		},
		[]string{"table"})
	// workStat is a prometheus gauge vector tracking the number of rows found
	// during a batchedJob's getWork stage and queued into the work channel sliced
	// by a table label.
	workStat = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "janitor_workbatch",
			Help: "Number of items of work by table the boulder-janitor queued for deletion.",
		},
		[]string{"table"})
)

// batchedDBJob is a struct abstracting the common properties of a long running
// cleanup job based on cursoring across a database table's auto incrementing
// primary key.
type batchedDBJob struct {
	db  janitorDB
	log blog.Logger
	// table is the name of the table that this job cleans up.
	table string
	// purgeBefore indicates the cut-off for the the resoruce being cleaned up by
	// the job. Rows that older than now - purgeBefore are deleted.
	purgeBefore time.Time
	// workSleep is a duration that the job will sleep between getWork() calls
	// when no new work is found. If not provided, defaults to a minute.
	workSleep time.Duration
	// batchSize indicates how many database rows of work should be returned per query.
	batchSize int64
	// maxDPS optionally indicates a maximum rate of deletes to run per second.
	maxDPS int
	// parallelism controls how many independent go routines will be performing
	// cleanup deletes.
	parallelism int
	// workQuery is the parameterized SQL query that is used to find more work. It will be provided three parameters:
	//   * :startID - the primary key value to start the work query from.
	//   * :cutoff  - the purgeBefore date used to control which rows are old enough to be deleted.
	//   * :limit   - the batchSize value. Only this many rows should be returned by the query.
	workQuery string
}

// getWork reads work into the provided work channel starting at the startID by
// using the batchedDBJob's configured work query, purgeBefore, and batchSize.
// If there is no error the last primary key ID written to the work channel will
// be returned, otherwise an error result is returned.
func (j batchedDBJob) getWork(work chan<- int64, startID int64) (int64, error) {
	var idBatch []int64
	_, err := j.db.Select(
		&idBatch,
		j.workQuery,
		map[string]interface{}{
			"startID": startID,
			"cutoff":  j.purgeBefore,
			"limit":   j.batchSize,
		},
	)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	rowsFound := len(idBatch)
	workStat.WithLabelValues(j.table).Set(float64(rowsFound))
	if rowsFound == 0 {
		return startID, nil
	}
	var lastID int64
	for _, v := range idBatch {
		work <- v
		lastID = v
	}
	return lastID, nil
}

// cleanResource uses the configured level of parallelism to run go routines
// that read ID values from the work channel and delete the corresponding table
// rows. If the batchedDBJob configures a maxDPS rate then it will be enforced by
// synchronizing the delete operations on a ticker based on the maxDPS.
// cleanResource will block until all of the worker go routines complete.
func (j batchedDBJob) cleanResource(work <-chan int64) {
	wg := new(sync.WaitGroup)
	deleted := int64(0)

	var ticker *time.Ticker
	if j.maxDPS > 0 {
		ticker = time.NewTicker(
			time.Duration(float64(time.Second) / float64(j.maxDPS)))
	}

	for i := 0; i < j.parallelism; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range work {
				if ticker != nil {
					<-ticker.C
				}
				if err := j.deleteResource(id); err != nil {
					j.log.Errf(
						"error deleting ID %d from table %q: %s",
						id, j.table, err)
					errStat.WithLabelValues(j.table, "deleteResource").Inc()
				}
				_ = atomic.AddInt64(&deleted, 1)
			}
		}()
	}

	wg.Wait()
	j.log.Infof(
		"deleted a total of %d rows from table %q",
		deleted, j.table)
}

// deleteResource peforms a delete of the given ID from the batchedDBJob's
// table or returns an error.
func (j batchedDBJob) deleteResource(id int64) error {
	// NOTE(@cpu): We throw away the sql.Result here without checking the rows
	// affected because the query is always specific to the ID auto-increment
	// primary key. If there are multiple rows with the same primary key MariaDB
	// has failed us deeply.
	query := fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, j.table)
	if _, err := j.db.Exec(query, id); err != nil {
		return err
	}
	j.log.Debugf("deleted ID %d in table %q", id, j.table)
	deletedStat.WithLabelValues(j.table).Inc()
	return nil
}

// RunForever starts a go routine that will run forever getting work with
// getWork and deleting rows with cleanResource.
func (j batchedDBJob) RunForever() {
	var id int64
	work := make(chan int64)

	go func() {
		for {
			lastID, err := j.getWork(work, id)
			if err != nil {
				j.log.Errf("error getting work for %q from ID %d: %s",
					j.table, id, err.Error())
				errStat.WithLabelValues(j.table, "getWork").Inc()
				time.Sleep(time.Millisecond * 500)
				continue
			} else if lastID == id {
				j.log.Debugf(
					"made no new progress on table %q. Sleeping for a minute",
					j.table)
				if j.workSleep.Seconds() == 0 {
					time.Sleep(time.Minute)
				} else {
					time.Sleep(j.workSleep)
				}
			}
			id = lastID
		}
	}()

	j.cleanResource(work)
}
