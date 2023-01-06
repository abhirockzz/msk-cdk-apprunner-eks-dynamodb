FROM golang:1.16-buster AS build
RUN go env -w GOPROXY=direct

WORKDIR /app
COPY go.mod ./
COPY go.sum ./

RUN go mod download

COPY main.go ./
RUN go build -o /msk-go-app

FROM gcr.io/distroless/base-debian10
WORKDIR /
COPY --from=build /msk-go-app /msk-go-app
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/msk-go-app"]