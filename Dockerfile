# Stage 1: Build whisper.cpp (whisper-stream binary)
FROM ubuntu:24.04 AS whisper-build

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential cmake git ca-certificates \
    libsdl2-dev libvulkan-dev glslang-tools glslc \
    && rm -rf /var/lib/apt/lists/*

ARG WHISPER_VERSION=master
ARG GGML_NATIVE=ON
ARG WHISPER_CMAKE_FLAGS=""
RUN git clone --depth 1 --branch ${WHISPER_VERSION} \
    https://github.com/ggerganov/whisper.cpp /src/whisper.cpp

WORKDIR /src/whisper.cpp

# CPU always. Vulkan via GGML_VULKAN (not the old WHISPER_VULKAN).
RUN cmake -B build \
    -DCMAKE_BUILD_TYPE=Release \
    -DWHISPER_SDL2=ON \
    -DGGML_VULKAN=ON \
    -DGGML_NATIVE=${GGML_NATIVE} \
    -DBUILD_SHARED_LIBS=OFF \
    ${WHISPER_CMAKE_FLAGS} \
    && cmake --build build --config Release -j$(nproc)

# Verify the binary exists
RUN test -f build/bin/whisper-stream && echo "whisper-stream built OK"

# Stage 2: Build Go binary
FROM golang:1.26 AS go-build

WORKDIR /src/dictate
COPY go.mod ./
RUN go mod download 2>/dev/null || true
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/dictate ./cmd/dictate

# Stage 3: Export binaries
FROM scratch AS export
COPY --from=whisper-build /src/whisper.cpp/build/bin/whisper-stream /
COPY --from=go-build /out/dictate /
