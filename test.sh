#!/bin/bash
set -e

echo "==> Vetting..."
go vet ./... 

echo "==> Testing..."
go test -count=1 ./...

echo "==> Done: ./gopher-rdp"
