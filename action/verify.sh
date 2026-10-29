#!/usr/bin/env bash
# Runs prove verify, then writes prove show --format md to the job summary
# whatever verify found. The exit code is verify's.
#
# PROVE_MODE=run first runs the checks prove plan says to run (no valid,
# current, trusted record), signing with this job's key, so a missing record
# costs a CI run instead of a red job.
set -uo pipefail

commit="${PROVE_COMMIT:-HEAD}"
read -ra extra <<<"${PROVE_ARGS:-}"

if [ "${PROVE_FETCH_RECORDS:-true}" = true ]; then
  git fetch --quiet origin '+refs/prove/*:refs/prove/*' ||
    echo "::warning::prove: could not fetch refs/prove/* from origin"
fi
# A pull request checkout holds the merge commit, not the head it verifies.
if ! git cat-file -e "$commit^{commit}" 2>/dev/null; then
  git fetch --quiet --depth=1 origin "$commit" ||
    echo "::warning::prove: could not fetch $commit from origin"
fi

case "${PROVE_MODE:-verify}" in
verify) ;;
run)
  prove plan "$commit"
  # A failing check is recorded as failed and verify reports it below.
  prove run --stale --commit "$commit"
  if [ $? -eq 2 ]; then exit 2; fi
  ;;
*)
  echo "::error::prove: mode must be verify or run, not ${PROVE_MODE}"
  exit 2
  ;;
esac

prove verify "$commit" ${extra[@]+"${extra[@]}"}
code=$?

summary="${GITHUB_STEP_SUMMARY:-/dev/stdout}"
if ! prove show "$commit" --format md >>"$summary"; then
  echo "::warning::prove: prove show failed, so the job summary has no prove table"
fi
exit "$code"
