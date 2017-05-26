# Build Flags
BUILD_NUMBER ?= $(BUILD_NUMBER:)
BUILD_DATE = $(shell date -u)
BUILD_HASH = $(shell git rev-parse HEAD)

# Golang Flags
GOPATH ?= $(GOPATH:):./vendor
GOFLAGS ?= $(GOFLAGS:)
GO=go
GO_LINKER_FLAGS ?= -ldflags \
				   "-X github.com/primefour/http_server/model.BuildNumber=$(BUILD_NUMBER)\
				    -X 'github.com/primefour/http_server/model.BuildDate=$(BUILD_DATE)'\
				    -X github.com/primefour/http_server/model.BuildHash=$(BUILD_HASH)"

.prebuild:
	@echo Preparation for running go code
	go get $(GOFLAGS) github.com/Masterminds/glide
	touch $@

build-linux: .prebuild
	@echo Build Linux amd64
	env GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) $(GO_LINKER_FLAGS) ./cmd/platform


