package main

import (
	"github.com/jmhodges/clock"
	blog "github.com/letsencrypt/boulder/log"
)

// newCertificatesPerNameJob returns a batchedDBJob configured to delete expired
// rows from the certificatesPerName table.
func newCertificatesPerNameJob(
	db janitorDB,
	log blog.Logger,
	clk clock.Clock,
	config Config) *batchedDBJob {
	purgeBefore := clk.Now().Add(-config.Janitor.CertificatesPerName.GracePeriod.Duration)
	workQuery := `SELECT id FROM certificatesPerName
		 WHERE
		   id > :startID AND
		   time <= :cutoff
		 ORDER by id
		 LIMIT :limit`
	log.Debugf("Creating Certificates job from config: %#v\n", config.Janitor.CertificatesPerName)
	return &batchedDBJob{
		db:          db,
		log:         log,
		purgeBefore: purgeBefore,
		batchSize:   config.Janitor.CertificatesPerName.BatchSize,
		maxDPS:      config.Janitor.CertificatesPerName.MaxDPS,
		parallelism: config.Janitor.CertificatesPerName.Parallelism,
		table:       "certificatesPerName",
		workQuery:   workQuery,
	}
}
