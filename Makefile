# Define pkgs, run, and cover vairables for test so that we can override them in
# the terminal more easily.
pkgs := $(shell go list ./...)
run := .
count := 1

IGNITE_VERSION ?= v29.3.1
IGNITE_EVOLVE_APP_VERSION ?= main
EVNODE_VERSION ?= v1.0.0-beta.9

## help: Show this help message
help: Makefile
	@echo " Choose a command run in "$(PROJECTNAME)":"
	@sed -n 's/^##//p' $< | column -t -s ':' |  sed -e 's/^/ /'
.PHONY: help

## clean: clean testcache
clean:
	@echo "--> Clearing testcache"
	@go clean --testcache
.PHONY: clean

## cover: generate to code coverage report.
cover:
	@echo "--> Generating Code Coverage"
	@go install github.com/ory/go-acc@latest
	@go-acc -o coverage.txt $(pkgs)
.PHONY: cover

## deps: Install dependencies
deps:
	@echo "--> Installing dependencies"
	@go mod download
	@go mod tidy
.PHONY: deps

## lint: Run linters golangci-lint and markdownlint.
lint: vet
	@echo "--> Running golangci-lint"
	@golangci-lint run --fix

lint-fix: lint

.PHONY: lint lint-fix

## fmt: Run fixes for linters. Currently only markdownlint.
fmt:
	@echo "--> Formatting markdownlint"
	@markdownlint --config .markdownlint.yaml '**/*.md' -f
.PHONY: fmt

## gci: Format Go imports
gci:
	@echo "--> Formatting Go imports"
	@gci write --section standard --section default --section "prefix(github.com/evstack/ev-abci)" --section "prefix(github.com/evstack)" .
.PHONY: gci

## vet: Run go vet
vet:
	@echo "--> Running go vet"
	@go vet $(pkgs)
.PHONY: vet

## test: Running unit tests
test: vet
	@echo "--> Running unit tests"
	@go test -v -race -covermode=atomic -coverprofile=coverage.txt $(pkgs) -run $(run) -count=$(count)
.PHONY: test

## proto-gen: Generate protobuf files using buf
proto-gen:
	@echo "--> Generating protobuf files for modules"
	@cd modules/proto && \
		go tool github.com/bufbuild/buf/cmd/buf dep update
	@cd modules/proto && \
		go tool github.com/bufbuild/buf/cmd/buf generate
	@mv modules/github.com/evstack/ev-abci/modules/migrationmngr/types/** modules/migrationmngr/types/ && \
		mv modules/github.com/evstack/ev-abci/modules/migrationmngr/module/* modules/migrationmngr/module/ && \
		mv modules/github.com/evstack/ev-abci/modules/network/types/** modules/network/types/ && \
		mv modules/github.com/evstack/ev-abci/modules/network/module/v1/* modules/network/module/v1
	@rm -r modules/github.com

.PHONY: proto-gen


## build-attester-docker-image: Build Docker images for the GM chain and attester integration tests
build-attester-docker-image:
	@echo "--> Building GM integration Docker image"
	@docker build \
		-f tests/integration/docker/Dockerfile.gm \
		--build-arg IGNITE_VERSION=$(IGNITE_VERSION) \
		--build-arg IGNITE_EVOLVE_APP_VERSION=$(IGNITE_EVOLVE_APP_VERSION) \
		--build-arg EVNODE_VERSION=$(EVNODE_VERSION) \
		-t evabci/gm:local \
		.

.PHONY: build-attester-docker-image
