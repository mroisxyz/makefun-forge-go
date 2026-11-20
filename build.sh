#!/bin/sh
# Cross-compile the forge as a static Linux/amd64 binary committed as source.
set -eu
export GOOS=linux GOARCH=amd64 CGO_ENABLED=0
export GOTOOLCHAIN=local
go build -trimpath -ldflags="-s -w" -o forge-linux-amd64 .
chmod +x forge-linux-amd64
ls -lh forge-linux-amd64
