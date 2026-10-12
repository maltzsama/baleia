FROM node:22-alpine AS web
WORKDIR /web

RUN apk add --no-cache python3 make g++

COPY web/package.json web/package-lock.json* ./

RUN npm ci

COPY web/ ./
RUN npm run build

FROM golang:latest AS go
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 go build -o /api ./cmd/api
RUN CGO_ENABLED=0 go build -o /worker ./cmd/worker
RUN CGO_ENABLED=0 go build -o /migrate ./cmd/migrate

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /
COPY --from=go /api /api
COPY --from=go /worker /worker
COPY --from=go /migrate /migrate
COPY --from=web /web/build /web/build
COPY api/ /api-spec/
COPY db/migrations/ /db/migrations/
