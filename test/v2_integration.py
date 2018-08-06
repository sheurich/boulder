#!/usr/bin/env python2.7
"""
Integration test cases for ACMEv2 as implemented by boulder-wfe2.
"""
import random
import subprocess
import requests
import datetime
import time
import os
import json

import OpenSSL

from cryptography import x509
from cryptography.hazmat.backends import default_backend
from cryptography.hazmat.primitives.asymmetric import rsa

import chisel2

from acme.messages import Status, CertificateRequest, Directory
from acme import crypto_util as acme_crypto_util
from acme import client as acme_client
from acme import messages

import josepy

def random_domain():
    """Generate a random domain for testing (to avoid rate limiting)."""
    return "rand.%x.xyz" % random.randrange(2**32)

def test_multidomain():
    chisel2.auth_and_issue([random_domain(), random_domain()])

def test_wildcardmultidomain():
    """
    Test issuance for a random domain and a random wildcard domain using DNS-01.
    """
    chisel2.auth_and_issue([random_domain(), "*."+random_domain()], chall_type="dns-01")

def test_http_challenge():
    chisel2.auth_and_issue([random_domain(), random_domain()], chall_type="http-01")

def test_tls_alpn_challenge():
    if not default_config_dir.startswith("test/config-next"):
        return
    chisel2.auth_and_issue([random_domain(), random_domain()], chall_type="tls-alpn-01")

def test_overlapping_wildcard():
    """
    Test issuance for a random domain and a wildcard version of the same domain
    using DNS-01. This should result in *two* distinct authorizations.
    """
    domain = random_domain()
    domains = [ domain, "*."+domain ]
    client = chisel2.make_client(None)
    csr_pem = chisel2.make_csr(domains)
    order = client.new_order(csr_pem)
    authzs = order.authorizations

    if len(authzs) != 2:
        raise Exception("order for %s had %d authorizations, expected 2" %
                (domains, len(authzs)))

    cleanup = chisel2.do_dns_challenges(client, authzs)
    try:
        order = client.poll_and_finalize(order)
    finally:
        cleanup()

def test_wildcard_exactblacklist():
    """
    Test issuance for a wildcard that would cover an exact blacklist entry. It
    should fail with a policy error.
    """

    # We include "highrisk.le-test.hoffman-andrews.com" in `test/hostname-policy.json`
    # Issuing for "*.le-test.hoffman-andrews.com" should be blocked
    domain = "*.le-test.hoffman-andrews.com"
    # We expect this to produce a policy problem
    chisel2.expect_problem("urn:ietf:params:acme:error:rejectedIdentifier",
        lambda: chisel2.auth_and_issue([domain], chall_type="dns-01"))

def test_wildcard_authz_reuse():
    """
    Test that an authorization for a base domain obtained via HTTP-01 isn't
    reused when issuing a wildcard for that base domain later on.
    """

    # Create one client to reuse across multiple issuances
    client = chisel2.make_client(None)

    # Pick a random domain to issue for
    domains = [ random_domain() ]
    csr_pem = chisel2.make_csr(domains)

    # Submit an order for the name
    order = client.new_order(csr_pem)
    # Complete the order via an HTTP-01 challenge
    cleanup = chisel2.do_http_challenges(client, order.authorizations)
    try:
        order = client.poll_and_finalize(order)
    finally:
        cleanup()

    # Now try to issue a wildcard for the random domain
    domains[0] = "*." + domains[0]
    csr_pem = chisel2.make_csr(domains)
    order = client.new_order(csr_pem)

    # We expect all of the returned authorizations to be pending status
    for authz in order.authorizations:
        if authz.body.status != Status("pending"):
            raise Exception("order for %s included a non-pending authorization (status: %s) from a previous HTTP-01 order" %
                    ((domains), str(authz.body.status)))

def test_bad_overlap_wildcard():
    if not os.environ.get('BOULDER_CONFIG_DIR', '').startswith("test/config-next"):
        return
    chisel2.expect_problem("urn:ietf:params:acme:error:malformed",
        lambda: chisel2.auth_and_issue(["*.example.com", "www.example.com"]))

