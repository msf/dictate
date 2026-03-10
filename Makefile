BIN_DIR   := bin
MODEL_DIR := models
MODEL_URL := https://huggingface.co/ggerganov/whisper.cpp/resolve/main

.PHONY: all whisper build models lint fmt vet run clean

all: whisper build models

# --- Build (Docker, hermetic) ---

whisper: $(BIN_DIR)/whisper-stream $(BIN_DIR)/dictate

$(BIN_DIR)/whisper-stream $(BIN_DIR)/dictate: Dockerfile go.mod $(wildcard cmd/**/*.go internal/**/*.go)
	DOCKER_BUILDKIT=1 docker build --output type=local,dest=$(BIN_DIR)/ .
	@echo "built: $(BIN_DIR)/whisper-stream $(BIN_DIR)/dictate"

# --- Build (host Go, fast iteration) ---

build:
	go build -trimpath -o $(BIN_DIR)/dictate ./cmd/dictate

# --- Models (multilingual for pt+en) ---

models: $(MODEL_DIR)/ggml-tiny.bin $(MODEL_DIR)/ggml-base.bin $(MODEL_DIR)/ggml-small.bin

$(MODEL_DIR)/ggml-tiny.bin:
	@mkdir -p $(MODEL_DIR)
	curl -L -sS -o $@ $(MODEL_URL)/ggml-tiny.bin

$(MODEL_DIR)/ggml-base.bin:
	@mkdir -p $(MODEL_DIR)
	curl -L -sS -o $@ $(MODEL_URL)/ggml-base.bin

$(MODEL_DIR)/ggml-small.bin:
	@mkdir -p $(MODEL_DIR)
	curl -L -sS -o $@ $(MODEL_URL)/ggml-small.bin

# --- Lint ---

lint: fmt vet

fmt:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needs fixing:" && gofmt -d . && exit 1)

vet:
	go vet ./...

# --- Run ---

run:
	$(BIN_DIR)/dictate --model $(MODEL_DIR)/ggml-base.bin

# --- Clean ---

clean:
	rm -rf $(BIN_DIR) $(MODEL_DIR)
