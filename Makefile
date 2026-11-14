GOCACHE ?= $(CURDIR)/.gocache
GOMODCACHE ?= $(CURDIR)/.gomodcache

MANIFEST := cloudferry-migrator/umbrel-app.yml
COMPOSE := cloudferry-migrator/docker-compose.yml
EXPORTS := cloudferry-migrator/exports.sh
STANDALONE_COMPOSE := deploy/standalone/docker-compose.yml
DOCKERFILE := Dockerfile

# Single source of truth for the GitHub owner: exports.sh, which umbrelOS
# sources before starting compose. The committed value is the OWNER
# placeholder; `make set-owner OWNER=<login>` rewrites it here and in the
# manifest.
OWNER ?= $(shell sed -n 's/^export OWNER="\(.*\)"$$/\1/p' $(EXPORTS))
IMAGE ?= ghcr.io/$(OWNER)/cloud-ferry
# One tag format everywhere: git tag v0.1.0, image tag 0.1.0, manifest 0.1.0.
TAG ?= 0.1.0
PLATFORMS ?= linux/amd64,linux/arm64
RCLONE_DIGEST := sha256:7cbeec24a44f206067597c0368be4de90ae19a64d5113c7d1938bd737a81e466
TAG_RE := $(subst .,\.,$(TAG))

.PHONY: help fmt vet test build run docker docker-push digest pin-digest set-owner check-version lint lint-images

help:
	@echo "make test         unit tests, no network and no rclone"
	@echo "make build        binary in bin/cloudferry"
	@echo "make run          run locally, rclone is spawned (SPAWN_RCLONE=true)"
	@echo "make docker       build the image for the host platform"
	@echo "make docker-push  build linux/amd64+linux/arm64 and push to ghcr.io"
	@echo "make pin-digest   pin the published manifest-list digest into the compose file"
	@echo "make set-owner    replace the OWNER placeholder (make set-owner OWNER=<login>)"
	@echo "make check-version  verify manifest/compose agree with TAG"
	@echo "make lint         official Umbrel app linter (UMBREL_APPS=<clone>)"
	@echo "make lint-images  same, plus registry image checks (published image required)"

fmt:
	gofmt -w ./cmd ./internal

vet:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go vet ./...

test:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go test ./...

build:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go build -o bin/cloudferry ./cmd/cloudferry

run: build
	DATA_DIR=./.devdata \
	RC_SOCKET=./.devdata/rclone.sock \
	RCLONE_CONFIG=./.devdata/rclone.conf \
	SPAWN_RCLONE=true \
	LISTEN_ADDR=127.0.0.1:3000 \
	DOWNLOADS_DIR=./.devdata/downloads \
	./bin/cloudferry

# Local image for smoke-testing on the build machine (host architecture only:
# a multi-arch build cannot be --load'ed into the local Docker image store).
docker:
	docker buildx build --build-arg VERSION=$(TAG) -t $(IMAGE):$(TAG) --load .

# The published image: multi-arch manifest list, pushed to GHCR. This is what
# cloudferry-migrator/docker-compose.yml pins by digest.
docker-push:
	@test "$(OWNER)" != "OWNER" || { echo "error: run make set-owner OWNER=<github-login> first"; exit 1; }
	docker buildx build --platform $(PLATFORMS) --build-arg VERSION=$(TAG) \
		-t $(IMAGE):$(TAG) --push .

# Print the manifest-list digest for the published tag.
digest:
	docker buildx imagetools inspect $(IMAGE):$(TAG) --format '{{.Manifest.Digest}}'

# Pin the published manifest-list digest into the Umbrel compose file. Reads it
# from the registry, or takes it from DIGEST=sha256:... for CI and offline use.
# The digest must be the multi-arch index digest, not an arch-specific one.
pin-digest:
	@test "$(OWNER)" != "OWNER" || { echo "error: run make set-owner OWNER=<github-login> first"; exit 1; }
	@digest="$(DIGEST)"; \
	if [ -z "$$digest" ]; then \
		digest="$$(docker buildx imagetools inspect $(IMAGE):$(TAG) --format '{{.Manifest.Digest}}')" \
			|| { echo "error: cannot read $(IMAGE):$(TAG) digest; publish the image or pass DIGEST=sha256:..."; exit 1; }; \
	fi; \
	case "$$digest" in sha256:*) ;; *) digest="sha256:$$digest" ;; esac; \
	printf '%s' "$$digest" | grep -Eq '^sha256:[0-9a-f]{64}$$' \
		|| { echo "error: unexpected digest '$$digest' (want the sha256 manifest-list digest)"; exit 1; }; \
	sed -i.bak -E "s#(cloud-ferry:$(TAG_RE)@)sha256:[0-9a-f]{64}#\1$$digest#g" $(COMPOSE); \
	rm -f $(COMPOSE).bak; \
	grep -n "cloud-ferry:$(TAG)@sha256:" $(COMPOSE)

