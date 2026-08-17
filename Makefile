BINARY := cf-dnspod
PACKAGE := ./cmd/cf-dnspod
DIST := dist
LDFLAGS := -s -w

.PHONY: test race vet build clean smoke

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

build:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-amd64 $(PACKAGE)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-arm64 $(PACKAGE)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-arm64 $(PACKAGE)

smoke: build
	$(DIST)/$(BINARY)-darwin-arm64 --help
	file $(DIST)/$(BINARY)-linux-amd64 $(DIST)/$(BINARY)-linux-arm64 $(DIST)/$(BINARY)-darwin-arm64

clean:
	rm -f $(DIST)/$(BINARY)-linux-amd64 $(DIST)/$(BINARY)-linux-arm64 $(DIST)/$(BINARY)-darwin-arm64

