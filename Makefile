GOCACHE ?= $(CURDIR)/.gocache
GOMODCACHE ?= $(CURDIR)/.gomodcache

MANIFEST := cloudferry-migrator/umbrel-app.yml
COMPOSE := cloudferry-migrator/docker-compose.yml
EXPORTS := cloudferry-migrator/exports.sh
STANDALONE_COMPOSE := deploy/standalone/docker-compose.yml
DOCKERFILE := Dockerfile

# Single source of truth for the GitHub owner: the literal image reference in
# cloudferry-migrator/docker-compose.yml. That string is what Umbrel pulls, and
# umbreld does not expand ${OWNER} from exports.sh when pulling, so a variable
# there would resolve to an empty owner and the install would 404. Everything
# else (manifest URLs, Dockerfile labels, IMAGE below) is derived from compose
# and rewritten together with `make set-owner OWNER=<login>`.
OWNER ?= $(shell sed -n 's/^[[:space:]]*image: ghcr\.io\/\([a-z0-9-]*\)\/cloud-ferry:.*/\1/p' $(COMPOSE) | head -n 1)
IMAGE ?= ghcr.io/$(OWNER)/cloud-ferry
REPO := $(patsubst ghcr.io/%,%,$(IMAGE))

# One tag format everywhere, without the leading v: git tag v0.3.1 ->
# image tag 0.3.1 -> manifest version "0.3.1" -> compose
# cloud-ferry:0.3.1@sha256:<manifest-list digest>.
# The version is changed by `make set-version VERSION=x.y.z`, never by hand.
TAG ?= 0.3.2
PLATFORMS ?= linux/amd64,linux/arm64
RCLONE_IMAGE := rclone/rclone:1.76.0@sha256:7cbeec24a44f206067597c0368be4de90ae19a64d5113c7d1938bd737a81e466
TAG_RE := $(subst .,\.,$(TAG))
ZERO_DIGEST := sha256:0000000000000000000000000000000000000000000000000000000000000000

# IMAGE_CHECK=0 skips the checks that talk to GHCR (no Docker or no network).
# `make release-tag` warns loudly but still requires the pin to be committed.
IMAGE_CHECK ?= 1

.PHONY: help fmt vet test build run docker docker-push digest pin-digest set-owner set-version owner check-version check-image release-tag lint lint-images

help:
	@echo "make test           unit tests with the race detector, no network and no rclone"
	@echo "make build          binary in bin/cloudferry"
	@echo "make run            run locally, rclone is spawned (SPAWN_RCLONE=true)"
	@echo "make docker         build the image for the host platform"
	@echo "make docker-push    build linux/amd64+linux/arm64 and push to ghcr.io"
	@echo "make digest         print the published manifest-list digest for TAG"
	@echo "make pin-digest     pin the published manifest-list digest into the compose file"
	@echo "make set-owner      rewrite the image owner everywhere (make set-owner OWNER=<login>)"
	@echo "make set-version    set TAG, the manifest version and reset the compose digest to zero"
	@echo "make check-version  offline release contract for TAG: version, real digest, pins"
	@echo "make check-image    registry contract for TAG: digest, linux/amd64+linux/arm64, public"
	@echo "make release-tag    run all checks and create the annotated tag v<TAG> (never pushes)"
	@echo "make lint           official Umbrel app linter (UMBREL_APPS=<clone>)"
	@echo "make lint-images    same, plus registry image checks (published image required)"

fmt:
	gofmt -w ./cmd ./internal

vet:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go vet ./...

test:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go test -race -count=1 ./...

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
# The Dockerfile has no default VERSION, so every build must pass one.
docker:
	@test -n "$(OWNER)" || { echo "error: cannot read the image owner from $(COMPOSE)" >&2; exit 1; }
	@printf '%s' "$(TAG)" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$$' || { echo "error: TAG=$(TAG) must look like 0.3.1 (no leading v)" >&2; exit 1; }
	docker buildx build --build-arg VERSION=$(TAG) --build-arg REVISION=$(shell git rev-parse HEAD 2>/dev/null) -t $(IMAGE):$(TAG) --load .

# The published image: multi-arch manifest list, pushed to GHCR. This is what
# cloudferry-migrator/docker-compose.yml pins by digest.
docker-push:
	@test -n "$(OWNER)" || { echo "error: cannot read the image owner from $(COMPOSE)" >&2; exit 1; }
	@printf '%s' "$(TAG)" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$$' || { echo "error: TAG=$(TAG) must look like 0.3.1 (no leading v)" >&2; exit 1; }
	docker buildx build --platform $(PLATFORMS) --build-arg VERSION=$(TAG) \
		--build-arg REVISION=$(shell git rev-parse HEAD 2>/dev/null) \
		-t $(IMAGE):$(TAG) --push .