# Replace the OWNER placeholder in the manifest URLs, the image labels and in
# exports.sh, which feeds ${OWNER} into the compose image reference.
set-owner:
	@test -n "$(OWNER)" -a "$(OWNER)" != "OWNER" || { echo "usage: make set-owner OWNER=<github-login>"; exit 1; }
	@printf '%s' "$(OWNER)" | grep -Eq '^[A-Za-z0-9-]+$$' || { echo "error: OWNER must be a GitHub login"; exit 1; }
	sed -i.bak -E \
		-e 's#github\.com/OWNER/#github.com/$(OWNER)/#g' \
		-e 's#githubusercontent\.com/OWNER/#githubusercontent.com/$(OWNER)/#g' \
		$(MANIFEST) $(DOCKERFILE)
	sed -i.bak -E 's#^export OWNER=.*#export OWNER="$(OWNER)"#' $(EXPORTS)
	rm -f $(MANIFEST).bak $(EXPORTS).bak $(DOCKERFILE).bak
	@grep -n "github.com/$(OWNER)/" $(MANIFEST) $(DOCKERFILE)
	@grep -n '^export OWNER=' $(EXPORTS)

# Catch a release where the git tag, manifest version, compose tag or the
# pinned rclone digest disagree.
check-version:
	@grep -Eq "^version: \"?$(TAG)\"?$$" $(MANIFEST) || { echo "error: $(MANIFEST) version is not $(TAG)"; exit 1; }
	@grep -Eq "cloud-ferry:$(TAG)@sha256:[0-9a-f]{64}" $(COMPOSE) || { echo "error: $(COMPOSE) must reference cloud-ferry:$(TAG)@sha256:<digest>"; exit 1; }
	@! grep -Eq "cloud-ferry:$(TAG)@sha256:0{64}" $(COMPOSE) || { echo "error: digest is still the zero placeholder; run make pin-digest"; exit 1; }
	@grep -Fq "rclone/rclone:1.76.0@$(RCLONE_DIGEST)" $(COMPOSE) || { echo "error: $(COMPOSE) rclone image is not pinned"; exit 1; }
	@grep -Fq "rclone/rclone:1.76.0@$(RCLONE_DIGEST)" $(STANDALONE_COMPOSE) || { echo "error: $(STANDALONE_COMPOSE) rclone image is not pinned"; exit 1; }
	@echo "OK: $(MANIFEST) and $(COMPOSE) use $(TAG); rclone is pinned"

# Official Umbrel app linter. It expects a clone of getumbrel/umbrel-apps with
# its Node dependencies installed; --root points it at this repository so the
# package does not have to be copied into that clone:
#   git clone https://github.com/getumbrel/umbrel-apps /tmp/umbrel-apps
#   (cd /tmp/umbrel-apps && npm ci)
#   make lint UMBREL_APPS=/tmp/umbrel-apps
# Image checks are opt-in because before the first publish the digest is still
# the zero placeholder.
lint:
	@test -n "$(UMBREL_APPS)" || { echo "error: set UMBREL_APPS=/path/to/umbrel-apps (clone + npm ci), see Makefile"; exit 1; }
	cd "$(UMBREL_APPS)" && npm run lint:apps -- cloudferry-migrator --root "$(CURDIR)"

lint-images:
	@test -n "$(UMBREL_APPS)" || { echo "error: set UMBREL_APPS=/path/to/umbrel-apps (clone + npm ci), see Makefile"; exit 1; }
	cd "$(UMBREL_APPS)" && npm run lint:apps -- cloudferry-migrator --root "$(CURDIR)" --check-images
