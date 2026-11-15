# Sourced by umbrelOS before rendering templates and starting containers.
# The rc password is a local service secret derived from the device seed; cloud
# tokens are never derived and never live here.

# There is deliberately no OWNER export here. docker-compose.yml must carry the
# literal image reference: umbreld does not expand variables from this file when
# it pulls the image, and an empty owner becomes ghcr.io//cloud-ferry (404).
# The owner lives in cloudferry-migrator/docker-compose.yml and is rewritten
# together with the manifest and the Dockerfile by:
#   make set-owner OWNER=<github-login>

export APP_CLOUDFERRY_RC_USER="cloudferry"
export APP_CLOUDFERRY_RC_PASS="$(derive_entropy "${app_entropy_identifier}-rc-pass")"
