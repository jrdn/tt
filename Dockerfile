FROM --platform=$BUILDPLATFORM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /tt ./cmd/tt

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /tt /tt
COPY skills /skills
EXPOSE 8080
ENTRYPOINT ["/tt"]
CMD ["server"]