# Print the manifest-list digest for the published tag. Parsed from the plain
# `imagetools inspect` output on purpose: it does not depend on buildx template
# fields, and the same output lists the platforms next to it.
digest:
	@docker buildx imagetools inspect $(IMAGE):$(TAG) | sed -n 's/^Digest:[[:space:]]*//p' | head -n 1

# Print the owner the compose file pins; CI compares it with the repository
# owner so a fork never publishes into someone else's namespace.
owner:
	@echo $(OWNER)

# Pin the published manifest-list digest into the Umbrel compose file. Reads it
# from the registry, or takes it from DIGEST=sha256:... for CI and offline use.
# The digest must be the multi-arch index digest, not an arch-specific one, and
# it must never be the zero placeholder.
pin-digest:
	@test -n "$(OWNER)" || { echo "error: cannot read the image owner from $(COMPOSE)" >&2; exit 1; }
	@grep -Eq "cloud-ferry:$(TAG_RE)@sha256:[0-9a-f]{64}" $(COMPOSE) \
		|| { echo "error: $(COMPOSE) does not reference cloud-ferry:$(TAG) yet; run make set-version VERSION=$(TAG) first" >&2; exit 1; }
	@digest="$(DIGEST)"; \
	if [ -z "$$digest" ]; then \
		inspect="$$(docker buildx imagetools inspect $(IMAGE):$(TAG) 2>/dev/null)" \
			|| { echo "error: cannot read $(IMAGE):$(TAG): publish the image or pass DIGEST=sha256:..."; exit 1; }; \
		digest="$$(printf '%s\n' "$$inspect" | sed -n 's/^Digest:[[:space:]]*//p' | head -n 1)"; \
	fi; \
	case "$$digest" in sha256:*) ;; *) digest="sha256:$$digest" ;; esac; \
	printf '%s' "$$digest" | grep -Eq '^sha256:[0-9a-f]{64}$$' \
		|| { echo "error: unexpected digest '$$digest' (want the sha256 manifest-list digest)"; exit 1; }; \
	if [ "$$digest" = "$(ZERO_DIGEST)" ]; then \
		echo "error: refusing the zero digest: the image was not published"; \
		exit 1; \
	fi; \
	sed -i.bak -E 's/cloud-ferry:$(TAG_RE)@sha256:[0-9a-f]{64}/cloud-ferry:$(TAG)@'"$$digest"'/' $(COMPOSE); \
	rm -f $(COMPOSE).bak; \
	grep -n "cloud-ferry:$(TAG)@" $(COMPOSE); \
	echo "next: make check-version TAG=$(TAG) && git commit $(COMPOSE)"

# Replace the image owner in the compose image reference, the manifest URLs and
# the Dockerfile labels. GHCR paths are lowercase, so only ^[a-z0-9-]+$ is
# accepted; pass the login already lowercased, for example chikurov.
# The rewrite matches whatever owner is there now (including a legacy
# mixed-case one), so it is idempotent and cannot fail on a stale grep.
set-owner:
	@test "$(origin OWNER)" = "command line" \
		|| { echo "usage: make set-owner OWNER=<github-login> (lowercase letters, digits and hyphens only)" >&2; exit 1; }
	@printf '%s' "$(OWNER)" | grep -Eq '^[a-z0-9-]+$$' \
		|| { echo "error: OWNER must be lowercase (GHCR paths are case-sensitive): pass the login as chikurov, not Chikurov" >&2; exit 1; }
	sed -i.bak -E \
		-e 's#^([[:space:]]*image: )ghcr\.io/[A-Za-z0-9-]+/cloud-ferry:#\1ghcr.io/$(OWNER)/cloud-ferry:#' \
		-e 's#github\.com/[A-Za-z0-9-]+/cloud-ferry#github.com/$(OWNER)/cloud-ferry#g' \
		-e 's#raw\.githubusercontent\.com/[A-Za-z0-9-]+/cloud-ferry#raw.githubusercontent.com/$(OWNER)/cloud-ferry#g' \
		$(COMPOSE) $(MANIFEST) $(DOCKERFILE)
	rm -f $(COMPOSE).bak $(MANIFEST).bak $(DOCKERFILE).bak
	@grep -Eq "^[[:space:]]*image: ghcr\.io/$(OWNER)/cloud-ferry:[0-9]+\.[0-9]+\.[0-9]+@sha256:[0-9a-f]{64}$$" $(COMPOSE) \
		|| { echo "error: $(COMPOSE) does not use ghcr.io/$(OWNER)/cloud-ferry; check the image line" >&2; exit 1; }
	@grep -Fq "github.com/$(OWNER)/cloud-ferry" $(MANIFEST) \
		|| { echo "error: $(MANIFEST) still points at another owner" >&2; exit 1; }
	@grep -Fq "github.com/$(OWNER)/cloud-ferry" $(DOCKERFILE) \
		|| { echo "error: $(DOCKERFILE) labels still point at another owner" >&2; exit 1; }
	@echo "owner is now $(OWNER); check the diff before committing:"
	@git diff --stat -- $(COMPOSE) $(MANIFEST) $(DOCKERFILE) 2>/dev/null || true

