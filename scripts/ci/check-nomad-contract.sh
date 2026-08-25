#!/usr/bin/env sh
# Validate the deploy contract embedded in each Nomad jobspec.
# This is intentionally dependency-free so it runs in Husky and CI.

set -eu

fail=0

error() { printf 'ERROR: %s\n' "$*" >&2; fail=1; }

meta_value() {
  file="$1"; key="$2"
  sed -n '/# veridian-contract:start/,/# veridian-contract:end/p' "$file" |
    awk -v wanted="\"$key\"" '$1 == wanted && $2 == "=" { gsub(/^"|"$/, "", $3); print $3; exit }'
}

require_meta() {
  file="$1"; key="$2"; expected="${3:-}"; value="$(meta_value "$file" "$key")"
  if [ -z "$value" ]; then error "$file: missing contract key $key"
  elif [ -n "$expected" ] && [ "$value" != "$expected" ]; then error "$file: $key=$value, expected $expected"; fi
}

files="${*:-$(find deploy -maxdepth 1 -type f -name '*.nomad.hcl' -print | sort)}"
[ -n "$files" ] || error "no deploy/*.nomad.hcl jobspec found"

for file in $files; do
  [ -f "$file" ] || { error "$file: not a file"; continue; }
  start_count="$(grep -c '^# veridian-contract:start$' "$file" || true)"
  end_count="$(grep -c '^# veridian-contract:end$' "$file" || true)"
  if [ "$start_count" != "1" ] || [ "$end_count" != "1" ]; then error "$file: expected exactly one bounded veridian contract block"; continue; fi
  job_id="$(sed -n 's/^job "\([^"]*\)".*/\1/p' "$file" | head -1)"
  [ -n "$job_id" ] || { error "$file: missing job ID"; continue; }

  require_meta "$file" veridian.contract.version 1
  require_meta "$file" veridian.managed_by repo
  require_meta "$file" veridian.owner
  require_meta "$file" veridian.objective
  require_meta "$file" veridian.rto_minutes
  require_meta "$file" veridian.rpo_minutes
  require_meta "$file" veridian.state
  require_meta "$file" veridian.mobility

  priority="$(awk '$1 == "priority" && $2 == "=" { print $3; exit }' "$file")"
  case "$priority" in ''|*[!0-9]*) error "$file: priority must be an explicit integer" ;; esac
  case "$job_id" in
    *-staging)
      production_job="${job_id%-staging}"
      require_meta "$file" veridian.environment staging
      require_meta "$file" veridian.tier saas-staging
      require_meta "$file" veridian.criticality C
      require_meta "$file" veridian.preemptible true
      require_meta "$file" veridian.production_job "$production_job"
      require_meta "$file" veridian.promotion_policy non-production
      if [ -n "$priority" ] && [ "$priority" -gt 50 ]; then error "$file: staging priority $priority must be <= 50"; fi
      ;;
    *)
      require_meta "$file" veridian.environment production
      require_meta "$file" veridian.tier saas-prod
      require_meta "$file" veridian.preemptible false
      require_meta "$file" veridian.staging_job "${job_id}-staging"
      require_meta "$file" veridian.promotion_policy staging-required
      criticality="$(meta_value "$file" veridian.criticality)"
      case "$criticality" in A|B) ;; *) error "$file: production criticality must be A or B" ;; esac
      if [ -n "$priority" ] && [ "$priority" -lt 70 ]; then error "$file: production priority $priority must be >= 70"; fi
      if [ ! -f "deploy/${job_id}-staging.nomad.hcl" ]; then error "$file: declared staging gate has no deploy/${job_id}-staging.nomad.hcl"; fi
      ;;
  esac
  if grep -Eq '^[[:space:]]*default[[:space:]]*=[[:space:]]*"[^"]*latest' "$file"; then error "$file: floating image_tag default is forbidden"; fi
  if grep -Eq '^[[:space:]]*image[[:space:]]*=[[:space:]]*"[^"]*:latest"' "$file"; then error "$file: literal :latest image is forbidden"; fi
done

[ "$fail" -eq 0 ] || exit 1
printf 'Nomad contracts: OK (%s)\n' "$(printf '%s\n' $files | wc -l | tr -d ' ')"
