# Load testing the OCSP signing components.

Here are instructions on how to realistically load test the OCSP signing
components of Boulder, exercising the pkcs11key, boulder-ca, and
ocsp-updater components.

Set up a SoftHSM instance running pkcs11-daemon on some remote host with more
CPUs than your local machine. Easiest way to do this is to clone the Boulder
repo, and on the remote machine run:

    remote-machine$ docker-compose run -p 5657:5657 bhsm

Check that the port is open:

    local-machine$ nc -zv remote-machine 5657
    Connection to remote-machine 5657 port [tcp/*] succeeded!

Set up a handy alias:

    local-machine$ alias drun="docker-compose run -e PKCS11_PROXY_SOCKET=tcp://remote-machine:5657 -e FAKE_DNS=172.17.0.1 --service-ports"

Run the pkcs11key benchmark to check raw signing speed at various settings for SESSIONS:

    local-machine$ drun -e SESSIONS=4 -e MODULE=/usr/local/lib/libpkcs11-proxy.so --entrypoint /go/src/github.com/letsencrypt/pkcs11key/test.sh boulder

Initialize the tokens for use by Boulder:

    local-machine$ drun --entrypoint "softhsm --module /usr/local/lib/libpkcs11-proxy.so --init-token --pin 5678 --so-pin 1234 --slot 0 --label intermediate" boulder
    local-machine$ drun --entrypoint "softhsm --module /usr/local/lib/libpkcs11-proxy.so --init-token --pin 5678 --so-pin 1234 --slot 1 --label root" boulder

Run a local Boulder instance:

    local-machine$ drun boulder ./start.py

Issue a bunch of certificates with test.js, ideally more than a hundred. Note:
you may already have more than a hundred certificates already issued in your
local database. If so, no need to do more.

Using a MySQL client, artificially make all the OCSP responses go stale (9 days
is chosen to be between ocspStaleMaxAge and ocspMinTimeToExpiry in
test/config/ocsp-updater.json):

    local-machine$ docker-compose run boulder mysql -h boulder-mysql -u root -D boulder_sa_integration
    MariaDB [boulder_sa_integration]> update certificateStatus set ocspLastUpdated = DATE_SUB(NOW(), INTERVAL 9 DAY);
    Query OK, 641 rows affected (0.02 sec)
    Rows matched: 641  Changed: 641  Warnings: 0

Then, query the ocspLastUpdated field, grouping by second. You should see the
numbers updating over the next few seconds. The count per second gives you a
rough idea of how quickly ocsp-updater was able to refresh the results.

    MariaDB [boulder_sa_integration]> select count(*), ocspLastUpdated from certificateStatus group by ocspLastUpdated;
    +----------+---------------------+
    | count(*) | ocspLastUpdated     |
    +----------+---------------------+
    |      271 | 2016-12-21 07:00:22 |
    |       19 | 2016-12-30 07:00:22 |
    |       75 | 2016-12-30 07:00:23 |
    |       88 | 2016-12-30 07:00:24 |
    |       88 | 2016-12-30 07:00:25 |
    |       86 | 2016-12-30 07:00:26 |
    |       15 | 2016-12-30 07:00:27 |
    +----------+---------------------+

For instance, this represents a peak speed of about 88 signatures per second.

If you vary the NumSessions config value in test/config/ca.json, you should see
the signing speed vary linearly, up to the number of cores in the remote
machine. Note that hyperthreaded cores look like 2 cores but may only perform
as 1 (needs testing).

Keep in mind that round-trip time between your local machine and your HSM
machine greatly impact signing speed.
