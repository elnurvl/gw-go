FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /gateway .

FROM alpine:3.21

RUN addgroup -S app && adduser -S -G app app
WORKDIR /app
ENV TZ=Asia/Baku
COPY --from=build /gateway .
COPY config.yaml .
USER app

EXPOSE 9000

ENTRYPOINT ["./gateway"]
