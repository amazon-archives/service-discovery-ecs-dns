FROM golang:1.8

RUN go get github.com/goji/httpauth && go get github.com/gorilla/mux

COPY src /go/src/github.com/awslabs/samples/time

RUN go install github.com/awslabs/samples/time

EXPOSE 8081

CMD ["/go/bin/time"]
