all:
	docker run -e  \
		GOPATH=/app/ \
		--rm  \
		-v "$(PWD):/target" \
		-v "$(PWD):/app/src/github.com/awslabs/service-discovery-ecs-dns"  \
		-w /target  \
		golang:1.8 \
		go build \
		-o ecssd_agent \
 		-ldflags "-linkmode external -extldflags -static" \
		github.com/awslabs/service-discovery-ecs-dns
	 docker build -t awslabs/ecssd_agent:latest .
