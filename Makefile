BINARY_NAME := module2anywhere
BUILD_LDFLAGS := -s -w -extldflags=-static
GARBLE_FLAGS := -seed=random -literals -tiny

.PHONY: all build garble run clean vet fmt test help

all: build

# 普通构建（不依赖 garble）
build:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 GOPRIVATE=* go build -trimpath -ldflags="${BUILD_LDFLAGS}" -o "./bin/${BINARY_NAME}" .

# 混淆构建（需先 go install mvdan.cc/garble@latest）
garble:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 GOPRIVATE=* garble ${GARBLE_FLAGS} build -ldflags="${BUILD_LDFLAGS}" -o "./bin/${BINARY_NAME}" .

run:
	@./bin/${BINARY_NAME} $(ARGS)

vet:
	go vet ./...

fmt:
	gofmt -s -w .

test:
	go test ./...

clean:
	rm -rf ./bin

help:
	@echo "Targets:"
	@echo "  make build  - 普通构建"
	@echo "  make garble - 混淆构建（需安装 garble）"
	@echo "  make run    - 运行（ARGS=\"...\"）"
	@echo "  make vet    - 静态检查"
	@echo "  make fmt    - 格式化"
	@echo "  make test   - 测试"
	@echo "  make clean  - 清理"