def test_duplicate_orders():
    """
    Test that the same client issuing for the same domain names twice in a row
    works without error.
    """
    client = chisel2.make_client(None)
    domains = [ random_domain() ]
    chisel2.auth_and_issue(domains, client=client)
    chisel2.auth_and_issue(domains, client=client)

def test_order_reuse_failed_authz():
    """
    Test that creating an order for a domain name, failing an authorization in
    that order, and submitting another new order request for the same name
    doesn't reuse a failed authorizaton in the new order.
    """

    client = chisel2.make_client(None)
    domains = [ random_domain() ]
    csr_pem = chisel2.make_csr(domains)

    order = client.new_order(csr_pem)
    firstOrderURI = order.uri

    # Pick the first authz's first challenge, doesn't matter what type it is
    chall_body = order.authorizations[0].body.challenges[0]
    # Answer it, but with nothing set up to solve the challenge request
    client.answer_challenge(chall_body, chall_body.response(client.net.key))

    # Poll for a fixed amount of time checking for the order to become invalid
    # from the authorization attempt initiated above failing
    deadline = datetime.datetime.now() + datetime.timedelta(seconds=60)
    while datetime.datetime.now() < deadline:
        time.sleep(1)
        updatedOrder = requests.get(firstOrderURI).json()
        if updatedOrder['status'] == "invalid":
            break

    # If the loop ended and the status isn't invalid then we reached the
    # deadline waiting for the order to become invalid, fail the test
    if updatedOrder['status'] != "invalid":
        raise Exception("timed out waiting for order %s to become invalid" % firstOrderURI)

    # Make another order with the same domains
    order = client.new_order(csr_pem)

    # It should not be the same order as before
    if order.uri == firstOrderURI:
        raise Exception("new-order for %s returned a , now-invalid, order" % domains)

    # We expect all of the returned authorizations to be pending status
    for authz in order.authorizations:
        if authz.body.status != Status("pending"):
            raise Exception("order for %s included a non-pending authorization (status: %s) from a previous order" %
                    ((domains), str(authz.body.status)))

    # We expect the new order can be fulfilled
    cleanup = chisel2.do_http_challenges(client, order.authorizations)
    try:
        order = client.poll_and_finalize(order)
    finally:
        cleanup()

def test_order_finalize_early():
    """
    Test that finalizing an order before its fully authorized results in the
    order having an error set and the status being invalid.
    """
    # Create a client
    client = chisel2.make_client(None)

    # Create a random domain and a csr
    domains = [ random_domain() ]
    csr_pem = chisel2.make_csr(domains)

    # Create an order for the domain
    order = client.new_order(csr_pem)

    deadline = datetime.datetime.now() + datetime.timedelta(seconds=5)

    # Finalizing an order early should generate an unauthorized error and we
    # should check that the order is invalidated.
    chisel2.expect_problem("urn:ietf:params:acme:error:unauthorized",
        lambda: client.finalize_order(order, deadline))

    # Poll for a fixed amount of time checking for the order to become invalid
    # from the early finalization attempt initiated above failing
    while datetime.datetime.now() < deadline:
        time.sleep(1)
        updatedOrder = requests.get(order.uri).json()
        if updatedOrder['status'] == "invalid":
            break

    # If the loop ended and the status isn't invalid then we reached the
    # deadline waiting for the order to become invalid, fail the test
    if updatedOrder['status'] != "invalid":
        raise Exception("timed out waiting for order %s to become invalid" % order.uri)

    # The order should have an error with the expected type
    if updatedOrder['error']['type'] != 'urn:ietf:params:acme:error:unauthorized':
        raise Exception("order %s has incorrect error field type: \"%s\"" % (order.uri, updatedOrder['error']['type']))

def test_revoke_by_issuer():
    client = chisel2.make_client(None)
    order = chisel2.auth_and_issue([random_domain()], client=client)

    cert = OpenSSL.crypto.load_certificate(OpenSSL.crypto.FILETYPE_PEM, order.fullchain_pem)
    client.revoke(josepy.ComparableX509(cert), 0)

