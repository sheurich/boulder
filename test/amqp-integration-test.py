#!/usr/bin/env python2.7
import atexit
import base64
import os
import re
import shutil
import socket
import subprocess
import sys
import tempfile
import urllib
import time
import urllib2

import startservers


class ExitStatus:
    OK, PythonFailure, NodeFailure, Error, OCSPFailure, CTFailure = range(6)


class ProcInfo:
    """
        Args:
            cmd (str): The command that was run
            proc(subprocess.Popen): The Popen of the command run
    """

    def __init__(self, cmd, proc):
        self.cmd = cmd
        self.proc = proc


def die(status):
    global exit_status
    # Set exit_status so cleanup handler knows what to report.
    exit_status = status
    sys.exit(exit_status)

def fetch_ocsp(request_bytes, url):
    """Fetch an OCSP response using POST, GET, and GET with URL encoding.

    Returns a tuple of the responses.
    """
    ocsp_req_b64 = base64.b64encode(request_bytes)

    # Make the OCSP request three different ways: by POST, by GET, and by GET with
    # URL-encoded parameters. All three should have an identical response.
    get_response = urllib2.urlopen("%s/%s" % (url, ocsp_req_b64)).read()
    get_encoded_response = urllib2.urlopen("%s/%s" % (url, urllib.quote(ocsp_req_b64, safe = ""))).read()
    post_response = urllib2.urlopen("%s/" % (url), request_bytes).read()

    return (post_response, get_response, get_encoded_response)

def make_ocsp_req(cert_file, issuer_file):
    """Return the bytes of an OCSP request for the given certificate file."""
    ocsp_req_file = os.path.join(tempdir, "ocsp.req")
    # First generate the OCSP request in DER form
    cmd = ("openssl ocsp -no_nonce -issuer %s -cert %s -reqout %s" % (
        issuer_file, cert_file, ocsp_req_file))
    print cmd
    subprocess.check_output(cmd, shell=True)
    with open(ocsp_req_file) as f:
        ocsp_req = f.read()
    return ocsp_req

def fetch_until(cert_file, issuer_file, url, initial, final):
    """Fetch OCSP for cert_file until OCSP status goes from initial to final.

    Initial and final are treated as regular expressions. Any OCSP response
    whose OpenSSL OCSP verify output doesn't match either initial or final is
    a fatal error.

    If OCSP responses by the three methods (POST, GET, URL-encoded GET) differ
    from each other, that is a fatal error.

    If we loop for more than five seconds, that is a fatal error.

    Returns nothing on success.
    """
    ocsp_request = make_ocsp_req(cert_file, issuer_file)
    timeout = time.time() + 5
    while True:
        time.sleep(0.25)
        if time.time() > timeout:
            print("Timed out waiting for OCSP to go from '%s' to '%s'" % (
                initial, final))
            die(ExitStatus.OCSPFailure)
        responses = fetch_ocsp(ocsp_request, url)
        # This variable will be true at the end of the loop if all the responses
        # matched the final state.
        all_final = True
        for resp in responses:
            verify_output = ocsp_verify(cert_file, issuer_file, resp)
            if re.search(initial, verify_output):
                all_final = False
                break
            elif re.search(final, verify_output):
                continue
            else:
                print verify_output
                print("OCSP response didn't match '%s' or '%s'" %(
                    initial, final))
                die(ExitStatus.OCSPFailure)
        if all_final:
            # Check that all responses were equal to each other.
            for resp in responses:
                if resp != responses[0]:
                    print "OCSP responses differed:"
                    print(base64.b64encode(responses[0]))
                    print(" vs ")
                    print(base64.b64encode(resp))
                    die(ExitStatus.OCSPFailure)
            return

def ocsp_verify(cert_file, issuer_file, ocsp_response):
    ocsp_resp_file = os.path.join(tempdir, "ocsp.resp")
    with open(ocsp_resp_file, "w") as f:
        f.write(ocsp_response)
    ocsp_verify_cmd = """openssl ocsp -no_nonce -issuer %s -cert %s \
      -verify_other %s -CAfile ../test-root.pem \
      -respin %s""" % (issuer_file, cert_file, issuer_file, ocsp_resp_file)
    print ocsp_verify_cmd
    try:
        output = subprocess.check_output(ocsp_verify_cmd,
            shell=True, stderr=subprocess.STDOUT)
    except subprocess.CalledProcessError as e:
        output = e.output
        print output
        print "subprocess returned non-zero: %s" % e
        die(ExitStatus.OCSPFailure)
    # OpenSSL doesn't always return non-zero when response verify fails, so we
    # also look for the string "Response Verify Failure"
    verify_failure = "Response Verify Failure"
    if re.search(verify_failure, output):
        print output
        die(ExitStatus.OCSPFailure)
    return output

