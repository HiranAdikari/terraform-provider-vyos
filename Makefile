BINARY   = terraform-provider-vyos
INSTALL  = $(HOME)/go/bin/$(BINARY)

.PHONY: build install fmt vet

build:
	go build -o $(BINARY) .

install:
	go install .

fmt:
	go fmt ./...

vet:
	go vet ./...
