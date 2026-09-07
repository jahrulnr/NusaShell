#!/usr/bin/env bash
# Minimal PR watcher for babysit-pr skill. Requires: gh (GitHub CLI), jq.
# Emits JSON snapshots; the agent reads `actions` and decides.
set -euo pipefail

PR="${PR:-auto}"
INTERVAL="${INTERVAL:-60}"
TMP="${TMPDIR:-/tmp}"
STATE_FILE="${STATE_FILE:-$TMP/babysit-pr-state.json}"

usage() {
  echo "usage: pr_watch.sh --pr <n|url|auto> (--once | --watch | --retry-failed-now) [--interval SECONDS]"
}

resolve_pr() {
  if [ "$PR" = "auto" ]; then
    gh pr view --json number,url,headRefName --jq '.number' 2>/dev/null || {
      echo '{"error":"no PR found for current branch"}' >&2; exit 1; }
  else
    echo "$PR"
  fi
}

pr_number=$(resolve_pr)

snapshot() {
  gh pr view "$pr_number" --json number,title,state,isDraft,mergeable,reviewDecision,headRefName,url,author \
    --jq '{number,title,state,isDraft,mergeable,reviewDecision,headRefName,url,author:.author.login}' \
    > $TMP/babysit-pr-view.json

  # checks: aggregate statusCheckRollup per name
  gh pr view "$pr_number" --json statusCheckRollup \
    --jq '{checks: [.statusCheckRollup[]? | select(.__typename=="CheckRun" or .__typename=="StatusContext") | {name: (.name // .context), status, conclusion, detailsUrl}]}' \
    > $TMP/babysit-pr-checks.json 2>/dev/null || echo '{"checks":[]}' > $TMP/babysit-pr-checks.json

  # reviews: only published; skip own (authenticated operator)
  me=$(gh api user --jq '.login')
  gh pr view "$pr_number" --json reviews \
    --jq --arg me "$me" '{reviews: [.reviews[]? | select(.state != "PENDING" and .author.login != $me) | {author: .author.login, state, submittedAt, body: (.body | if length > 200 then .[0:200] + "..." else . end)}]}' \
    > $TMP/babysit-pr-reviews.json 2>/dev/null || echo '{"reviews":[]}' > $TMP/babysit-pr-reviews.json

  jq -s '.[0] * .[1] * .[2]' \
    $TMP/babysit-pr-view.json $TMP/babysit-pr-checks.json $TMP/babysit-pr-reviews.json > $TMP/babysit-pr-raw.json

  # build actions[]
  jq '
    .actions = []
    | if .state == "MERGED" or .state == "CLOSED" then
        .actions += [{"id":"stop_pr_closed","detail":.state}]
      else
        ( .checks |
          if any(.[]; .conclusion == "FAILURE" or .conclusion == "TIMED_OUT" or .status == "COMPLETED" and .conclusion == "FAILURE") then
            .actions += [{"id":"diagnose_ci_failure","failed":([ .[] | select(.conclusion == "FAILURE" or .conclusion == "TIMED_OUT") | .name ])}]
          else . end )
        | ( if any(.checks[]?; .conclusion == "FAILURE") and ([.actions[]?.id] | index("diagnose_ci_failure")) then
              .actions += [{"id":"retry_failed_checks","budget":3}]
            else . end )
        | ( if (.reviews | length) > 0 then
              .actions += [{"id":"process_review_comment","items":(.reviews | map({author: .author, state, preview: .body}))}]
            else . end )
      end
  ' $TMP/babysit-pr-raw.json > $TMP/babysit-pr-snapshot.json
  cat $TMP/babysit-pr-snapshot.json
  cp $TMP/babysit-pr-snapshot.json "$STATE_FILE"
}

retry_failed() {
  run_id=$(gh run list --branch "$(gh pr view "$pr_number" --json headRefName --jq .headRefName)" --limit 1 --json databaseId --jq '.[0].databaseId' 2>/dev/null || echo "")
  if [ -n "$run_id" ]; then
    gh run rerun "$run_id" --failed
    echo "{\"action\":\"retried_failed_checks\",\"run\":$run_id}"
  else
    echo '{"action":"none","detail":"no workflow run found"}'
  fi
}

case "${1:-}" in
  --once) snapshot ;;
  --retry-failed-now) retry_failed ;;
  --watch)
    while true; do snapshot; sleep "$INTERVAL"; done ;;
  *) usage; exit 2 ;;
esac