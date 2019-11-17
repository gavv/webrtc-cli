GO111MODULE := on
export GO111MODULE

all: build

build:
	go build -mod=vendor

install:
	go install -mod=vendor

check:
	go build -mod=vendor -o /dev/null
	golangci-lint run .

fmt:
	gofmt -s -w .
