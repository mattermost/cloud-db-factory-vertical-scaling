################################################################################
##                             VERSION PARAMS                                 ##
################################################################################

## Docker Build Versions
DOCKER_BUILD_IMAGE = golang:1.17.0
DOCKER_BASE_IMAGE = alpine:3.14

################################################################################

GO ?= $(shell command -v go 2> /dev/null)
CLOUD_DB_FACTORY_VERTICAL_SCALING_IMAGE ?= mattermost/cloud-db-factory-vertical-scaling:test
MACHINE = $(shell uname -m)
GOFLAGS ?= $(GOFLAGS:)
BUILD_TIME := $(shell date -u +%Y%m%d.%H%M%S)
BUILD_HASH := $(shell git rev-parse HEAD)

################################################################################

LOGRUS_URL := github.com/sirupsen/logrus

LOGRUS_VERSION := $(shell find go.mod -type f -exec cat {} + | grep ${LOGRUS_URL} | awk '{print $$NF}')

LOGRUS_PATH := $(GOPATH)/pkg/mod/${LOGRUS_URL}\@${LOGRUS_VERSION}

export GO111MODULE=on

all: check-style dist

## Runs govet and gofmt against all packages.
.PHONY: check-style
check-style: govet lint
	@echo Checking for style guide compliance

## Runs lint against all packages.
.PHONY: lint
lint:
	@echo Running lint
	env GO111MODULE=off $(GO) get -u golang.org/x/lint/golint
	golint -set_exit_status ./...
	@echo lint success

## Runs govet against all packages.
.PHONY: vet
govet:
	@echo Running govet
	$(GO) vet ./...
	@echo Govet success

## Builds and thats all :)
.PHONY: dist
dist:	build

.PHONY: binaries
binaries: ## Build binaries of cloud-db-factory-vertical-scaling
	@echo Building binaries of Cloud-DB-Factory-Vertical-Scaling
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -gcflags all=-trimpath=$(PWD) -asmflags all=-trimpath=$(PWD) -a -installsuffix cgo -o build/_output/bin/cloud-db-factory-vertical-scaling-linux-amd64  ./
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GO) build -gcflags all=-trimpath=$(PWD) -asmflags all=-trimpath=$(PWD) -a -installsuffix cgo -o build/_output/bin/cloud-db-factory-vertical-scaling-darwin-amd64  ./
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -gcflags all=-trimpath=$(PWD) -asmflags all=-trimpath=$(PWD) -a -installsuffix cgo -o build/_output/bin/cloud-db-factory-vertical-scaling-linux-arm64  ./
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build -gcflags all=-trimpath=$(PWD) -asmflags all=-trimpath=$(PWD) -a -installsuffix cgo -o build/_output/bin/cloud-db-factory-vertical-scaling-darwin-arm64  ./

.PHONY: build
build:
	@echo Building Cloud-DB-Factory-Vertical-Scaling
	GOOS=linux CGO_ENABLED=0 $(GO) build -gcflags all=-trimpath=$(PWD) -asmflags all=-trimpath=$(PWD) -a -installsuffix cgo -o build/_output/bin/main  ./

.PHONY: build-image
build-image:  ## Build the docker image for cloud-db-factory-vertical-scaling
	@echo Building Cloud-DB-Factory-Vertical-Scaling Docker Image
	echo $DOCKER_PASSWORD | docker login --username $DOCKER_USERNAME --password-stdin
	docker buildx build \
	--platform linux/arm64,linux/amd64 \
	--build-arg DOCKER_BUILD_IMAGE=$(DOCKER_BUILD_IMAGE) \
	--build-arg DOCKER_BASE_IMAGE=$(DOCKER_BASE_IMAGE) \
	. -f build/Dockerfile -t $(CLOUD_DB_FACTORY_VERTICAL_SCALING_IMAGE) \
	--no-cache \
	--push

.PHONY: push-image-pr
push-image-pr:
	set -e
	set -u
	export TAG="${CIRCLE_SHA1:0:7}"
	echo $DOCKER_PASSWORD | docker login --username $DOCKER_USERNAME --password-stdin
	docker tag mattermost/cloud-db-factory-vertical-scaling:test mattermost/cloud-db-factory-vertical-scaling:$TAG
	docker push mattermost/cloud-db-factory-vertical-scaling:$TAG

.PHONY: push-image
push-image:
	set -e
	set -u
	if [[ -z "${CIRCLE_TAG:-}" ]]; then
	  echo "Pushing lastest for $CIRCLE_BRANCH..."
	  TAG=latest
	else
	  echo "Pushing release $CIRCLE_TAG..."
	  TAG="$CIRCLE_TAG"
	fi
	echo $DOCKER_PASSWORD | docker login --username $DOCKER_USERNAME --password-stdin
	docker tag mattermost/cloud-db-factory-vertical-scaling:test mattermost/cloud-db-factory-vertical-scaling:$TAG
	docker push mattermost/cloud-db-factory-vertical-scaling:$TAG

.PHONY: install
install: build
	go install ./...

.PHONY: release
release:
	@echo Cut a release
	sh ./scripts/release.sh

.PHONY: deps
deps:
	sudo apt update && sudo apt install hub git
	go get k8s.io/release/cmd/release-notes
