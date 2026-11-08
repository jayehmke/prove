#!/usr/bin/env bash
# Runs prove verify, then writes prove show --format md to the job summary
# whatever verify found. The exit code is verify's.
#
# PROVE_MODE=run is for a job with no signing key: it runs the checks prove
# plan says to run (no valid, current, trusted record) with prove run
# --unsigned and writes their results to PROVE_RESULTS. It verifies nothing
# and fails only on a usage error. PROVE_MODE=sign is for a later job that
# holds the key and runs no repo code: it signs those results with prove
# sign, then verifies.
set -uo pipefail

commit="${PROVE_COMMIT:-HEAD}"
results="${PROVE_RESULTS:-}"
read -ra extra <<<"${PROVE_ARGS:-}"

if [ "${PROVE_FETCH_RECORDS:-true}" = true ]; then
  git fetch --quiet origin '+refs/prove/*:refs/prove/*' ||
    echo "::warning::prove: could not fetch refs/prove/* from origin"
fi
# The commit may not be the one checked out (a pull request's head).
if ! git cat-file -e "$commit^{commit}" 2>/dev/null; then
  git fetch --quiet --depth=1 origin "$commit" ||
    echo "::warning::prove: could not fetch $commit from origin"
fi

no_sampling=()
case "${PROVE_MODE:-verify}" in
verify) ;;
run)
  mkdir -p "$results" || exit 2
  # The plan also keeps the results folder from being empty when nothing
  # is stale: actions/upload-artifact uploads no artifact for an empty
  # folder, and the sign job's download then fails.
  prove plan "$commit" | tee "$results/plan.txt"
  # A failing check is a result like any other; the sign job's verify
  # reports it. --checkout-policy: plan, run and verify all use this
  # checkout's policy.
  prove run --stale --unsigned --out "$results" --commit "$commit" --checkout-policy
  if [ $? -eq 2 ]; then exit 2; fi
  exit 0
  ;;
sign)
  for arg in ${extra[@]+"${extra[@]}"}; do
    case "$arg" in
    -sample | -sample=* | --sample | --sample=*)
      echo "::error::prove: sign mode can't take --sample: sampling reruns checks, and this job holds the signing key"
      exit 2
      ;;
    esac
  done
  prove sign "$results" --commit "$commit" --checkout-policy
  if [ $? -eq 2 ]; then exit 2; fi
  # Without it verify would rerun the share of checks the policy's
  # sample: names, in the job that holds the key.
  no_sampling=(--sample 0)
  ;;
*)
  echo "::error::prove: mode must be verify, run or sign, not ${PROVE_MODE}"
  exit 2
  ;;
esac

prove verify "$commit" ${no_sampling[@]+"${no_sampling[@]}"} ${extra[@]+"${extra[@]}"}
code=$?

summary="${GITHUB_STEP_SUMMARY:-/dev/stdout}"
if ! prove show "$commit" --format md >>"$summary"; then
  echo "::warning::prove: prove show failed, so the job summary has no prove table"
fi
exit "$code"
