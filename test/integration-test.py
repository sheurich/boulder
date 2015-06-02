#!/usr/bin/env python2.7
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import time

tempdir = tempfile.mkdtemp()

exit_status = 0

def die():
    global exit_status
    exit_status = 1
    sys.exit(1)

def build(path):
    cmd = 'go build -o %s/%s %s' % (tempdir, os.path.basename(path), path)
    print(cmd)
    if subprocess.Popen(cmd, shell=True).wait() != 0:
        die()

# A strange Go bug: If cfssl is up-to-date, we'll get a failure building
# Boulder. Work around by touching cfssl.go.
subprocess.Popen('touch Godeps/_workspace/src/github.com/cloudflare/cfssl/cmd/cfssl/cfssl.go', shell=True).wait()
build('./cmd/boulder')
build('./Godeps/_workspace/src/github.com/cloudflare/cfssl/cmd/cfssl')

boulder = subprocess.Popen('''
    exec %s/boulder --config test/boulder-test-config.json
    ''' % tempdir, shell=True)

cfssl = subprocess.Popen('''
    exec %s/cfssl \
      -loglevel 0 \
      serve \
      -port 9300 \
      -ca test/test-ca.pem \
      -ca-key test/test-ca.key \
      -config test/cfssl-config.json
    ''' % tempdir, shell=True, stdout=None)

def run_test():
    os.chdir('test/js')

    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    # Wait up to 7 seconds for Boulder to come up.
    for i in range(0, 7):
        try:
            s.connect(('localhost', 4300))
            break
        except socket.error, e:
            pass
        time.sleep(1)

    if subprocess.Popen('npm install', shell=True).wait() != 0:
        die()

    issue = subprocess.Popen('''
        node test.js --email foo@bar.com --agree true \
          --domains foo.com --new-reg http://localhost:4300/acme/new-reg \
          --certKey %s/key.pem --cert %s/cert.der
        ''' % (tempdir, tempdir), shell=True)
    if issue.wait() != 0:
        die()
    revoke = subprocess.Popen('''
        node revoke.js %s/cert.der %s/key.pem http://localhost:4300/acme/revoke-cert/
        ''' % (tempdir, tempdir), shell=True)
    if revoke.wait() != 0:
        die()

try:
    run_test()
except Exception as e:
    exit_status = 1
    print e
finally:
    # Check whether boulder died. This can happen, for instance, if there was an
    # existing boulder already listening on the port.
    if boulder.poll() is not None:
        print("Boulder died")
        exit_status = 1
    else:
        boulder.kill()

    if cfssl.poll() is not None:
        print("CFSSL died")
        exit_status = 1
    else:
        cfssl.kill()

    shutil.rmtree(tempdir)

    if exit_status == 0:
        print("SUCCESS")
    else:
        print("FAILURE")
    sys.exit(exit_status)
