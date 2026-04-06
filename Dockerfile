FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /mrdn ./cmd/mrdn

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /mrdn /usr/local/bin/mrdn
ENTRYPOINT ["mrdn"]
