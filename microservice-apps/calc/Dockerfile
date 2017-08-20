FROM golang:1.8

RUN go get github.com/goji/httpauth && go get github.com/gorilla/mux && go get github.com/marcmak/calc/calc

COPY src /go/src/github.com/awslabs/samples/calc

RUN go install github.com/awslabs/samples/calc

EXPOSE 8081

CMD ["/go/bin/calc"]
