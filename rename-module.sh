#!/usr/bin/env bash

rules='
id: replace-module
language: go
rule:
  kind: import_spec
  pattern: $OLD_IMPORT
constraints:
  OLD_IMPORT:
    has:
      field: path
      regex: ^"tailscale.com
transform:
  NEW_IMPORT:
    replace:
      source: $OLD_IMPORT
      replace: tailscale.com(?<PATH>.*)
      by: github.com/sagernet/tailscale$PATH
fix: $NEW_IMPORT
'

sg scan --inline-rules "$rules" -U

sed -i 's|module tailscale.com|module github.com/sagernet/tailscale|' go.mod

go mod tidy

gci write .

git commit -m "Rename module" -a
