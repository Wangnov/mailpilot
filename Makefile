BINARY := mailpilot
DIST := dist

.PHONY: build test vet cross clean

build:
	go build -o $(BINARY) .

test:
	go test ./...

vet:
	go vet ./...

# 交叉编译到常见平台（单二进制，目标机零依赖）
cross:
	@mkdir -p $(DIST)
	GOOS=linux  GOARCH=amd64 go build -o $(DIST)/$(BINARY)-linux-amd64  .
	GOOS=linux  GOARCH=arm64 go build -o $(DIST)/$(BINARY)-linux-arm64  .
	GOOS=darwin GOARCH=arm64 go build -o $(DIST)/$(BINARY)-darwin-arm64 .
	GOOS=darwin GOARCH=amd64 go build -o $(DIST)/$(BINARY)-darwin-amd64 .

clean:
	rm -rf $(BINARY) $(DIST)
