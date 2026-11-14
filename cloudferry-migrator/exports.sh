# Sourced by umbrelOS before rendering templates and starting containers.
# The rc password is a local service secret derived from the device seed; cloud
# tokens are never derived and never live here.

# GitHub owner of the published image. docker-compose.yml references it as
# ghcr.io/${OWNER}/cloud-ferry and the Makefile reads it from this line, so it
# is the single place to change:
#   make set-owner OWNER=<github-login>
export OWNER="chikurov"

export APP_CLOUDFERRY_RC_USER="cloudferry"
export APP_CLOUDFERRY_RC_PASS="$(derive_entropy "${app_entropy_identifier}-rc-pass")"