def test_revoke_by_authz():
    domains = [random_domain()]
    order = chisel2.auth_and_issue(domains)

    # create a new client and re-authz
    client = chisel2.make_client(None)
    chisel2.auth_and_issue(domains, client=client)

    cert = OpenSSL.crypto.load_certificate(OpenSSL.crypto.FILETYPE_PEM, order.fullchain_pem)
    client.revoke(josepy.ComparableX509(cert), 0)

def test_revoke_by_privkey():
    client = chisel2.make_client(None)
    domains = [random_domain()]
    key = OpenSSL.crypto.PKey()
    key.generate_key(OpenSSL.crypto.TYPE_RSA, 2048)
    key_pem = OpenSSL.crypto.dump_privatekey(OpenSSL.crypto.FILETYPE_PEM, key)
    csr_pem = chisel2.make_csr(domains)
    order = client.new_order(csr_pem)
    cleanup = chisel2.do_http_challenges(client, order.authorizations)
    try:
        order = client.poll_and_finalize(order)
    finally:
        cleanup()

    # Create a new client with the JWK as the cert private key
    jwk = josepy.JWKRSA(key=key)
    net = acme_client.ClientNetwork(key, user_agent="Boulder integration tester")

    directory = Directory.from_json(net.get(chisel2.DIRECTORY_V2).json())
    new_client = acme_client.ClientV2(directory, net)

    cert = OpenSSL.crypto.load_certificate(OpenSSL.crypto.FILETYPE_PEM, order.fullchain_pem)
    client.revoke(josepy.ComparableX509(cert), 0)

def test_sct_embedding():
    if not os.environ.get('BOULDER_CONFIG_DIR', '').startswith("test/config-next"):
        return
    order = chisel2.auth_and_issue([random_domain()])
    cert = x509.load_pem_x509_certificate(str(order.fullchain_pem), default_backend())

    # make sure there is no poison extension
    try:
        cert.extensions.get_extension_for_oid(x509.ObjectIdentifier("1.3.6.1.4.1.11129.2.4.3"))
        raise Exception("certificate contains CT poison extension")
    except x509.ExtensionNotFound:
        # do nothing
        pass

    # make sure there is a SCT list extension
    try:
        sctList = cert.extensions.get_extension_for_oid(x509.ObjectIdentifier("1.3.6.1.4.1.11129.2.4.2"))
    except x509.ExtensionNotFound:
        raise Exception("certificate doesn't contain SCT list extension")
    if len(sctList.value) != 2:
        raise Exception("SCT list contains wrong number of SCTs")
    for sct in sctList.value:
        if sct.version != x509.certificate_transparency.Version.v1:
            raise Exception("SCT contains wrong version")
        if sct.entry_type != x509.certificate_transparency.LogEntryType.PRE_CERTIFICATE:
            raise Exception("SCT contains wrong entry type")

def test_only_return_existing_reg():
    client = chisel2.uninitialized_client()
    email = "test@not-example.com"
    client.new_account(messages.NewRegistration.from_data(email=email,
            terms_of_service_agreed=True))
    
    client = chisel2.uninitialized_client(key=client.net.key)
    class extendedAcct(dict):
        def json_dumps(self, indent=None):
            return json.dumps(self)
    acct = extendedAcct({
        "termsOfServiceAgreed": True,
        "contact": [email],
        "onlyReturnExisting": True
    })
    resp = client.net.post(client.directory['newAccount'], acct, acme_version=2)
    if resp.status_code != 200:
        raise Exception("incorrect response returned for onlyReturnExisting")

    other_client = chisel2.uninitialized_client()
    newAcct = extendedAcct({
        "termsOfServiceAgreed": True,
        "contact": [email],
        "onlyReturnExisting": True
    })
    chisel2.expect_problem("urn:ietf:params:acme:error:accountDoesNotExist",
        lambda: other_client.net.post(other_client.directory['newAccount'], newAcct, acme_version=2))

def run(cmd, **kwargs):
    return subprocess.check_output(cmd, shell=True, stderr=subprocess.STDOUT, **kwargs)
