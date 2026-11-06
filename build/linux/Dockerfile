FROM node:22-bookworm AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.26-bookworm AS package
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev dpkg-dev \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=frontend /src/frontend/dist ./frontend/dist
RUN CGO_ENABLED=1 go build -tags webkit2_41 -trimpath -ldflags="-s -w" -o /out/downwell .
COPY build/linux/downwell.desktop /tmp/downwell.desktop
COPY build/linux/downwell.svg /tmp/downwell.svg
RUN mkdir -p /pkg/DEBIAN /pkg/usr/bin /pkg/usr/share/applications /pkg/usr/share/icons/hicolor/scalable/apps \
    && install -m 0755 /out/downwell /pkg/usr/bin/downwell \
    && install -m 0644 /tmp/downwell.desktop /pkg/usr/share/applications/downwell.desktop \
    && install -m 0644 /tmp/downwell.svg /pkg/usr/share/icons/hicolor/scalable/apps/downwell.svg \
    && printf 'Package: downwell\nVersion: 0.1.0\nSection: net\nPriority: optional\nArchitecture: amd64\nDepends: libgtk-3-0, libwebkit2gtk-4.1-0\nMaintainer: Download Manager\nDescription: Cross-platform HTTP download manager\n Desktop client with segmented HTTP downloads and persistent queues.\n' > /pkg/DEBIAN/control \
    && dpkg-deb --root-owner-group --build /pkg /out/downwell_0.1.0_amd64.deb \
    && dpkg -i /out/downwell_0.1.0_amd64.deb