# Move to a new release version: the Makefile TAG, the manifest version and the
# compose image tag change together, and the compose digest is reset to zero so
# check-version fails until the new image is published and pinned.
set-version:
	@test -n "$(VERSION)" || { echo "usage: make set-version VERSION=0.3.2" >&2; exit 1; }
	@printf '%s' "$(VERSION)" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$$' || { echo "error: VERSION must be x.y.z without the leading v" >&2; exit 1; }
	@if [ "$(VERSION)" = "$(TAG)" ] && ! grep -Fq "cloud-ferry:$(TAG)@$(ZERO_DIGEST)" $(COMPOSE); then \
		if [ "$(FORCE)" = "1" ]; then \
			echo "warning: resetting the pin of $(VERSION) to zero (FORCE=1)"; \
		else \
			echo "error: $(VERSION) is already pinned; bump to a new version (FORCE=1 resets the pin to zero)" >&2; \
			exit 1; \
		fi; \
	fi
	sed -i.bak -E 's/^TAG \?= .*/TAG ?= $(VERSION)/' Makefile
	sed -i.bak -E 's/^version: .*/version: "$(VERSION)"/' $(MANIFEST)
	sed -i.bak -E 's/cloud-ferry:[0-9]+\.[0-9]+\.[0-9]+@sha256:[0-9a-f]{64}/cloud-ferry:$(VERSION)@$(ZERO_DIGEST)/' $(COMPOSE)
	rm -f Makefile.bak $(MANIFEST).bak $(COMPOSE).bak
	@grep -Fq "cloud-ferry:$(VERSION)@$(ZERO_DIGEST)" $(COMPOSE) \
		|| { echo "error: $(COMPOSE) did not get cloud-ferry:$(VERSION)@sha256:<64 zeros>" >&2; exit 1; }
	@grep -Eq "^version: \"$(VERSION)\"$$" $(MANIFEST) || { echo "error: $(MANIFEST) version was not updated" >&2; exit 1; }
	@grep -Eq "^TAG \?= $(subst .,\.,$(VERSION))$$" Makefile || { echo "error: Makefile TAG was not updated" >&2; exit 1; }
	@echo "version is now $(VERSION); the compose digest is zero on purpose"
	@echo "next: commit, publish the image, then make pin-digest TAG=$(VERSION) DIGEST=sha256:..."

