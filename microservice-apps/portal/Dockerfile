FROM golang:1.8

RUN go get github.com/gorilla/mux

COPY src /go/src/github.com/awslabs/samples/portal

COPY public /var/www/html/

RUN go install github.com/awslabs/samples/portal

ENV HTML_FILE_DIR /var/www/html

EXPOSE 8080

CMD ["/go/bin/portal"]
