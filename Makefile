ROOT_DIR := $(shell dirname $(realpath $(lastword $(MAKEFILE_LIST))))
SHELL := /bin/sh

SOURCES = $(shell find $(ROOT_DIR) -name "*.go" -print | grep -v /thrift_0_9_2/ )
SOURCE_DIRS = $(shell find $(ROOT_DIR) -d -print | grep -v /thrift_0_9_2/ | grep -v . )
TESTS   = $(shell go list ./... | grep -v e2e | grep -v "thrift_0_9_2")
COVERAGE_DIR ?= $(PWD)/coverage

export GO111MODULE = on

DEF_BUILDER = ./ocb
BUILDER ?= $(DEF_BUILDER)
VERSION ?= 0.113.0

default: all

all: clean check build

clean:
	rm -rf otel
	rm -rf build
	rm -rf ocb


check: test lint checkfmt coverage

test:
	go test -race -v -failfast $(TESTS)

checkfmt:
	@[ -z $$(gofmt -l $(SOURCES)) ] || (echo "Sources not formatted correctly. Fix by running: make fmt" && false)

fmt: $(SOURCES)
	gofmt -s -w $(SOURCES)

lint:
	golint -set_exit_status $(SOURCE_DIRS)
	golangci-lint run

coverage:
	mkdir -p $(COVERAGE_DIR)
	go test -v $(TESTS) -coverpkg=./... -coverprofile=$(COVERAGE_DIR)/coverage.out
	go test -v $(TESTS) -coverpkg=./... -covermode=count -coverprofile=$(COVERAGE_DIR)/count.out fmt
	go tool cover -func=$(COVERAGE_DIR)/coverage.out
	go tool cover -func=$(COVERAGE_DIR)/count.out
	go tool cover -html=$(COVERAGE_DIR)/coverage.out -o $(COVERAGE_DIR)/index.html

build: $(SOURCES)
	CGO_ENABLED=0 go build $(BUILD_FLAGS) -ldflags "$(LDFLAGS)" $(CMD)

godoc:
	go install github.com/princjef/gomarkdoc/cmd/gomarkdoc@latest
	gomarkdoc --output ./docs/receiver.md ./

ocb: # darwin-amd64
	if [ "$(BUILDER)" = "$(DEF_BUILDER)" ]; then \
		[ -x $(BUILDER) ] && exit 0 ; \
		sys=$$( uname -s | tr 'A-Z' 'a-z' ); \
		mach=$$( uname -m | tr 'A-Z' 'a-z' ); \
		[ $$mach = "x86_64" ] && mach="amd64"; \
		curl -o ocb -L "https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/cmd/builder/v$(VERSION)/ocb_$(VERSION)_$${sys}_$${mach}" ;\
		chmod 777 ./ocb; \
	fi

build.local:  ocb
	mkdir -p otel
	CGO_ENABLED=0 $(BUILDER) --config config/build_config.yaml

run: build.local
	./otel/local/zalando-tracing-otel-collector --config config/run_config.yaml

debug: build.local
	dlv --listen=:2345 --api-version=2 --headless=true --accept-multiclient --log exec ./otel/local/zalando-tracing-otel-collector -- --config config/run_config.yaml