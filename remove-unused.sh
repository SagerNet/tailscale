#!/usr/bin/env bash

set -e -o pipefail

function remove_unused() {
  git rm -rf --ignore-unmatch \
    .github \
    **/*_test.go \
    tstest/ \
    release/ \
    cmd/ \
    util/winutil/s4u/ \
    k8s-operator/ \
    ssh/
}

remove_unused
remove_unused

go mod tidy
git commit -a -m "Remove unused"
