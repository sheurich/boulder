#!/bin/bash
#
# Run this script to generate a SoftHSM config and import test-ca.key to use
# with Boulder. Note that we don't check in the generated config or database
# because they require absolute paths.
#

if [ -r /proc/brcm_monitor0 ]; then
  echo "The /proc/brcm_monitor0 file has open permissions. Please run"
  echo " # chmod 600 /proc/brcm_monitor0"
  echo "as root to avoid crashing the system."
  echo https://bugs.launchpad.net/ubuntu/+source/bcmwl/+bug/1450825
  exit 2
fi

cd $(dirname $0)
export SOFTHSM_CONF=$PWD/softhsm.conf
echo 0:${PWD}/softhsm.db > ${SOFTHSM_CONF}
softhsm --init-token --slot 0 --label token_label --pin 5678 --so-pin 1234
softhsm --slot 0 --import test-ca.key  --label key_label --pin 5678 --id FF
echo "Add this to your .bashrc:"
echo "export SOFTHSM_CONF=${SOFTHSM_CONF}"
echo "And edit test/test-ca.key-pkcs11.json to have:"
echo '"module": "/usr/lib/softhsm/libsofthsm.so"'
