.PHONY: all vet install

PKGS=$(shell go list ./... | grep -Fv vendor)

all:
	docker run -e GOPATH=/app/ \
		-e CGO_ENABLED=0 \
		--rm  \
		-v "$(PWD):/target" \
		-v "$(PWD):/app/src/github.com/bankrs/service-discovery-ecs-dns" \
		-w /target  \
		golang:1.9.0 \
		go build \
		-o ecssd_agent \
		github.com/bankrs/service-discovery-ecs-dns
	 docker build -t bankrs/ecssd_agent:latest .

install:
	@go install $(PKGS)

vet:
	@go vet $(PKGS)
