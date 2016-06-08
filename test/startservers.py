import atexit
import BaseHTTPServer
import errno
import os
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time

default_config = os.environ.get('BOULDER_CONFIG')
if default_config is None:
    default_config = 'test/boulder-config.json'
processes = []


def install(race_detection):
    # Pass empty BUILD_TIME and BUILD_ID flags to avoid constantly invalidating the
    # build cache with new BUILD_TIMEs, or invalidating it on merges with a new
    # BUILD_ID.
    cmd = "make GO_BUILD_FLAGS=''  "
    if race_detection:
        cmd = "make GO_BUILD_FLAGS='-race -tags \"integration\"'"

    return subprocess.call(cmd, shell=True) == 0

def run(cmd, race_detection):
    # Note: Must use exec here so that killing this process kills the command.
    cmd = """GORACE="halt_on_error=1" exec ./bin/%s""" % cmd
    p = subprocess.Popen(cmd, shell=True)
    p.cmd = cmd
    return p

def start(race_detection):
    """Return True if everything builds and starts.

    Give up and return False if anything fails to build, or dies at
    startup. Anything that did start before this point can be cleaned
    up explicitly by calling stop(), or automatically atexit.
    """
    global processes
    forward()
    progs = [
        'boulder-wfe --config %s' % default_config,
        'boulder-ra --config %s' % default_config,
        'boulder-sa --config %s' % default_config,
        'boulder-ca --config %s' % default_config,
        'boulder-va --config %s' % default_config,
        'boulder-publisher --config %s' % default_config,
        'ocsp-updater --config %s' % default_config,
        'ocsp-responder --config %s' % default_config,
        'ct-test-srv --config %s' % default_config,
        'dns-test-srv --config %s' % default_config,
        'mail-test-srv --config %s' % default_config,
        'ocsp-responder --config test/issuer-ocsp-responder.json',
        'caa-checker --config cmd/caa-checker/test-config.yml'
    ]
    if not install(race_detection):
        return False
    for prog in progs:
        try:
            processes.append(run(prog, race_detection))
        except Exception as e:
            print(e)
            return False
        if not check():
            # Don't keep building stuff if a server has already died.
            return False

    # Wait until all servers are up before returning to caller. This means
    # checking each server's debug port until it's available.
    while True:
        try:
            time.sleep(0.3)
            # If one of the servers has died, quit immediately.
            if not check():
                return False
            ports = range(8000, 8005) + [4000]
            for debug_port in ports:
                s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
                s.connect(('localhost', debug_port))
                s.close()
            break
        except socket.error as e:
            if e.errno == errno.ECONNREFUSED:
                print "Waiting for debug port %d" % debug_port
            else:
                raise

    # Some servers emit extra text after their debug server is open. Sleep 1
    # second so the "servers running" message comes last.
    time.sleep(1)
    print "All servers running. Hit ^C to kill."
    return True

def forward():
    """Add a TCP forwarder between Boulder and RabbitMQ to simulate failures."""
    cmd = """exec listenbuddy -listen :5673 -speak boulder-rabbitmq:5672"""
    p = subprocess.Popen(cmd, shell=True)
    p.cmd = cmd
    print('started %s with pid %d' % (p.cmd, p.pid))
    global processes
    processes.insert(0, p)

def bounce_forward():
    """Kill all forwarded TCP connections."""
    global processes
    processes[0].send_signal(signal.SIGUSR1)

def check():
    """Return true if all started processes are still alive.

    Log about anything that died.
    """
    global processes
    busted = []
    stillok = []
    for p in processes:
        if p.poll() is None:
            stillok.append(p)
        else:
            busted.append(p)
    if busted:
        print "\n\nThese processes exited early (check above for their output):"
        for p in busted:
            print "\t'%s' with pid %d exited %d" % (p.cmd, p.pid, p.returncode)
    processes = stillok
    return not busted


@atexit.register
def stop():
    for p in processes:
        if p.poll() is None:
            p.kill()
