FROM node:22-alpine AS web
WORKDIR /web

RUN apk add --no-cache python3 make g++

COPY web/package.json web/package-lock.json* ./

RUN npm ci

COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS go
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-X github.com/galileostudio/baleia/internal/version.Version=${VERSION}" -o /api ./cmd/api
RUN CGO_ENABLED=0 go build -ldflags "-X github.com/galileostudio/baleia/internal/version.Version=${VERSION}" -o /worker ./cmd/worker
RUN CGO_ENABLED=0 go build -o /migrate ./cmd/migrate

FROM alpine:3.20
RUN apk --no-cache add ca-certificates && adduser -D -u 10001 baleia
WORKDIR /
COPY --from=go /api /api
COPY --from=go /worker /worker
COPY --from=go /migrate /migrate
COPY --from=web /web/build /web/build
COPY api/ /api-spec/
COPY db/migrations/ /db/migrations/
# Apache 2.0, section 4(a): recipients of the image get a copy of the license.
COPY LICENSE /usr/share/licenses/baleia/LICENSE
USER 10001
