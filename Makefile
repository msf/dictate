BIN_DIR   := bin
ALT_BUILD_DIR := .build
MODEL_DIR := models
MODEL_URL := https://huggingface.co/ggerganov/whisper.cpp/resolve/main
DOCKER_BUILD := DOCKER_BUILDKIT=1 docker build

.PHONY: all whisper whisper-generic whisper-native build bench integ-test record models models-recommended model-gpu model-cpu-light lint fmt vet run clean

all: whisper build models

# --- Build (Docker, hermetic) ---

whisper: $(BIN_DIR)/whisper-stream $(BIN_DIR)/dictate

$(BIN_DIR)/whisper-stream $(BIN_DIR)/dictate: Dockerfile go.mod $(wildcard cmd/**/*.go audio/**/*.go output/**/*.go whisper/**/*.go)
	$(DOCKER_BUILD) --build-arg GGML_NATIVE=ON --output type=local,dest=$(BIN_DIR)/ .
	@echo "built: $(BIN_DIR)/whisper-stream $(BIN_DIR)/dictate"

whisper-native:
	$(DOCKER_BUILD) --build-arg GGML_NATIVE=ON --output type=local,dest=$(ALT_BUILD_DIR)/native/ .
	@echo "built: $(ALT_BUILD_DIR)/native/whisper-stream $(ALT_BUILD_DIR)/native/dictate"

whisper-generic:
	$(DOCKER_BUILD) --build-arg GGML_NATIVE=OFF --output type=local,dest=$(ALT_BUILD_DIR)/generic/ .
	@echo "built: $(ALT_BUILD_DIR)/generic/whisper-stream $(ALT_BUILD_DIR)/generic/dictate"

# --- Build (host Go, fast iteration) ---

build:
	go build -trimpath -o $(BIN_DIR)/dictate ./cmd/dictate

bench: build
	go build -trimpath -o $(BIN_DIR)/bench ./cmd/bench

integ-test: whisper bench
	go test -count=1 -tags=integration -v ./integ

# --- Models (multilingual for pt+en) ---

models: $(MODEL_DIR)/ggml-tiny.bin $(MODEL_DIR)/ggml-base.bin $(MODEL_DIR)/ggml-small.bin

models-recommended: $(MODEL_DIR)/ggml-large-v3-turbo-q5_0.bin $(MODEL_DIR)/ggml-medium-q5_0.bin

model-gpu: $(MODEL_DIR)/ggml-large-v3-turbo-q5_0.bin

model-cpu-light: $(MODEL_DIR)/ggml-medium-q5_0.bin

$(MODEL_DIR)/ggml-tiny.bin:
	@mkdir -p $(MODEL_DIR)
	curl -L -sS -o $@ $(MODEL_URL)/ggml-tiny.bin

$(MODEL_DIR)/ggml-base.bin:
	@mkdir -p $(MODEL_DIR)
	curl -L -sS -o $@ $(MODEL_URL)/ggml-base.bin

$(MODEL_DIR)/ggml-small.bin:
	@mkdir -p $(MODEL_DIR)
	curl -L -sS -o $@ $(MODEL_URL)/ggml-small.bin

$(MODEL_DIR)/ggml-large-v3-turbo-q5_0.bin:
	@mkdir -p $(MODEL_DIR)
	curl -L -sS -o $@ $(MODEL_URL)/ggml-large-v3-turbo-q5_0.bin

$(MODEL_DIR)/ggml-medium-q5_0.bin:
	@mkdir -p $(MODEL_DIR)
	curl -L -sS -o $@ $(MODEL_URL)/ggml-medium-q5_0.bin

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
	rm -rf $(BIN_DIR) $(ALT_BUILD_DIR) $(MODEL_DIR)
