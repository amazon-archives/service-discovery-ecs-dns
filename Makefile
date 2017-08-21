.PHONY: all vet install

PKGS=$(shell go list ./... | grep -Fv vendor)

all:
	docker run -e  \
		GOPATH=/app/ \
		--rm  \
		-v "$(PWD):/target" \
		-v "$(PWD):/app/src/github.com/bankrs/service-discovery-ecs-dns"  \
		-w /target  \
		golang:1.8 \
		go build \
		-o ecssd_agent \
 		-ldflags "-linkmode external -extldflags -static" \
		github.com/bankrs/service-discovery-ecs-dns
	 docker build -t bankrs/ecssd_agent:latest .

install:
	@go install $(PKGS)

vet:
	@go vet $(PKGS)
