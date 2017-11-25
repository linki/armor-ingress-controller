FROM golang:1.9-alpine

COPY . /go/src/github.com/linki/armor-ingress-controller
RUN go install -v github.com/linki/armor-ingress-controller

ENTRYPOINT ["/go/bin/armor-ingress-controller"]