# Offline release contract. A zero digest here means the install would pull an
# image that does not exist, so it is a hard error, never a warning.
check-version:
	@printf '%s' "$(TAG)" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$$' || { echo "error: TAG=$(TAG) must look like 0.3.2 (no leading v)" >&2; exit 1; }
	@grep -Eq "^TAG \?= $(TAG_RE)$$" Makefile || { echo "error: Makefile TAG is not $(TAG); run make set-version VERSION=$(TAG)" >&2; exit 1; }
	@grep -Eq "^version: \"?$(TAG_RE)\"?$$" $(MANIFEST) || { echo "error: $(MANIFEST) version is not $(TAG)" >&2; exit 1; }
	@test -n "$(OWNER)" || { echo "error: cannot read the image owner from $(COMPOSE)" >&2; exit 1; }
	@printf '%s' "$(OWNER)" | grep -Eq '^[a-z0-9-]+$$' || { echo "error: image owner '$(OWNER)' in $(COMPOSE) must be lowercase" >&2; exit 1; }
	@test "$$(grep -Ec "^[[:space:]]*image: ghcr\.io/" $(COMPOSE))" -eq 1 || { echo "error: $(COMPOSE) must reference exactly one ghcr.io image" >&2; exit 1; }
	@grep -Eq "^[[:space:]]*image: ghcr\.io/$(OWNER)/cloud-ferry:$(TAG_RE)@sha256:[0-9a-f]{64}$$" $(COMPOSE) \
		|| { echo "error: $(COMPOSE) must reference ghcr.io/$(OWNER)/cloud-ferry:$(TAG)@sha256:<manifest-list digest>" >&2; exit 1; }
	@! grep -Fq "cloud-ferry:$(TAG)@$(ZERO_DIGEST)" $(COMPOSE) \
		|| { echo "error: the digest is the zero placeholder; the image was never published. Pin the real index digest with make pin-digest TAG=$(TAG) DIGEST=sha256:..." >&2; exit 1; }
	@! grep -Eq '^[[:space:]]*image:.*\$$\{OWNER\}' $(COMPOSE) \
		|| { echo 'error: $${OWNER} is not expanded by umbreld when pulling; write the literal owner into $(COMPOSE)' >&2; exit 1; }
	@! grep -Eq '^[[:space:]]*export OWNER=' $(EXPORTS) \
		|| { echo "error: $(EXPORTS) must not export OWNER; $(COMPOSE) holds the literal owner, see INSTALL.md" >&2; exit 1; }
	@sh -n $(EXPORTS) || { echo "error: $(EXPORTS) is not valid sh" >&2; exit 1; }
	@grep -Fq "$(RCLONE_IMAGE)" $(COMPOSE) || { echo "error: $(COMPOSE) rclone image is not pinned" >&2; exit 1; }
	@grep -Fq "$(RCLONE_IMAGE)" $(STANDALONE_COMPOSE) || { echo "error: $(STANDALONE_COMPOSE) rclone image is not pinned" >&2; exit 1; }
	@grep -Eq '^ARG VERSION$$' $(DOCKERFILE) || { echo "error: $(DOCKERFILE) must declare ARG VERSION without a default value" >&2; exit 1; }
	@grep -Fq "github.com/$(OWNER)/cloud-ferry" $(MANIFEST) || { echo "error: $(MANIFEST) URLs do not use owner $(OWNER); run make set-owner OWNER=$(OWNER)" >&2; exit 1; }
	@grep -Fq "raw.githubusercontent.com/$(OWNER)/cloud-ferry" $(MANIFEST) || { echo "error: $(MANIFEST) icon URL does not use owner $(OWNER)" >&2; exit 1; }
	@grep -Fq "github.com/$(OWNER)/cloud-ferry" $(DOCKERFILE) || { echo "error: $(DOCKERFILE) labels do not use owner $(OWNER)" >&2; exit 1; }
	@digest="$$(grep -Eo "cloud-ferry:$(TAG_RE)@sha256:[0-9a-f]{64}" $(COMPOSE) | head -n 1)"; \
	echo "OK: $(TAG) matches Makefile and $(MANIFEST); $(COMPOSE) pins $$digest; rclone and Dockerfile are pinned"

