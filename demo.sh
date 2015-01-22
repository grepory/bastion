#!/bin/sh

export GOPATH=`pwd`
go get ./...
go run cmd/bastion/main.go --access_key_id=a --secret_key=s --region=us-west-1 --opsee=54.67.86.46:5555 --hostname="cliff" --data=cookbooks/bastion/files/default/demo_data.json