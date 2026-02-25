##@ General

all: help

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## 📚 Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\n🎯 \033[1mUsage:\033[0m\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Build

.PHONY: build
build: ## 🔨 Build the application
	@echo "🔨 Building Go application..."
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -installsuffix cgo -ldflags="-w -s -extldflags=-static" -o dist/tp-gui .
	@echo "✅ Build completed successfully!"

.PHONY: run
run: ## 🚀 Run the application
	@echo "🚀 Starting application..."
	@go run .

##@ Development

.PHONY: test
test: ## 🧪 Run tests
	@echo "🧪 Running tests..."
	@go test ./...
	@echo "✅ Tests completed!"