# Registry contract. The pin must be the published index digest for TAG, the
# index must carry both Umbrel architectures, and the package must be public:
# Umbrel pulls without credentials, so a private package fails the install.
check-image:
	@if [ "$(IMAGE_CHECK)" = "0" ]; then echo "warning: image checks skipped (IMAGE_CHECK=0)"; exit 0; fi; \
	command -v docker >/dev/null 2>&1 || { echo "error: docker is required for image checks; use IMAGE_CHECK=0 to skip them" >&2; exit 1; }; \
	pinned="$$(grep -Eo "cloud-ferry:$(TAG_RE)@sha256:[0-9a-f]{64}" $(COMPOSE) | head -n 1 | grep -Eo 'sha256:[0-9a-f]{64}')"; \
	[ -n "$$pinned" ] || { echo "error: $(COMPOSE) does not pin a digest for $(TAG)" >&2; exit 1; }; \
	published="$$(docker buildx imagetools inspect "$(IMAGE):$(TAG)" 2>/dev/null)" \
		|| { echo "error: cannot read $(IMAGE):$(TAG): it was never published, or the GHCR package is private (Umbrel pulls anonymously). Publish it with make docker-push TAG=$(TAG) or the CI publish job" >&2; exit 1; }; \
	index_digest="$$(printf '%s\n' "$$published" | sed -n 's/^Digest:[[:space:]]*//p' | head -n 1)"; \
	[ -n "$$index_digest" ] || { echo "error: cannot parse the index digest of $(IMAGE):$(TAG)" >&2; exit 1; }; \
	[ "$$index_digest" = "$$pinned" ] \
		|| { echo "error: $(COMPOSE) pins $$pinned but $(IMAGE):$(TAG) is $$index_digest; do not tag yet: re-pin the published index (make pin-digest TAG=$(TAG) DIGEST=$$index_digest) and commit, or publish a new version" >&2; exit 1; }; \
	case "$$published" in \
		*linux/amd64*) ;; \
		*) echo "error: $(IMAGE):$(TAG) is missing linux/amd64; published index: $$index_digest" >&2; exit 1 ;; \
	esac; \
	case "$$published" in \
		*linux/arm64*) ;; \
		*) echo "error: $(IMAGE):$(TAG) is missing linux/arm64; published index: $$index_digest" >&2; exit 1 ;; \
	esac; \
	if command -v curl >/dev/null 2>&1; then \
		token="$$(curl -fsS "https://ghcr.io/token?scope=repository:$(REPO):pull&service=ghcr.io" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')"; \
		code="$$(curl -s -o /dev/null -w '%{http_code}' \
			-H 'Accept: application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json' \
			-H "Authorization: Bearer $$token" \
			"https://ghcr.io/v2/$(REPO)/manifests/$(TAG)")"; \
		[ "$$code" = "200" ] \
			|| { echo "error: $(IMAGE):$(TAG) is not anonymously pullable (HTTP $$code); Umbrel pulls without credentials. Set GitHub -> Packages -> cloud-ferry -> Package settings -> Change visibility -> Public" >&2; exit 1; }; \
		echo "OK: $(IMAGE):$(TAG) is $$index_digest, linux/amd64+linux/arm64, public"; \
	else \
		echo "warning: curl not found, public visibility was not checked"; \
	fi

# The only place a release tag is created. It refuses to tag anything that the
# store could not install: the digest is real, the version matches everywhere,
# the registry has exactly this index for both architectures, and the pin is
# committed (not just in the working tree). It never pushes.
release-tag: check-version check-image
	@test "$(origin TAG)" = "command line" || { echo "error: pass the release version explicitly: make release-tag TAG=x.y.z" >&2; exit 1; }
	@git rev-parse --verify HEAD >/dev/null 2>&1 || { echo "error: not a git repository" >&2; exit 1; }
	@if git rev-parse -q --verify "refs/tags/v$(TAG)" >/dev/null 2>&1; then echo "error: tag v$(TAG) already exists (never move a published tag)" >&2; exit 1; fi
	@git diff --quiet HEAD -- Makefile $(COMPOSE) $(MANIFEST) $(DOCKERFILE) $(EXPORTS) \
		|| { echo "error: release files have uncommitted changes; commit the version bump and the pin first" >&2; git status --short -- Makefile $(COMPOSE) $(MANIFEST) $(DOCKERFILE) $(EXPORTS) >&2; exit 1; }
	@git show HEAD:$(COMPOSE) | grep -Fq "cloud-ferry:$(TAG)@sha256:" \
		|| { echo "error: the commit at HEAD does not pin cloud-ferry:$(TAG); the tag would capture an unpinned image" >&2; exit 1; }
	git tag -a "v$(TAG)" -m "Cloud Ferry $(TAG)"
	@echo "created annotated tag v$(TAG) at $$(git rev-parse --short HEAD)"
	@echo "not pushed. After review: git push origin v$(TAG)"

# Official Umbrel app linter. It expects a clone of getumbrel/umbrel-apps with
# its Node dependencies installed; --root points it at this repository so the
# package does not have to be copied into that clone:
#   git clone https://github.com/getumbrel/umbrel-apps /tmp/umbrel-apps
#   (cd /tmp/umbrel-apps && npm ci)
#   make lint UMBREL_APPS=/tmp/umbrel-apps
# Image checks are opt-in because before the first publish the digest is still
# the zero placeholder.
lint:
	@test -n "$(UMBREL_APPS)" || { echo "error: set UMBREL_APPS=/path/to/umbrel-apps (clone + npm ci), see Makefile" >&2; exit 1; }
	cd "$(UMBREL_APPS)" && npm run lint:apps -- cloudferry-migrator --root "$(CURDIR)"

lint-images:
	@test -n "$(UMBREL_APPS)" || { echo "error: set UMBREL_APPS=/path/to/umbrel-apps (clone + npm ci), see Makefile" >&2; exit 1; }
	cd "$(UMBREL_APPS)" && npm run lint:apps -- cloudferry-migrator --root "$(CURDIR)" --check-images
