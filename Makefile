BIN_DIR   := bin
MODEL_DIR := models
MODEL_URL := https://huggingface.co/ggerganov/whisper.cpp/resolve/main
DOCKER_BUILD := DOCKER_BUILDKIT=1 docker build

.PHONY: all whisper whisper-generic whisper-native build bench record models lint fmt vet run clean

all: whisper build models

# --- Build (Docker, hermetic) ---

whisper: $(BIN_DIR)/whisper-stream $(BIN_DIR)/dictate

$(BIN_DIR)/whisper-stream $(BIN_DIR)/dictate: Dockerfile go.mod $(wildcard cmd/**/*.go internal/**/*.go)
	$(DOCKER_BUILD) --build-arg GGML_NATIVE=ON --output type=local,dest=$(BIN_DIR)/ .
	@echo "built: $(BIN_DIR)/whisper-stream $(BIN_DIR)/dictate"

whisper-native:
	$(DOCKER_BUILD) --build-arg GGML_NATIVE=ON --output type=local,dest=bin-native/ .
	@echo "built: bin-native/whisper-stream bin-native/dictate"

whisper-generic:
	$(DOCKER_BUILD) --build-arg GGML_NATIVE=OFF --output type=local,dest=bin-generic/ .
	@echo "built: bin-generic/whisper-stream bin-generic/dictate"

# --- Build (host Go, fast iteration) ---

build:
	go build -trimpath -o $(BIN_DIR)/dictate ./cmd/dictate

bench: build
	go build -trimpath -o $(BIN_DIR)/bench ./cmd/bench

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

# --- Benchmark corpus ---

CORPUS_DIR := bench/corpus

record:
	@mkdir -p $(CORPUS_DIR)
	@echo "Recording to $(CORPUS_DIR)/design_en.wav — speak naturally, Ctrl+C to stop"
	pw-record --target auto $(CORPUS_DIR)/design_en.wav
	@echo "Done. Now write the reference transcript:"
	@echo "  $$EDITOR $(CORPUS_DIR)/design_en.txt"

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
