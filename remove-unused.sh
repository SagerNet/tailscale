#!/usr/bin/env bash

set -e -o pipefail

function remove_unused() {
  git rm -rf --ignore-unmatch \
    .github \
    **/*_test.go \
    tstest/ \
    ipn/lapitest \
    feature/capture \
    feature/condregister/maybe_capture.go \
    feature/taildrop \
    feature/condregister/maybe_taildrop.go \
    feature/relayserver \
    feature/condregister/maybe_relayserver.go \
    feature/tap \
    feature/condregister/maybe_tap.go \
    feature/tpm \
    feature/condregister/maybe_tpm.go \
    feature/wakeonlan \
    feature/condregister/maybe_wakeonlan.go \
    release/ \
    cmd/ \
    util/winutil/s4u/ \
    k8s-operator/ \
    ssh/ \
    wf/ \
    internal/tooldeps \
    gokrazy/
}

remove_unused
remove_unused

go mod tidy
git commit -a -m "Remove unused"
