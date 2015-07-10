#!/bin/bash
# Run all tests and coverage checks. Called from Travis automatically, also
# suitable to run manually. See list of prerequisite packages in .travis.yml
if type realpath >/dev/null 2>&1 ; then
  cd $(realpath $(dirname $0))
fi

FAILURE=0

TESTDIRS="analysis \
          ca \
          core \
          log \
          policy \
          ra \
          rpc \
          sa \
          test \
          va \
          wfe"
          # cmd
          # Godeps

# We need to know, for github-pr-status, what the triggering commit is.
# Assume first it's the travis commit (for builds of master), unless we're
# a PR, when it's actually the first parent.
TRIGGER_COMMIT=${TRAVIS_COMMIT}
if [ "${TRAVIS_PULL_REQUEST}" != "false" ] ; then
  revs=$(git rev-list --parents -n 1 HEAD)
  # The trigger commit is the last ID in the space-delimited rev-list
  TRIGGER_COMMIT=${revs##* }
fi

GITHUB_SECRET_FILE="$(pwd)/test/github-secret.json"

start_context() {
  CONTEXT="$1"
  printf "[%16s] Starting\n" ${CONTEXT}
}

end_context() {
  printf "[%16s] Done\n" ${CONTEXT}
  CONTEXT=""
}

update_status() {
  if ([ "${TRAVIS}" == "true" ] && [ "x${CONTEXT}" != "x" ]) && [ -f "${GITHUB_SECRET_FILE}" ]; then
    github-pr-status --authfile $GITHUB_SECRET_FILE \
      --owner "letsencrypt" --repo "boulder" \
      status --sha "${TRIGGER_COMMIT}" --context "${CONTEXT}" $*
  fi
}

run() {
  echo "$*"
  $* 2>&1
  local status=$?

  if [ ${status} -eq 0 ]; then
    update_status --state success
    echo "Success: $*"
  else
    FAILURE=1
    update_status --state failure
    echo "[!] FAILURE: $*"
  fi

  return ${status}
}

run_and_comment() {
  if [ "x${TRAVIS}" = "x" ] || [ "${TRAVIS_PULL_REQUEST}" == "false" ] || [ ! -f "${GITHUB_SECRET_FILE}"] ; then
    run $*
  else
    result=$(run $*)
    local status=$?
    # Only send a comment if exit code > 0
    if [ ${status} -ne 0 ] ; then
      echo $'```\n'${result}$'\n```' | github-pr-status --authfile $GITHUB_SECRET_FILE \
        --owner "letsencrypt" --repo "boulder" \
        comment --pr ${TRAVIS_PULL_REQUEST}
    fi
  fi
}

# Path for installed go package binaries. If yours is different, override with
# GOBIN=/my/path/to/bin ./test.sh
GOBIN=${GOBIN:-$HOME/gopath/bin}

#
# Run Go Vet, a correctness-focused static analysis tool
#

start_context "test/vet"
run_and_comment go vet ./...
end_context #test/vet

#
# Run Go Lint, a style-focused static analysis tool
#

start_context "test/golint"
[ -x "$(which golint)" ] && run golint ./...
end_context #test/golint

#
# Ensure all files are formatted per the `go fmt` tool
#
start_context "test/gofmt"
unformatted=$(find . -name "*.go" -not -path "./Godeps/*" -print | xargs -n1 gofmt -l)
if [ "x${unformatted}" == "x" ] ; then
  update_status --state success
else

  V="Unformatted files found.
  Please run 'go fmt' on each of these files and amend your commit to continue."

  for f in ${unformatted}; do
    V=$(printf "%s\n - %s" "${V}" "${f}")
  done

  # Print to stdout
  printf "%s\n\n" "${V}"
  update_status --state failure --description "${V}"

  # Post a comment with the unformatted list
  if [ "${TRAVIS_PULL_REQUEST}" != "false" ] ; then
    run_and_comment printf "%s\n\n" "${V}"
  fi

  [ "${TRAVIS}" == "true" ] || exit 1 # Stop here if running locally
fi
end_context #test/gofmt

#
# Unit Tests. These do not receive a context or status updates,
# as they are reflected in our eventual exit code.
#

# Ensure SQLite is installed so we don't recompile it each time
go install ./Godeps/_workspace/src/github.com/mattn/go-sqlite3

if [ "${TRAVIS}" == "true" ] ; then
  # Run each test by itself for Travis, so we can get coverage
  for dir in ${TESTDIRS}; do
    run go test -tags pkcs11 -covermode=count -coverprofile=${dir}.coverprofile ./${dir}/
  done

  # Gather all the coverprofiles
  [ -e $GOBIN/gover ] && run $GOBIN/gover

  # We don't use the run function here because sometimes goveralls fails to
  # contact the server and exits with non-zero status, but we don't want to
  # treat that as a failure.
  [ -e $GOBIN/goveralls ] && $GOBIN/goveralls -coverprofile=gover.coverprofile -service=travis-ci
else
  # Run all the tests together if local, for speed
  dirlist=""

  for dir in ${TESTDIRS}; do
    dirlist="${dirlist} ./${dir}/"
  done

  run go test -tags pkcs11 ${dirlist}
fi

# If the unittests failed, exit before trying to run the integration test.
if [ ${FAILURE} != 0 ]; then
  echo "--------------------------------------------------"
  echo "---          A unit test failed.               ---"
  echo "--- Stopping before running integration tests. ---"
  echo "--------------------------------------------------"
  exit ${FAILURE}
fi

#
# Integration tests
#

# Set context to integration, and force a pending state
start_context "test/integration"
update_status --state pending --description "Integration Tests in progress"

if [ -z "$LETSENCRYPT_PATH" ]; then
  LETSENCRYPT_PATH=$(mktemp -d -t leXXXX)

  echo "------------------------------------------------"
  echo "--- Checking out letsencrypt client is slow. ---"
  echo "--- Recommend setting \$LETSENCRYPT_PATH to  ---"
  echo "--- client repo with initialized virtualenv  ---"
  echo "------------------------------------------------"
  run git clone \
    https://www.github.com/letsencrypt/lets-encrypt-preview.git \
    $LETSENCRYPT_PATH || exit 1

  cd $LETSENCRYPT_PATH
  run virtualenv --no-site-packages -p python2 ./venv && \
    ./venv/bin/pip install -r requirements.txt -e . || exit 1
  cd -
fi

source $LETSENCRYPT_PATH/venv/bin/activate
export LETSENCRYPT_PATH

python test/amqp-integration-test.py
case $? in
  0) # Success
    update_status --state success
    ;;
  1) # Python client failed, but Node client didn't, which does
     # not constitute failure
    update_status --state success --description "Python integration failed."
    ;;
  2) # Node client failed
    update_status --state failure --description "NodeJS integration failed."
    FAILURE=1
    ;;
  *) # Error occurred
    update_status --state error --description "Unknown error occurred."
    FAILURE=1
    ;;
esac
end_context #test/integration

exit ${FAILURE}
