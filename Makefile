.PHONY: fmt vet lint check fix hooks

fmt:
	go fmt ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

check: fmt vet lint

fix: fmt
	golangci-lint run --fix ./...

hooks:
	brew install lefthook && lefthook install
