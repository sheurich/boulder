package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"gopkg.in/gorp.v1"

	"github.com/jmhodges/clock"
	"github.com/letsencrypt/boulder/cmd"
	blog "github.com/letsencrypt/boulder/log"
	"github.com/letsencrypt/boulder/sa"
)

type contactExporter struct {
	log   blog.Logger
	dbMap *gorp.DbMap
	clk   clock.Clock
	grace time.Duration
}

type contact struct {
	ID int64 `json:"id"`
}

// Find all registration contacts with unexpired certificates.
func (c contactExporter) findContacts() ([]contact, error) {
	var contactsList []contact
	_, err := c.dbMap.Select(
		&contactsList,
		`SELECT id
		FROM registrations
		WHERE contact != 'null' AND
			id IN (
				SELECT registrationID
				FROM certificates
				WHERE expires >= :expireCutoff
			);`,
		map[string]interface{}{
			"expireCutoff": c.clk.Now().Add(-c.grace),
		})
	if err != nil {
		c.log.AuditErr(fmt.Sprintf("Error finding contacts: %s", err))
		return nil, err
	}

	return contactsList, nil
}

// The `writeContacts` function produces a file containing JSON serialized
// contact objects
func writeContacts(contactsList []contact, outfile string) error {
	data, err := json.Marshal(contactsList)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if outfile != "" {
		return ioutil.WriteFile(outfile, data, 0644)
	} else {
		fmt.Printf("%s", data)
		return nil
	}
}

const usageIntro = `
Introduction:

The contact exporter exists to retrieve the IDs of all registered
users with currently unexpired certificates. This list of registration IDs can
then be given as input to the notification mailer to send bulk notifications.

The -grace parameter can be used to allow registrations with certificates that
have already expired to be included in the export. The argument is a Go duration
obeying the usual suffix rules (e.g. 24h).

Registration IDs are favoured over email addresses as the intermediate format in
order to ensure the most up to date contact information is used at the time of
notification. The notification mailer will resolve the ID to email(s) when the
mailing is underway, ensuring we use the correct address if a user has updated
their contact information between the time of export and the time of
notification.

The contact exporter's registration ID output will be JSON of the form:
  [
   { "id": 1 },
   ...
   { "id": n }
  ]

Examples:
  Export all registration IDs with unexpired certificates to "regs.json":

  contact-exporter -config test/config/contact-exporter.json -outfile regs.json

  Export all registration IDs with certificates that are unexpired or expired
  within the last two days to "regs.json":

  contact-exporter -config test/config/contact-exporter.json -grace 48h -outfile
    "regs.json"

Required arguments:
- config
- outfile`

func main() {
	outFile := flag.String("outfile", "", "File to write contacts to (defaults to stdout).")
	grace := flag.Duration("grace", 2*24*time.Hour, "Include contacts with certificates that expired in < grace ago")
	type config struct {
		ContactExporter struct {
			cmd.DBConfig
			cmd.PasswordConfig
		}
	}
	configFile := flag.String("config", "", "File containing a JSON config.")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "%s\n\n", usageIntro)
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
	}

	flag.Parse()
	if *outFile == "" || *configFile == "" {
		flag.Usage()
		os.Exit(1)
	}

	_, log := cmd.StatsAndLogging(cmd.StatsdConfig{}, cmd.SyslogConfig{StdoutLevel: 7})

	configData, err := ioutil.ReadFile(*configFile)
	cmd.FailOnError(err, fmt.Sprintf("Reading %q", *configFile))
	var cfg config
	err = json.Unmarshal(configData, &cfg)
	cmd.FailOnError(err, "Unmarshaling config")

	dbURL, err := cfg.ContactExporter.DBConfig.URL()
	cmd.FailOnError(err, "Couldn't load DB URL")
	dbMap, err := sa.NewDbMap(dbURL, 10)
	cmd.FailOnError(err, "Could not connect to database")

	exporter := contactExporter{
		log:   log,
		dbMap: dbMap,
		clk:   cmd.Clock(),
		grace: *grace,
	}

	contacts, err := exporter.findContacts()
	cmd.FailOnError(err, "Could not find contacts")

	err = writeContacts(contacts, *outFile)
	cmd.FailOnError(err, fmt.Sprintf("Could not write contacts to outfile %q", *outFile))
}