def wait_for_ocsp_good(cert_file, issuer_file, url):
    fetch_until(cert_file, issuer_file, url, " unauthorized", ": good")

def wait_for_ocsp_revoked(cert_file, issuer_file, url):
    fetch_until(cert_file, issuer_file, url, ": good", ": revoked")

def verify_ct_submission(expectedSubmissions, url):
    resp = urllib2.urlopen(url)
    submissionStr = resp.read()
    if int(submissionStr) != expectedSubmissions:
        print "Expected %d submissions, found %d" % (expectedSubmissions, int(submissionStr))
        die(ExitStatus.CTFailure)

def run_node_test():
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    try:
        s.connect(('localhost', 4000))
    except socket.error, e:
        print("Cannot connect to WFE")
        die(ExitStatus.Error)

    os.chdir('test/js')

    if subprocess.Popen('npm install', shell=True).wait() != 0:
        print("\n Installing NPM modules failed")
        die(ExitStatus.Error)
    cert_file = os.path.join(tempdir, "cert.der")
    cert_file_pem = os.path.join(tempdir, "cert.pem")
    key_file = os.path.join(tempdir, "key.pem")
    # Pick a random hostname so we don't run into certificate rate limiting.
    domain = subprocess.check_output("openssl rand -hex 6", shell=True).strip()
    # Issue the certificate and transform it from DER-encoded to PEM-encoded.
    if subprocess.Popen('''
        node test.js --email foo@letsencrypt.org --agree true \
          --domains www.%s-TEST.com --new-reg http://localhost:4000/acme/new-reg \
          --certKey %s --cert %s && \
        openssl x509 -in %s -out %s -inform der -outform pem
        ''' % (domain, key_file, cert_file, cert_file, cert_file_pem),
        shell=True).wait() != 0:
        print("\nIssuing failed")
        die(ExitStatus.NodeFailure)

    ee_ocsp_url = "http://localhost:4002"
    issuer_ocsp_url = "http://localhost:4003"

    # As OCSP-Updater is generating responses independently of the CA we sit in a loop
    # checking OCSP until we either see a good response or we timeout (5s).
    wait_for_ocsp_good(cert_file_pem, "../test-ca.pem", ee_ocsp_url)

    # Verify that the static OCSP responder, which answers with a
    # pre-signed, long-lived response for the CA cert, works.
    wait_for_ocsp_good("../test-ca.pem", "../test-root.pem", issuer_ocsp_url)

    verify_ct_submission(1, "http://localhost:4500/submissions")

    if subprocess.Popen('''
        node revoke.js %s %s http://localhost:4000/acme/revoke-cert
        ''' % (cert_file, key_file), shell=True).wait() != 0:
        print("\nRevoking failed")
        die(ExitStatus.NodeFailure)

    wait_for_ocsp_revoked(cert_file_pem, "../test-ca.pem", ee_ocsp_url)
    return 0


def run_client_tests():
    root = os.environ.get("LETSENCRYPT_PATH")
    assert root is not None, (
        "Please set LETSENCRYPT_PATH env variable to point at "
        "initialized (virtualenv) client repo root")
    test_script_path = os.path.join(root, 'tests', 'boulder-integration.sh')
    cmd = "source %s/venv/bin/activate && SIMPLE_HTTP_PORT=5002 %s" % (root, test_script_path)
    if subprocess.Popen(cmd, shell=True, cwd=root, executable='/bin/bash').wait() != 0:
        die(ExitStatus.PythonFailure)


@atexit.register
def cleanup():
    import shutil
    shutil.rmtree(tempdir)
    if exit_status == ExitStatus.OK:
        print("\n\nSUCCESS")
    else:
        print("\n\nFAILURE %d" % exit_status)


exit_status = ExitStatus.OK
tempdir = tempfile.mkdtemp()
if not startservers.start(race_detection=True):
    die(ExitStatus.Error)
run_node_test()
run_client_tests()
if not startservers.check():
    die(ExitStatus.Error)
